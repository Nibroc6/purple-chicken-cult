// Package webpush sends Web Push messages (RFC 8291 aes128gcm, RFC 8292 VAPID).
// The "log" driver (Go tests, dev without VAPID keys) records payloads in an
// in-memory ring readable by tests. Push is enabled only with VAPID keys
// (config.PushEnabled, D20): in prod without them New returns ErrDisabled and
// push reports {enabled:false} (fail closed).
//
// Privacy: the push service sees only an encrypted, fixed-size record (every
// message is padded to the 4096-byte record size, so its length says nothing
// about its text), the endpoint it issued itself and a TTL. Payloads are built
// by gate/notify from catalog text and never carry post bodies.
//
// Endpoint policy (the sender posts to client-supplied URLs, so it must not be
// an SSRF primitive): https on port 443, no userinfo, no proxy, no redirects,
// and every dial goes to a public address (the safefetch deny list). Loopback
// endpoints on any port are accepted only under PCC_ENV=test with test hooks,
// for the local fake push endpoints of the tests.
//
// Owner: P1-W05.
package webpush

import (
	"context"
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"

	wp "github.com/SherClockHolmes/webpush-go"

	"github.com/Nibroc6/purple-chicken-cult/internal/platform/config"
)

// Limits.
const (
	// MaxEndpointLen bounds a subscription endpoint URL.
	MaxEndpointLen = 2048
	// RecordSize is the fixed encrypted record size (RFC 8188); every payload is
	// padded to it.
	RecordSize = 4096
	// MaxPayload is the largest plaintext payload that fits one record.
	MaxPayload = 3000
	// Timeout bounds one delivery (dial, TLS, response headers).
	Timeout = 10 * time.Second
)

// Subscription is a browser push subscription.
type Subscription struct {
	Endpoint string
	P256dh   string // base64url, uncompressed P-256 point (65 bytes)
	Auth     string // base64url, 16 bytes
}

// Sender delivers one encrypted push message. gone reports that the endpoint is
// permanently invalid (404/410) and must be deleted.
type Sender interface {
	Send(ctx context.Context, sub Subscription, payload []byte, ttl time.Duration) (gone bool, err error)
}

// Message is the JSON payload the service worker shows (web/src/sw/push.ts).
// Title is server-rendered text from the recipient's catalog; with
// hide_content it is the generic text and nothing else is set but URL.
type Message struct {
	V     int    `json:"v"`
	Title string `json:"title"`
	Tag   string `json:"tag,omitempty"`
	URL   string `json:"url"`
}

// MessageVersion is Message.V.
const MessageVersion = 1

// Marshal encodes m, refusing payloads that would not fit one record.
func (m Message) Marshal() ([]byte, error) {
	m.V = MessageVersion
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	if len(b) > MaxPayload {
		return nil, ErrTooLarge
	}
	return b, nil
}

// Errors.
var (
	// ErrDisabled is returned when push is not configured in prod.
	ErrDisabled = errors.New("webpush: disabled (VAPID keys missing)")
	// ErrBadKeys is returned for malformed VAPID keys (the server refuses to start).
	ErrBadKeys = errors.New("webpush: malformed VAPID keys")
	// ErrEndpoint is returned for an endpoint outside the policy.
	ErrEndpoint = errors.New("webpush: endpoint not allowed")
	// ErrSubKeys is returned for malformed subscription keys.
	ErrSubKeys = errors.New("webpush: malformed subscription keys")
	// ErrBlocked is returned when a dial would reach a non-public address.
	ErrBlocked = errors.New("webpush: address blocked")
	// ErrTooLarge is returned for a payload that does not fit one record.
	ErrTooLarge = errors.New("webpush: payload too large")
	// ErrRejected is returned when the push service refuses a message.
	ErrRejected = errors.New("webpush: push service rejected the message")
)

// AllowLoopback reports whether c accepts loopback endpoints (PCC_ENV=test with
// test hooks only; config.Validate refuses test hooks in any other env).
func AllowLoopback(c *config.Config) bool {
	return c != nil && c.Env == config.EnvTest && c.TestHooks
}

// New returns the VAPID sender when VAPID keys are configured (malformed keys
// are ErrBadKeys), the log sender in dev/test without them, and ErrDisabled
// otherwise.
func New(c *config.Config) (Sender, error) {
	if c.PushEnabled() {
		return NewVAPID(c.Push.VAPIDPublic, c.Push.VAPIDPrivate, c.Push.VAPIDSubject, AllowLoopback(c))
	}
	if c.DevLike() {
		return NewLog(256), nil
	}
	return nil, ErrDisabled
}

