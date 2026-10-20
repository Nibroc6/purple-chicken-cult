package dbsc_test

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Nibroc6/purple-chicken-cult/internal/platform/dbsc"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/dbsc/dbsctest"
)

const (
	origin = "https://pcc.example"
	reg    = origin + dbsc.RegisterPath
	ref    = origin + dbsc.RefreshPath
)

var challenge = []byte("0123456789abcdef0123456789abcdef")

func TestRegistrationES256AndRS256(t *testing.T) {
	for _, dev := range []*dbsctest.Device{dbsctest.NewES256(), dbsctest.NewRS256()} {
		jwk, err := dbsc.ParseRegistrationJWT(dev.Registration(challenge, reg), challenge, reg)
		if err != nil {
			t.Fatalf("register: %v", err)
		}
		if jwk.Alg != "ES256" && jwk.Alg != "RS256" {
			t.Fatalf("alg %q", jwk.Alg)
		}
		next := []byte("fedcba9876543210fedcba9876543210")
		if err := dbsc.VerifyRefreshJWT(jwk, dev.Refresh(next, ref, "sid"), next, ref, "sid"); err != nil {
			t.Fatalf("refresh %s: %v", jwk.Alg, err)
		}
	}
}

func TestRegistrationRejects(t *testing.T) {
	dev := dbsctest.NewES256()
	other := dbsctest.NewES256()
	good := dev.Registration(challenge, reg)
	parts := strings.Split(string(good), ".")
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	sig[3] ^= 1
	tampered := parts[0] + "." + parts[1] + "." + base64.RawURLEncoding.EncodeToString(sig)
	// A JWT carrying another key than the one that signed it.
	swapped := dev.Sign(map[string]any{"aud": reg, "jti": base64.RawURLEncoding.EncodeToString(challenge), "key": other.JWK()})
	none := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)) + "." + parts[1] + "."
	cases := map[string][]byte{
		"wrong aud":       dev.Registration(challenge, origin+"/elsewhere"),
		"refresh aud":     dev.Registration(challenge, ref),
		"wrong challenge": dev.Registration([]byte("another challenge of 32 bytes!!!"), reg),
		"tampered":        []byte(tampered),
		"swapped key":     swapped,
		"alg none":        []byte(none),
		"no key":          dev.Sign(map[string]any{"aud": reg, "jti": base64.RawURLEncoding.EncodeToString(challenge)}),
		"garbage":         []byte("not.a.jwt"),
		"empty":           nil,
		"huge":            []byte(strings.Repeat("a", dbsc.MaxJWT+1)),
	}
	for name, b := range cases {
		if _, err := dbsc.ParseRegistrationJWT(b, challenge, reg); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := dbsc.ParseRegistrationJWT(good, nil, reg); err == nil {
		t.Error("empty challenge accepted")
	}
}

func TestRefreshRejects(t *testing.T) {
	dev := dbsctest.NewES256()
	jwk, err := dbsc.ParseRegistrationJWT(dev.Registration(challenge, reg), challenge, reg)
	if err != nil {
		t.Fatal(err)
	}
	thief := dbsctest.NewES256()
	rsaDev := dbsctest.NewRS256()
	next := []byte("fedcba9876543210fedcba9876543210")
	cases := map[string][]byte{
		"other device":      thief.Refresh(next, ref, "sid"),
		"rsa signer":        rsaDev.Refresh(next, ref, "sid"),
		"registration aud":  dev.Refresh(next, reg, "sid"),
		"replayed old jti":  dev.Refresh(challenge, ref, "sid"),
		"other session sub": dev.Refresh(next, ref, "other"),
	}
	for name, b := range cases {
		if err := dbsc.VerifyRefreshJWT(jwk, b, next, ref, "sid"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestWeakRSAKeyRefused(t *testing.T) {
	k, err := rsa.GenerateKey(rand.Reader, 1024) //nolint:gosec // the point of the test
	if err != nil {
		t.Fatal(err)
	}
	jwk := dbsc.PublicJWK{Kty: "RSA", N: base64.RawURLEncoding.EncodeToString(k.N.Bytes()),
		E: base64.RawURLEncoding.EncodeToString(big.NewInt(int64(k.E)).Bytes())}
	if _, err := dbsc.PublicKey(jwk); err == nil {
		t.Fatal("1024-bit RSA accepted")
	}
	if _, err := dbsc.PublicKey(dbsc.PublicJWK{Kty: "EC", Crv: "P-384", X: "AA", Y: "AA"}); err == nil {
		t.Fatal("P-384 accepted")
	}
	if _, err := dbsc.PublicKey(dbsc.PublicJWK{Kty: "oct"}); err == nil {
		t.Fatal("symmetric key accepted")
	}
}

func TestHeadersConfigAndCookies(t *testing.T) {
	h := dbsc.RegistrationHeader(challenge)
	if !strings.HasPrefix(h, "(ES256 RS256);path=\"/api/auth/dbsc/register\";challenge=\"") {
		t.Fatalf("registration header %q", h)
	}
	if c := dbsc.ChallengeHeader(challenge, "sid"); !strings.HasSuffix(c, `;id="sid"`) {
		t.Fatalf("challenge header %q", c)
	}
	raw, _ := json.Marshal(dbsc.SessionConfig(origin, "sid", true))
	for _, want := range []string{`"session_identifier":"sid"`, `"refresh_url":"/api/auth/dbsc/refresh"`, `"name":"__Host-pcc-b"`, "Secure"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("config %s lacks %s", raw, want)
		}
	}
	w := httptest.NewRecorder()
	dbsc.SetBoundCookie(w, "tok", true)
	c := w.Header().Get("Set-Cookie")
	for _, want := range []string{"__Host-pcc-b=tok", "Max-Age=600", "HttpOnly", "Secure", "SameSite=Strict", "Path=/"} {
		if !strings.Contains(c, want) {
			t.Fatalf("cookie %q lacks %s", c, want)
		}
	}
	w = httptest.NewRecorder()
	dbsc.SetBoundCookie(w, "tok", false)
	if c := w.Header().Get("Set-Cookie"); !strings.HasPrefix(c, "pcc_dev_b=tok") || strings.Contains(c, "Secure") {
		t.Fatalf("dev cookie %q", c)
	}
}
