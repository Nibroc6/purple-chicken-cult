package blob

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"net/url"
	"strconv"
	"time"
)

// KeyLen is the length of a blob key: 128 random bits in lowercase base32
// without padding. Keys are never content hashes (§6), so equal images get
// unrelated keys and a key says nothing about the bytes behind it.
const KeyLen = 26

var b32 = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// NewKey returns a fresh random blob key.
func NewKey() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return b32.EncodeToString(b[:]), nil
}

// TagLen is the length of a viewer tag.
const TagLen = 6

// Tag is a viewer tag: HMAC(k, "pcc-media-viewer-v1" ‖ viewer)[:6]. It binds a
// signed URL to the viewer it was issued to without naming them.
type Tag [TagLen]byte

// Signed-URL errors. Both mean 403 at the media origin.
var (
	ErrSignature = errors.New("blob: bad media URL signature")
	ErrExpired   = errors.New("blob: media URL expired")
)

// MaxURLLifetime bounds how far in the future a verified URL may expire (the
// issuer never signs more than an hour ahead; this is slack for clock skew).
const MaxURLLifetime = 2 * time.Hour

// Signer signs and verifies per-viewer media URLs (plan §4.7, security #27):
//
//	URL = <base>/<key>?e=<unix>&v=<base64url(tag)>&s=<base64url(sig)>
//	sig = HMAC-SHA256(k, key (26 B ascii) ‖ e (8 B big-endian) ‖ v (6 B))
//
// The MAC input has a fixed width, so no two (key, e, v) triples share it. The
// Cloudflare Worker (deploy/cloudflare/media-worker.js) checks the same MAC
// before its cache lookup; the dev/test server checks it in Go.
type Signer struct{ k []byte }

// NewSigner returns a signer under key k (HKDF "pcc/media-url/v1").
func NewSigner(k []byte) *Signer { return &Signer{k: append([]byte(nil), k...)} }

// ViewerTag returns viewer's tag.
func (s *Signer) ViewerTag(viewer uint64) Tag {
	m := hmac.New(sha256.New, s.k)
	m.Write([]byte("pcc-media-viewer-v1"))
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], viewer)
	m.Write(b[:])
	var t Tag
	copy(t[:], m.Sum(nil))
	return t
}

func (s *Signer) mac(key string, exp int64, tag Tag) []byte {
	var in [KeyLen + 8 + TagLen]byte
	copy(in[:KeyLen], key)
	binary.BigEndian.PutUint64(in[KeyLen:KeyLen+8], uint64(exp)) //nolint:gosec // exp is a positive unix time
	copy(in[KeyLen+8:], tag[:])
	m := hmac.New(sha256.New, s.k)
	m.Write(in[:])
	return m.Sum(nil)
}

// Query returns the signed query string of key for tag, expiring at exp.
func (s *Signer) Query(key string, exp time.Time, tag Tag) string {
	e := exp.Unix()
	return "e=" + strconv.FormatInt(e, 10) +
		"&v=" + base64.RawURLEncoding.EncodeToString(tag[:]) +
		"&s=" + base64.RawURLEncoding.EncodeToString(s.mac(key, e, tag))
}

// URL returns base/key?<signed query>.
func (s *Signer) URL(base, key string, exp time.Time, tag Tag) string {
	return base + "/" + key + "?" + s.Query(key, exp, tag)
}

// Verify checks a signed query for key at time now and returns the expiry.
// Anything malformed, a repeated or missing parameter, a wrong MAC (another
// key, another viewer tag, another expiry) is ErrSignature; a past expiry is
// ErrExpired. The MAC is checked first, so an expired forgery is a forgery.
func (s *Signer) Verify(key string, q url.Values, now time.Time) (time.Time, error) {
	if !ValidKey(key) || len(key) != KeyLen || len(q) != 3 {
		return time.Time{}, ErrSignature
	}
	one := func(name string) (string, bool) {
		v, ok := q[name]
		return firstOnly(v), ok && len(v) == 1
	}
	es, ok1 := one("e")
	vs, ok2 := one("v")
	ss, ok3 := one("s")
	if !ok1 || !ok2 || !ok3 || len(es) == 0 || len(es) > 12 || len(vs) != 8 || len(ss) != 43 {
		return time.Time{}, ErrSignature
	}
	for _, c := range es {
		if c < '0' || c > '9' {
			return time.Time{}, ErrSignature
		}
	}
	e, err := strconv.ParseInt(es, 10, 64)
	if err != nil || (len(es) > 1 && es[0] == '0') {
		return time.Time{}, ErrSignature
	}
	tb, err1 := base64.RawURLEncoding.Strict().DecodeString(vs)
	sig, err2 := base64.RawURLEncoding.Strict().DecodeString(ss)
	if err1 != nil || err2 != nil || len(tb) != TagLen || len(sig) != sha256.Size {
		return time.Time{}, ErrSignature
	}
	var tag Tag
	copy(tag[:], tb)
	if !hmac.Equal(sig, s.mac(key, e, tag)) {
		return time.Time{}, ErrSignature
	}
	exp := time.Unix(e, 0).UTC()
	if !now.Before(exp) {
		return time.Time{}, ErrExpired
	}
	if exp.Sub(now) > MaxURLLifetime {
		return time.Time{}, ErrSignature
	}
	return exp, nil
}

func firstOnly(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

// PublicURL returns the cache identity of key under base (no query): what the
// edge caches and what a purge names.
func PublicURL(base, key string) string { return base + "/" + url.PathEscape(key) }