// VAPID sends through push services with VAPID authentication.
type VAPID struct {
	pub, priv, sub string
	loopback       bool
	hc             *http.Client
}

// NewVAPID returns a VAPID sender after checking that the key pair is a valid
// P-256 pair (public: uncompressed point, private: 32-byte scalar, base64url).
func NewVAPID(pub, priv, subject string, allowLoopback bool) (*VAPID, error) {
	pb, err1 := decode(pub)
	sb, err2 := decode(priv)
	if err1 != nil || err2 != nil || len(pb) != 65 || len(sb) != 32 || strings.TrimSpace(subject) == "" {
		return nil, ErrBadKeys
	}
	k, err := ecdh.P256().NewPrivateKey(sb)
	if err != nil || string(k.PublicKey().Bytes()) != string(pb) {
		return nil, ErrBadKeys
	}
	v := &VAPID{pub: pub, priv: priv, sub: subject, loopback: allowLoopback}
	tr := &http.Transport{
		Proxy:                 nil, // never HTTP(S)_PROXY
		DialContext:           v.dial,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   Timeout,
		ResponseHeaderTimeout: Timeout,
		MaxIdleConns:          16,
		IdleConnTimeout:       90 * time.Second,
	}
	v.hc = &http.Client{
		Transport:     tr,
		Timeout:       Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return v, nil
}

// PublicKey is the application server key the browser subscribes with.
func (v *VAPID) PublicKey() string { return v.pub }

// Send encrypts payload for s and posts it. 404/410 are gone; any other
// non-2xx status, a policy violation or a transport error is an error.
func (v *VAPID) Send(ctx context.Context, s Subscription, payload []byte, ttl time.Duration) (bool, error) {
	if err := CheckEndpoint(s.Endpoint, v.loopback); err != nil {
		return false, err
	}
	if err := CheckKeys(s.P256dh, s.Auth); err != nil {
		return false, err
	}
	if len(payload) > MaxPayload {
		return false, ErrTooLarge
	}
	if ttl < 0 {
		ttl = 0
	}
	resp, err := wp.SendNotificationWithContext(ctx, payload, &wp.Subscription{
		Endpoint: s.Endpoint, Keys: wp.Keys{P256dh: s.P256dh, Auth: s.Auth},
	}, &wp.Options{
		Subscriber: v.sub, VAPIDPublicKey: v.pub, VAPIDPrivateKey: v.priv, TTL: int(ttl.Seconds()),
		RecordSize: RecordSize, Urgency: wp.UrgencyNormal, HTTPClient: v.hc,
	})
	if err != nil {
		if errors.Is(err, ErrBlocked) {
			return false, ErrBlocked
		}
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 16<<10))
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return true, nil
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return false, ErrRejected
	}
	return false, nil
}

// dial resolves the host and connects only to an allowed address; the socket's
// Control hook checks the address actually dialed again (rebinding-proof).
func (v *VAPID) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, ErrBlocked
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	d := net.Dialer{Timeout: Timeout, Control: v.control}
	last := error(ErrBlocked)
	for _, ip := range ips {
		if blocked(ip, v.loopback) {
			continue
		}
		conn, err := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		last = err
	}
	return nil, last
}

func (v *VAPID) control(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil || blocked(ap.Addr(), v.loopback) {
		return ErrBlocked
	}
	return nil
}

