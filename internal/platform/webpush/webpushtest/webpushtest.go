// Package webpushtest is a local fake push service for Go tests: an httptest
// server holding the browser side of subscriptions (P-256 key pair and auth
// secret per endpoint) that decrypts every aes128gcm message it receives
// (RFC 8291) and checks its VAPID authorization (RFC 8292). Tests never contact
// a real push service.
//
// Owner: P1-W05.
package webpushtest

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/platform/webpush"
)

// Received is one message as the fake push service saw it.
type Received struct {
	Endpoint  string
	Plaintext []byte // decrypted payload (padding removed)
	BodyLen   int    // encrypted body length on the wire
	TTL       int
	Urgency   string
	Auth      string // Authorization header
	JWT       map[string]any
	VAPIDKey  string // the k= parameter
	DecodeErr error
}

// Message decodes Plaintext as a webpush.Message.
func (r Received) Message() (webpush.Message, error) {
	var m webpush.Message
	err := json.Unmarshal(r.Plaintext, &m)
	return m, err
}

type device struct {
	priv *ecdh.PrivateKey
	auth []byte
}

// Service is the fake push service.
type Service struct {
	t   testing.TB
	srv *httptest.Server

	mu      sync.Mutex
	devices map[string]device
	status  map[string]int
	got     []Received
	seq     int
}

// New starts a fake push service on 127.0.0.1 (closed in t.Cleanup).
func New(t testing.TB) *Service {
	t.Helper()
	s := &Service{t: t, devices: map[string]device{}, status: map[string]int{}}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.srv.Close)
	return s
}

// URL is the service's base URL.
func (s *Service) URL() string { return s.srv.URL }

// Subscribe creates a new browser-side subscription on this service.
func (s *Service) Subscribe() webpush.Subscription {
	s.t.Helper()
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		s.t.Fatal(err)
	}
	auth := make([]byte, 16)
	_, _ = rand.Read(auth)
	s.mu.Lock()
	s.seq++
	ep := s.srv.URL + "/push/" + strconv.Itoa(s.seq)
	s.devices[ep] = device{priv: priv, auth: auth}
	s.mu.Unlock()
	return webpush.Subscription{
		Endpoint: ep,
		P256dh:   base64.RawURLEncoding.EncodeToString(priv.PublicKey().Bytes()),
		Auth:     base64.RawURLEncoding.EncodeToString(auth),
	}
}

// Answer makes the service answer status for endpoint from now on (e.g. 410).
func (s *Service) Answer(endpoint string, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status[endpoint] = status
}

// Received returns every message received so far.
func (s *Service) Received() []Received {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Received(nil), s.got...)
}

// For returns the messages received for endpoint.
func (s *Service) For(endpoint string) []Received {
	var out []Received
	for _, r := range s.Received() {
		if r.Endpoint == endpoint {
			out = append(out, r)
		}
	}
	return out
}

func (s *Service) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	ep := s.srv.URL + r.URL.Path
	s.mu.Lock()
	dev, ok := s.devices[ep]
	status := s.status[ep]
	s.mu.Unlock()
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if status != 0 {
		w.WriteHeader(status)
		return
	}
	rec := Received{Endpoint: ep, BodyLen: len(body), Urgency: r.Header.Get("Urgency"), Auth: r.Header.Get("Authorization")}
	rec.TTL, _ = strconv.Atoi(r.Header.Get("TTL"))
	if r.Method != http.MethodPost || r.Header.Get("Content-Encoding") != "aes128gcm" {
		rec.DecodeErr = errors.New("webpushtest: not an aes128gcm POST")
	} else {
		rec.Plaintext, rec.DecodeErr = Decrypt(dev.priv, dev.auth, body)
	}
	rec.JWT, rec.VAPIDKey = parseVAPID(rec.Auth)
	s.mu.Lock()
	s.got = append(s.got, rec)
	s.mu.Unlock()
	w.WriteHeader(http.StatusCreated)
}

