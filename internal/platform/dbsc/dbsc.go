// Package dbsc implements the server side of Device Bound Session Credentials
// (ARCHITECTURE §17.2(2)): the Sec-Session-Registration and
// Sec-Session-Challenge headers, verification of the registration and refresh
// JWTs (proof of possession of the device key, aud = the endpoint URL, jti =
// the single-use challenge), the session configuration document and the
// short-lived bound cookie. ES256 and RS256 keys are accepted (many Windows
// TPMs are RSA-only); nothing else.
//
// A JWT's iat is not checked: the server's single-use challenge already makes
// every proof fresh, and the device clock is not trusted.
//
// Owner: P1-W07.
package dbsc

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"strings"
	"time"
)

// Endpoint paths (relative to the origin).
const (
	RegisterPath = "/api/auth/dbsc/register"
	RefreshPath  = "/api/auth/dbsc/refresh"
)

// Header names of the protocol.
const (
	HeaderRegistration = "Sec-Session-Registration"
	HeaderResponse     = "Sec-Session-Response"
	HeaderChallenge    = "Sec-Session-Challenge"
	HeaderSessionID    = "Sec-Session-Id"
)

// Bound cookie names: "__Host-" requires Secure, so dev over http uses pcc_dev_b.
const (
	BoundCookieProd = "__Host-pcc-b"
	BoundCookieDev  = "pcc_dev_b"
)

// BoundTTL is the lifetime of the bound cookie (plan P1-W07: 10 minutes).
const BoundTTL = 10 * time.Minute

// MaxJWT bounds a JWT accepted from a header.
const MaxJWT = 8 << 10

// ErrInvalid is every verification failure (its detail never leaves the server).
var ErrInvalid = errors.New("dbsc: invalid proof")

// PublicJWK is the device public key from the registration JWT.
type PublicJWK struct {
	Kty string `json:"kty"`
	Alg string `json:"alg,omitempty"`
	Crv string `json:"crv,omitempty"`
	X   string `json:"x,omitempty"`
	Y   string `json:"y,omitempty"`
	N   string `json:"n,omitempty"`
	E   string `json:"e,omitempty"`
}

// BoundCookieName returns the bound cookie name for the transport.
func BoundCookieName(secure bool) string {
	if secure {
		return BoundCookieProd
	}
	return BoundCookieDev
}

// SetBoundCookie sets the bound cookie (HttpOnly; SameSite=Strict; Path=/;
// Secure on https) for BoundTTL.
func SetBoundCookie(w http.ResponseWriter, token string, secure bool) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure is set whenever the origin is https (dev uses http://localhost)
		Name: BoundCookieName(secure), Value: token, Path: "/", MaxAge: int(BoundTTL / time.Second),
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode,
	})
}

// ClearBoundCookie expires the bound cookie.
func ClearBoundCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure is set whenever the origin is https (dev uses http://localhost)
		Name: BoundCookieName(secure), Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode,
	})
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// RegistrationHeader returns the Sec-Session-Registration header value that
// offers a device-bound session for challenge.
func RegistrationHeader(challenge []byte) string {
	return `(ES256 RS256);path="` + RegisterPath + `";challenge="` + b64(challenge) + `"`
}

// ChallengeHeader returns the Sec-Session-Challenge header value of a refresh.
func ChallengeHeader(challenge []byte, sessionID string) string {
	return `"` + b64(challenge) + `";id="` + sessionID + `"`
}

// Config is the session configuration document answered by registration and refresh.
type Config struct {
	SessionID   string       `json:"session_identifier"`
	RefreshURL  string       `json:"refresh_url"`
	Scope       Scope        `json:"scope"`
	Credentials []Credential `json:"credentials"`
}

// Scope is the part of the site a bound session covers.
type Scope struct {
	Origin        string `json:"origin"`
	IncludeSite   bool   `json:"include_site"`
	DeferRequests bool   `json:"defer_requests"`
}

// Credential is the bound cookie the browser keeps fresh.
type Credential struct {
	Type       string `json:"type"`
	Name       string `json:"name"`
	Attributes string `json:"attributes"`
}

// SessionConfig is the configuration of the session sessionID at origin.
func SessionConfig(origin, sessionID string, secure bool) Config {
	attrs := "Path=/; HttpOnly; SameSite=Strict"
	if secure {
		attrs = "Path=/; Secure; HttpOnly; SameSite=Strict"
	}
	return Config{
		SessionID: sessionID, RefreshURL: RefreshPath,
		Scope:       Scope{Origin: origin, DeferRequests: true},
		Credentials: []Credential{{Type: "cookie", Name: BoundCookieName(secure), Attributes: attrs}},
	}
}

// claims are the JWT payload fields this package reads.
type claims struct {
	Aud string     `json:"aud"`
	Jti string     `json:"jti"`
	Sub string     `json:"sub,omitempty"`
	Key *PublicJWK `json:"key,omitempty"`
}

type header struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