// CheckEndpoint applies the endpoint policy to raw: an absolute https URL on
// port 443 (or loopback http/https on any port when allowLoopback), no
// userinfo, no fragment, at most MaxEndpointLen bytes, and a host that is a
// name or a public IP address.
func CheckEndpoint(raw string, allowLoopback bool) error {
	if raw == "" || len(raw) > MaxEndpointLen || strings.ContainsAny(raw, " \t\r\n\\") {
		return ErrEndpoint
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Fragment != "" || u.Opaque != "" || u.Host == "" {
		return ErrEndpoint
	}
	host := u.Hostname()
	loop := allowLoopback && isLoopbackHost(host)
	switch {
	case loop && (u.Scheme == "http" || u.Scheme == "https"):
		return nil
	case u.Scheme != "https":
		return ErrEndpoint
	case u.Port() != "" && u.Port() != "443":
		return ErrEndpoint
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if blocked(ip, false) {
			return ErrEndpoint
		}
		return nil
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || !strings.Contains(host, ".") || numericLast(host) {
		return ErrEndpoint
	}
	return nil
}

func isLoopbackHost(h string) bool {
	if h == "localhost" {
		return true
	}
	ip, err := netip.ParseAddr(h)
	return err == nil && ip.Unmap().IsLoopback()
}

// numericLast reports hosts a resolver may read as an IPv4 address in a
// non-dotted-quad form (the last label is all digits or 0x…).
func numericLast(host string) bool {
	host = strings.TrimSuffix(host, ".")
	last := host[strings.LastIndexByte(host, '.')+1:]
	if last == "" || strings.HasPrefix(last, "0x") {
		return true
	}
	for _, r := range last {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// CheckKeys checks a subscription's keys: p256dh an uncompressed point on
// P-256, auth 16 bytes (base64url, padded or not).
func CheckKeys(p256dh, auth string) error {
	pk, err1 := decode(p256dh)
	ak, err2 := decode(auth)
	if err1 != nil || err2 != nil || len(pk) != 65 || len(ak) != 16 {
		return ErrSubKeys
	}
	if _, err := ecdh.P256().NewPublicKey(pk); err != nil {
		return ErrSubKeys
	}
	return nil
}

// DecodeKey decodes a base64url key (padded or not).
func DecodeKey(s string) ([]byte, error) { return decode(s) }

func decode(s string) ([]byte, error) {
	s = strings.TrimRight(strings.TrimSpace(s), "=")
	if s == "" || len(s) > 256 {
		return nil, ErrSubKeys
	}
	return base64.RawURLEncoding.DecodeString(s)
}

// Deny list (plan §4.2 safefetch), checked on the address and on its Unmap().
var denied = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12",
		"192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24",
		"203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4", "255.255.255.255/32",
		"::/128", "::1/128", "::/96", "::ffff:0:0/96", "64:ff9b::/96", "64:ff9b:1::/48", "100::/64",
		"2001::/32", "2001:db8::/32", "2002::/16", "fc00::/7", "fe80::/10", "fec0::/10", "ff00::/8",
	} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

func blocked(a netip.Addr, allowLoopback bool) bool {
	if !a.IsValid() || a.Zone() != "" {
		return true
	}
	u := a.Unmap()
	if allowLoopback && u.IsLoopback() {
		return false
	}
	if a.Is4In6() {
		return true
	}
	if u.IsLoopback() || u.IsPrivate() || u.IsLinkLocalUnicast() || u.IsLinkLocalMulticast() ||
		u.IsInterfaceLocalMulticast() || u.IsMulticast() || u.IsUnspecified() {
		return true
	}
	for _, p := range denied {
		if p.Contains(a) || p.Contains(u) {
			return true
		}
	}
	return false
}

// GenerateKeys returns a fresh VAPID key pair (base64url public point, private
// scalar), for `pcc` operators and tests.
func GenerateKeys() (public, private string, err error) {
	private, public, err = wp.GenerateVAPIDKeys()
	return public, private, err
}

// Sent is one message recorded by the log driver.
type Sent struct {
	Sub     Subscription
	Payload []byte
	TTL     time.Duration
}

// Log records messages instead of sending them. Endpoints listed with Gone
// answer gone; Fail makes every send fail.
type Log struct {
	mu   sync.Mutex
	ring []Sent
	max  int
	gone map[string]bool
	fail bool
}

// NewLog returns a log sender keeping the last max messages.
func NewLog(max int) *Log { return &Log{max: max, gone: map[string]bool{}} }

// Send records the message.
func (l *Log) Send(_ context.Context, s Subscription, payload []byte, ttl time.Duration) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.gone[s.Endpoint] {
		return true, nil
	}
	if l.fail {
		return false, ErrRejected
	}
	l.ring = append(l.ring, Sent{Sub: s, Payload: append([]byte(nil), payload...), TTL: ttl})
	if len(l.ring) > l.max {
		l.ring = l.ring[len(l.ring)-l.max:]
	}
	return false, nil
}

// Gone makes endpoint answer gone from now on.
func (l *Log) Gone(endpoint string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.gone[endpoint] = true
}

// Fail makes every later send fail (on) or succeed again (off).
func (l *Log) Fail(on bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fail = on
}

// Messages returns a copy of the recorded messages.
func (l *Log) Messages() []Sent {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Sent(nil), l.ring...)
}

// Reset forgets every recorded message.
func (l *Log) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ring = nil
}