// Decrypt opens one aes128gcm Web Push record (RFC 8291 §3.4, RFC 8188).
func Decrypt(priv *ecdh.PrivateKey, auth, body []byte) ([]byte, error) {
	if len(body) < 21 {
		return nil, errors.New("webpushtest: short body")
	}
	salt, rs, idlen := body[:16], binary.BigEndian.Uint32(body[16:20]), int(body[20])
	if len(body) < 21+idlen || idlen != 65 {
		return nil, errors.New("webpushtest: bad header")
	}
	asPub, err := ecdh.P256().NewPublicKey(body[21 : 21+idlen])
	if err != nil {
		return nil, err
	}
	ct := body[21+idlen:]
	if uint32(len(ct)) > rs {
		return nil, errors.New("webpushtest: more than one record")
	}
	secret, err := priv.ECDH(asPub)
	if err != nil {
		return nil, err
	}
	info := append(append([]byte("WebPush: info\x00"), priv.PublicKey().Bytes()...), asPub.Bytes()...)
	ikm, err := hkdf.Key(sha256.New, secret, auth, string(info), 32)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}
	blk, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(blk)
	if err != nil {
		return nil, err
	}
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, err
	}
	i := len(pt) - 1
	for i >= 0 && pt[i] == 0 {
		i--
	}
	if i < 0 || pt[i] != 2 {
		return nil, errors.New("webpushtest: bad padding delimiter")
	}
	return pt[:i], nil
}

// parseVAPID splits "vapid t=<jwt>, k=<key>" and returns the JWT claims and key.
func parseVAPID(h string) (map[string]any, string) {
	rest, ok := strings.CutPrefix(h, "vapid ")
	if !ok {
		return nil, ""
	}
	var tok, key string
	for _, part := range strings.Split(rest, ",") {
		part = strings.TrimSpace(part)
		if v, ok := strings.CutPrefix(part, "t="); ok {
			tok = v
		}
		if v, ok := strings.CutPrefix(part, "k="); ok {
			key = v
		}
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return nil, key
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, key
	}
	claims := map[string]any{}
	if json.Unmarshal(b, &claims) != nil {
		return nil, key
	}
	return claims, key
}

// VerifyVAPID checks that the Authorization header is an ES256 JWT signed by
// the VAPID public key pub (base64url point) for aud, not expired at now and
// valid for at most 24 h (RFC 8292 §2).
func VerifyVAPID(header, pub, aud string, now time.Time) error {
	rest, ok := strings.CutPrefix(header, "vapid ")
	if !ok {
		return errors.New("webpushtest: not a vapid authorization")
	}
	var tok, key string
	for _, part := range strings.Split(rest, ",") {
		part = strings.TrimSpace(part)
		if v, ok := strings.CutPrefix(part, "t="); ok {
			tok = v
		}
		if v, ok := strings.CutPrefix(part, "k="); ok {
			key = v
		}
	}
	if key != strings.TrimRight(pub, "=") {
		return errors.New("webpushtest: k is not the server key")
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return errors.New("webpushtest: malformed jwt")
	}
	hdr, err1 := base64.RawURLEncoding.DecodeString(parts[0])
	sig, err2 := base64.RawURLEncoding.DecodeString(parts[2])
	pb, err3 := base64.RawURLEncoding.DecodeString(key)
	if err1 != nil || err2 != nil || err3 != nil || len(sig) != 64 || len(pb) != 65 {
		return errors.New("webpushtest: malformed jwt")
	}
	var h struct {
		Alg string `json:"alg"`
	}
	if json.Unmarshal(hdr, &h) != nil || h.Alg != "ES256" {
		return errors.New("webpushtest: alg is not ES256")
	}
	x, y := new(big.Int).SetBytes(pb[1:33]), new(big.Int).SetBytes(pb[33:])
	pk := ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(&pk, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		return errors.New("webpushtest: bad signature")
	}
	claims, _ := parseVAPID(header)
	if claims["aud"] != aud {
		return errors.New("webpushtest: wrong aud")
	}
	exp, _ := claims["exp"].(float64)
	if t := time.Unix(int64(exp), 0); !t.After(now) || t.After(now.Add(24*time.Hour)) {
		return errors.New("webpushtest: exp out of range")
	}
	if sub, _ := claims["sub"].(string); !strings.HasPrefix(sub, "mailto:") && !strings.HasPrefix(sub, "https:") {
		return errors.New("webpushtest: sub is not a contact")
	}
	return nil
}

// Wait polls until n messages arrived for endpoint or d passes.
func (s *Service) Wait(endpoint string, n int, d time.Duration) []Received {
	deadline := time.Now().Add(d)
	for {
		got := s.For(endpoint)
		if len(got) >= n || time.Now().After(deadline) {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
}