// parse splits and decodes a compact JWS. It returns the header, the claims,
// the signing input and the signature.
func parse(b []byte) (header, claims, []byte, []byte, error) {
	var (
		h header
		c claims
	)
	if len(b) == 0 || len(b) > MaxJWT {
		return h, c, nil, nil, ErrInvalid
	}
	parts := strings.Split(strings.TrimSpace(string(b)), ".")
	if len(parts) != 3 {
		return h, c, nil, nil, ErrInvalid
	}
	dec := base64.RawURLEncoding.Strict()
	hb, err1 := dec.DecodeString(parts[0])
	pb, err2 := dec.DecodeString(parts[1])
	sig, err3 := dec.DecodeString(parts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return h, c, nil, nil, ErrInvalid
	}
	if json.Unmarshal(hb, &h) != nil || json.Unmarshal(pb, &c) != nil {
		return h, c, nil, nil, ErrInvalid
	}
	if h.Typ != "" && h.Typ != "dbsc+jwt" && h.Typ != "jwt" && h.Typ != "JWT" {
		return h, c, nil, nil, ErrInvalid
	}
	return h, c, []byte(parts[0] + "." + parts[1]), sig, nil
}

func eq(a, b string) bool { return a != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

// check verifies the common claims: aud is exactly aud and jti the challenge.
func check(c claims, challenge []byte, aud string) error {
	if len(challenge) == 0 || !eq(c.Aud, aud) || !eq(c.Jti, b64(challenge)) {
		return ErrInvalid
	}
	return nil
}

// ParseRegistrationJWT verifies a registration JWT: aud is the registration
// URL aud, jti the challenge, and the JWT is signed by the key it carries
// (claim "key"). It returns that key.
func ParseRegistrationJWT(b []byte, challenge []byte, aud string) (PublicJWK, error) {
	h, c, input, sig, err := parse(b)
	if err != nil || c.Key == nil {
		return PublicJWK{}, ErrInvalid
	}
	if err := check(c, challenge, aud); err != nil {
		return PublicJWK{}, err
	}
	key := *c.Key
	if err := verify(key, h.Alg, input, sig); err != nil {
		return PublicJWK{}, err
	}
	key.Alg = h.Alg
	return key, nil
}

// VerifyRefreshJWT verifies a refresh JWT signed by jwk: aud is the refresh
// URL aud, jti the single-use challenge and, when sessionID is set, sub (if
// present) names that session.
func VerifyRefreshJWT(jwk PublicJWK, b []byte, challenge []byte, aud, sessionID string) error {
	h, c, input, sig, err := parse(b)
	if err != nil {
		return ErrInvalid
	}
	if err := check(c, challenge, aud); err != nil {
		return err
	}
	if c.Sub != "" && sessionID != "" && !eq(c.Sub, sessionID) {
		return ErrInvalid
	}
	if jwk.Alg != "" && h.Alg != jwk.Alg {
		return ErrInvalid
	}
	return verify(jwk, h.Alg, input, sig)
}

// MinRSABits is the smallest RSA modulus accepted.
const MinRSABits = 2048

// PublicKey decodes jwk into an *ecdsa.PublicKey (P-256) or *rsa.PublicKey.
func PublicKey(jwk PublicJWK) (crypto.PublicKey, error) {
	dec := base64.RawURLEncoding.Strict()
	switch jwk.Kty {
	case "EC":
		if jwk.Crv != "P-256" {
			return nil, ErrInvalid
		}
		x, err1 := dec.DecodeString(jwk.X)
		y, err2 := dec.DecodeString(jwk.Y)
		if err1 != nil || err2 != nil || len(x) != 32 || len(y) != 32 {
			return nil, ErrInvalid
		}
		raw := append(append([]byte{4}, x...), y...)
		pk, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), raw)
		if err != nil {
			return nil, ErrInvalid
		}
		return pk, nil
	case "RSA":
		n, err1 := dec.DecodeString(jwk.N)
		e, err2 := dec.DecodeString(jwk.E)
		if err1 != nil || err2 != nil || len(e) == 0 || len(e) > 4 {
			return nil, ErrInvalid
		}
		pk := &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
		if pk.N.BitLen() < MinRSABits || pk.E < 3 || pk.E%2 == 0 {
			return nil, ErrInvalid
		}
		return pk, nil
	}
	return nil, ErrInvalid
}

// verify checks sig over input with jwk under alg (ES256 needs an EC key and a
// raw 64-byte R‖S signature, RS256 an RSA key and PKCS#1 v1.5).
func verify(jwk PublicJWK, alg string, input, sig []byte) error {
	pk, err := PublicKey(jwk)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(input)
	switch k := pk.(type) {
	case *ecdsa.PublicKey:
		if alg != "ES256" || len(sig) != 64 {
			return ErrInvalid
		}
		r, s := new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])
		if !ecdsa.Verify(k, sum[:], r, s) {
			return ErrInvalid
		}
		return nil
	case *rsa.PublicKey:
		if alg != "RS256" {
			return ErrInvalid
		}
		if rsa.VerifyPKCS1v15(k, crypto.SHA256, sum[:], sig) != nil {
			return ErrInvalid
		}
		return nil
	}
	return ErrInvalid
}
