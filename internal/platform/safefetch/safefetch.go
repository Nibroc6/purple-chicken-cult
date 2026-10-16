// Package safefetch is the SSRF-guarded fetcher for link previews (ARCHITECTURE
// §10.1.6, plan §4.2):
//
//   - http/https on ports 80/443 only; URLs with userinfo are refused;
//   - never a proxy (Transport.Proxy = nil, so HTTP(S)_PROXY is ignored), no
//     cookie jar, a fixed User-Agent "pcc-preview/1";
//   - every resolved address is checked (after Unmap) against the deny list
//     below, the connection is made to the checked IP only, and the dialer's
//     Control hook checks the socket address again (every dial, including
//     Happy Eyeballs fallbacks), so DNS rebinding cannot reach a private
//     address; one private answer blocks the whole host;
//   - numeric host forms other than dotted quads ("2130706433", "0x7f.1",
//     "0177.0.0.1", "127.1") and well-known metadata / internal names are
//     refused before any lookup;
//   - at most 3 redirects, each checked from scratch;
//   - a 3 s deadline for the whole fetch, 16 KiB of response headers, a body
//     cap measured after gzip decompression (GetHTML keeps the first 512 KiB,
//     GetImage refuses anything larger than its max), text/html only for
//     GetHTML.
//
// deploy/runbooks/egress.md adds an egress firewall for the service user as
// defense in depth.
//
// Owner: P0-W09 → P1-W11.
package safefetch

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/platform/config"
)

// Limits.
const (
	Timeout        = 3 * time.Second
	MaxHTML        = 512 << 10
	MaxHeaderBytes = 16 << 10
	MaxRedirects   = 3
	UserAgent      = "pcc-preview/1"
)

// Response is a fetched document.
type Response struct {
	FinalURL    *url.URL
	ContentType string // media type without parameters
	Charset     string // the charset parameter, if any
	Body        []byte
	Truncated   bool // GetHTML: the decoded body was longer than MaxHTML
}

// Errors.
var (
	ErrBlocked   = errors.New("safefetch: blocked")
	ErrTooLarge  = errors.New("safefetch: too large")
	ErrType      = errors.New("safefetch: content type")
	ErrRedirects = errors.New("safefetch: too many redirects")
	ErrStatus    = errors.New("safefetch: status")
)

// Client fetches untrusted URLs.
type Client struct {
	allowLoopback bool // tests and PCC_ENV=test with test hooks only: 127/8 and ::1 on any port
	timeout       time.Duration
	resolve       func(ctx context.Context, host string) ([]netip.Addr, error)
	dial          func(ctx context.Context, network, addr string) (net.Conn, error) // nil: net.Dialer with checkSocket
	hc            *http.Client
}

// New returns the production client.
func New() *Client { return newClient(false) }

// ForConfig returns New(), except under PCC_ENV=test with test hooks on (never
// in prod: config.Validate refuses test hooks outside Env=test), where loopback
// addresses on any port are also allowed so end-to-end tests can serve pages
// from 127.0.0.1. Every other rule still applies.
func ForConfig(cfg *config.Config) *Client {
	if cfg != nil && cfg.Env == config.EnvTest && cfg.TestHooks {
		return newClient(true)
	}
	return New()
}

func newForTest(allowLoopback bool) *Client { return newClient(allowLoopback) }

func newClient(allowLoopback bool) *Client {
	c := &Client{allowLoopback: allowLoopback, timeout: Timeout, resolve: lookup}
	tr := &http.Transport{
		Proxy:                  nil, // never HTTP(S)_PROXY
		DialContext:            c.dialContext,
		ForceAttemptHTTP2:      true,
		TLSHandshakeTimeout:    Timeout,
		ResponseHeaderTimeout:  Timeout,
		MaxResponseHeaderBytes: MaxHeaderBytes,
		DisableCompression:     true, // gzip is decoded here, under the size cap
		DisableKeepAlives:      true,
	}
	c.hc = &http.Client{
		Transport:     tr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return c
}

func lookup(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

// GetHTML fetches an HTML page; the body holds at most MaxHTML decoded bytes.
func (c *Client) GetHTML(ctx context.Context, u *url.URL) (*Response, error) {
	return c.get(ctx, u, "text/html", MaxHTML, false)
}

// GetImage fetches an image of at most max decoded bytes (P1 preview images).
func (c *Client) GetImage(ctx context.Context, u *url.URL, max int64) (*Response, error) {
	return c.get(ctx, u, "image/", max, true)
}

func (c *Client) get(ctx context.Context, u *url.URL, want string, max int64, strict bool) (*Response, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	cur := u
	for hop := 0; ; hop++ {
		if err := c.checkURL(cur); err != nil {
			return nil, err
		}
		resp, err := c.do(ctx, cur, want)
		if err != nil {
			return nil, err
		}
		if loc := resp.Header.Get("Location"); isRedirect(resp.StatusCode) && loc != "" {
			_ = resp.Body.Close()
			if hop >= MaxRedirects {
				return nil, ErrRedirects
			}
			next, err := cur.Parse(loc)
			if err != nil {
				return nil, ErrBlocked
			}
			cur = next
			continue
		}
		defer func() { _ = resp.Body.Close() }()
		return c.read(resp, cur, want, max, strict)
	}
}

func (c *Client) do(ctx context.Context, u *url.URL, want string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, ErrBlocked
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept-Encoding", "gzip")
	if want == "text/html" {
		req.Header.Set("Accept", "text/html")
	} else {
		req.Header.Set("Accept", "image/*")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		if errors.Is(err, ErrBlocked) {
			return nil, ErrBlocked
		}
		return nil, err
	}
	return resp, nil
}

func (c *Client) read(resp *http.Response, u *url.URL, want string, max int64, strict bool) (*Response, error) {
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %d", ErrStatus, resp.StatusCode)
	}
	mt, params, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || (want == "text/html" && mt != want) || (want != "text/html" && !strings.HasPrefix(mt, want)) {
		return nil, ErrType
	}
	var body io.Reader = resp.Body
	switch enc := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding"))); enc {
	case "", "identity":
	case "gzip", "x-gzip":
		zr, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, ErrType
		}
		defer func() { _ = zr.Close() }()
		body = zr
	default:
		return nil, ErrType
	}
	// The cap applies to the decoded stream: a small gzip bomb stops here.
	b, err := io.ReadAll(io.LimitReader(body, max+1))
	if err != nil {
		return nil, err
	}
	out := &Response{FinalURL: u, ContentType: mt, Charset: params["charset"]}
	if int64(len(b)) > max {
		if strict {
			return nil, ErrTooLarge
		}
		b, out.Truncated = b[:max], true
	}
	out.Body = b
	return out, nil
}

