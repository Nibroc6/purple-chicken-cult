package sigs

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
)

func TestVerifyAssertionOffline(t *testing.T) {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pb, _ := k.PublicKey.Bytes()
	cose, err := webauthncbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: pb[1:33], -3: pb[33:65]})
	if err != nil {
		t.Fatal(err)
	}
	chal := Challenge("friendship", []byte(`{"kind":"friendship","nonce":"n","schema_version":1}`))
	const origin = "https://pcc.example"
	type cd struct {
		Type        string `json:"type"`
		Challenge   string `json:"challenge"`
		Origin      string `json:"origin"`
		CrossOrigin bool   `json:"crossOrigin,omitempty"`
		TopOrigin   string `json:"topOrigin,omitempty"`
	}
	build := func(c cd, flags byte) (authData, clientData, sig []byte) {
		rp := sha256.Sum256([]byte("pcc.example"))
		authData = append(append([]byte{}, rp[:]...), flags, 0, 0, 0, 1)
		clientData, _ = json.Marshal(c)
		h := sha256.Sum256(clientData)
		d := sha256.Sum256(append(append([]byte{}, authData...), h[:]...))
		sig, err := ecdsa.SignASN1(rand.Reader, k, d[:])
		if err != nil {
			t.Fatal(err)
		}
		return authData, clientData, sig
	}
	good := cd{Type: "webauthn.get", Challenge: base64.RawURLEncoding.EncodeToString(chal[:]), Origin: origin}
	check := AssertionCheck{PublicKey: cose, Challenge: chal, Origin: origin}
	if a, cdj, s := build(good, flagUP|flagUV); VerifyAssertion(check, a, cdj, s) != nil {
		t.Fatal("good assertion rejected")
	}
	with := func(f func(*cd)) cd {
		c := good
		f(&c)
		return c
	}
	cases := []struct {
		name  string
		c     cd
		flags byte
		want  error
	}{
		{"create, not get", with(func(c *cd) { c.Type = "webauthn.create" }), flagUP | flagUV, ErrAssertionType},
		{"other challenge", with(func(c *cd) { c.Challenge = "AAAA" }), flagUP | flagUV, ErrAssertionChal},
		{"other origin", with(func(c *cd) { c.Origin = "https://evil.example" }), flagUP | flagUV, ErrAssertionOrigin},
		{"cross origin", with(func(c *cd) { c.CrossOrigin = true }), flagUP | flagUV, ErrAssertionOrigin},
		{"top origin", with(func(c *cd) { c.TopOrigin = origin }), flagUP | flagUV, ErrAssertionOrigin},
		{"no UV", good, flagUP, ErrAssertionFlags},
		{"no UP", good, flagUV, ErrAssertionFlags},
	}
	for _, c := range cases {
		a, cdj, s := build(c.c, c.flags)
		if err := VerifyAssertion(check, a, cdj, s); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
	a, cdj, s := build(good, flagUP|flagUV)
	s[len(s)-1] ^= 1
	if err := VerifyAssertion(check, a, cdj, s); !errors.Is(err, ErrAssertionBadSig) {
		t.Errorf("tampered signature: %v", err)
	}
	if err := VerifyAssertion(AssertionCheck{PublicKey: []byte{1, 2}, Challenge: chal}, a, cdj, s); !errors.Is(err, ErrAssertionBadCOSE) {
		t.Errorf("bad key: %v", err)
	}
	if got := AssertionChallenge(cdj); string(got) != string(chal[:]) {
		t.Errorf("challenge %x", got)
	}
}
