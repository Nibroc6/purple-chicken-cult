// Package dbsctest is the local dev driver of Device Bound Session Credentials:
// a software device key that answers registration and refresh challenges the
// way a browser with a TPM-backed key does (Go tests; the e2e spec has a
// TypeScript twin). ES256 and RS256.
//
// Owner: P1-W07.
package dbsctest

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/platform/dbsc"
)

// Device is a software device key.
type Device struct {
	alg string
	ec  *ecdsa.PrivateKey
	rsa *rsa.PrivateKey
}

// NewES256 returns a device with a P-256 key.
func NewES256() *Device {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	return &Device{alg: "ES256", ec: k}
}

// NewRS256 returns a device with a 2048-bit RSA key.
func NewRS256() *Device {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return &Device{alg: "RS256", rsa: k}
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// JWK is the device's public key.
func (d *Device) JWK() dbsc.PublicJWK {
	if d.ec != nil {
		raw, err := d.ec.PublicKey.Bytes()
		if err != nil {
			panic(err)
		}
		return dbsc.PublicJWK{Kty: "EC", Crv: "P-256", X: b64(raw[1:33]), Y: b64(raw[33:])}
	}
	return dbsc.PublicJWK{Kty: "RSA", N: b64(d.rsa.N.Bytes()), E: b64(big.NewInt(int64(d.rsa.E)).Bytes())}
}

// Sign signs payload as a compact dbsc+jwt.
func (d *Device) Sign(payload map[string]any) []byte {
	h, _ := json.Marshal(map[string]string{"alg": d.alg, "typ": "dbsc+jwt"})
	p, _ := json.Marshal(payload)
	input := b64(h) + "." + b64(p)
	sum := sha256.Sum256([]byte(input))
	var sig []byte
	if d.ec != nil {
		r, s, err := ecdsa.Sign(rand.Reader, d.ec, sum[:])
		if err != nil {
			panic(err)
		}
		sig = make([]byte, 64)
		r.FillBytes(sig[:32])
		s.FillBytes(sig[32:])
	} else {
		var err error
		sig, err = rsa.SignPKCS1v15(rand.Reader, d.rsa, crypto.SHA256, sum[:])
		if err != nil {
			panic(err)
		}
	}
	return []byte(input + "." + b64(sig))
}

// Registration answers a registration challenge for the registration URL aud.
func (d *Device) Registration(challenge []byte, aud string) []byte {
	return d.Sign(map[string]any{"aud": aud, "jti": b64(challenge), "iat": time.Now().Unix(), "key": d.JWK()})
}

// Refresh answers a refresh challenge of session sid for the refresh URL aud.
func (d *Device) Refresh(challenge []byte, aud, sid string) []byte {
	return d.Sign(map[string]any{"aud": aud, "jti": b64(challenge), "iat": time.Now().Unix(), "sub": sid})
}