func isRedirect(code int) bool {
	switch code {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect,
		http.StatusPermanentRedirect:
		return true
	}
	return false
}

// checkURL applies the URL rules before any lookup.
func (c *Client) checkURL(u *url.URL) error {
	if u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Opaque != "" || u.User != nil {
		return ErrBlocked
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return ErrBlocked
	}
	if err := c.checkPort(u.Scheme, u.Port(), host); err != nil {
		return err
	}
	if a, err := netip.ParseAddr(host); err == nil {
		if c.blocked(a) {
			return ErrBlocked
		}
		return nil
	}
	if numericHost(host) || blockedName(host) {
		return ErrBlocked
	}
	return nil
}

func (c *Client) checkPort(scheme, port, host string) error {
	if port == "" {
		return nil
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return ErrBlocked
	}
	if (scheme == "http" && n == 80) || (scheme == "https" && n == 443) {
		return nil
	}
	if c.allowLoopback {
		if a, err := netip.ParseAddr(host); err == nil && a.Unmap().IsLoopback() {
			return nil
		}
	}
	return ErrBlocked
}

// dialContext resolves the host, checks every address and dials only checked
// IPs; the socket address is checked once more in Control.
func (c *Client) dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, ErrBlocked
	}
	var addrs []netip.Addr
	if a, err := netip.ParseAddr(host); err == nil {
		addrs = []netip.Addr{a}
	} else {
		if numericHost(host) || blockedName(host) {
			return nil, ErrBlocked
		}
		if addrs, err = c.resolve(ctx, host); err != nil {
			return nil, err
		}
	}
	if len(addrs) == 0 {
		return nil, ErrBlocked
	}
	for _, a := range addrs {
		if c.blocked(a) {
			return nil, ErrBlocked
		}
	}
	var last error
	for _, a := range addrs {
		target := net.JoinHostPort(a.Unmap().String(), port)
		var conn net.Conn
		if c.dial != nil {
			conn, err = c.dial(ctx, "tcp", target)
		} else {
			d := net.Dialer{Timeout: c.timeout, Control: c.checkSocket}
			conn, err = d.DialContext(ctx, "tcp", target)
		}
		if err == nil {
			return conn, nil
		}
		if errors.Is(err, ErrBlocked) {
			return nil, ErrBlocked
		}
		last = err
	}
	return nil, last
}

// checkSocket is the dialer's Control hook: the address actually connected.
func (c *Client) checkSocket(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil || c.blocked(ap.Addr()) {
		return ErrBlocked
	}
	return nil
}

// Deny list (plan §4.2), checked on the address and on its Unmap().
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

func (c *Client) blocked(a netip.Addr) bool {
	if !a.IsValid() || a.Zone() != "" {
		return true
	}
	u := a.Unmap()
	if c.allowLoopback && u.IsLoopback() {
		return false
	}
	if a.Is4In6() {
		return true // an AAAA answer in ::ffff:0:0/96 is never a public IPv6 address
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

// numericHost reports hosts that resolvers may read as an IPv4 address in a
// non-dotted-quad form (inet_aton: decimal, octal, hex, fewer than four parts):
// their last label is all digits or a 0x hex number. Real TLDs never are.
func numericHost(host string) bool {
	host = strings.TrimSuffix(host, ".")
	last := host[strings.LastIndexByte(host, '.')+1:]
	if last == "" {
		return true
	}
	if strings.HasPrefix(last, "0x") {
		return true
	}
	for _, r := range last {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// blockedName: loopback and cloud-metadata / internal names, refused by name.
func blockedName(host string) bool {
	host = strings.TrimSuffix(host, ".")
	switch host {
	case "localhost", "metadata", "metadata.google.internal", "instance-data", "instance-data.ec2.internal",
		"metadata.azure.com", "metadata.tencentyun.com", "100-100-100-200.alibaba":
		return true
	}
	for _, suf := range []string{".localhost", ".internal", ".local", ".localdomain", ".home.arpa", ".lan"} {
		if strings.HasSuffix(host, suf) {
			return true
		}
	}
	return !strings.Contains(host, ".") // single-label names resolve through search domains
}
