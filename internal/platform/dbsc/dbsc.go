// Package dbsc implements Device Bound Session Credentials protocol helpers
// (ARCHITECTURE §17.2(2)): the Sec-Session-Registration header, registration JWT
// parsing and refresh JWT verification (aud = refresh URL, jti = single-use
// challenge), for ES256 and RS256 keys (many Windows TPMs are RSA-only).
//
// Owner: P1-W07. SCAFFOLD STUB: parsing/verification fail closed.
package dbsc

import (
	"encoding/base64"
	"errors"
)

// PublicJWK is the device public key from the registration JWT.
type PublicJWK struct {
	Kty string `json:"kty"`
	Alg string `json:"alg"`
	Crv string `json:"crv,omitempty"`
	X   string `json:"x,omitempty"`
	Y   string `json:"y,omitempty"`
	N   string `json:"n,omitempty"`
	E   string `json:"e,omitempty"`
}

// ErrNotImplemented fails closed until P1-W07 lands.
var ErrNotImplemented = errors.New("dbsc: not implemented")

// RegistrationHeader returns the Sec-Session-Registration header value.
func RegistrationHeader(challenge []byte) string {
	return `(ES256 RS256);path="/api/auth/dbsc/register";challenge="` + base64.RawURLEncoding.EncodeToString(challenge) + `"`
}

// ParseRegistrationJWT verifies a registration JWT against challenge.
func ParseRegistrationJWT(b []byte, challenge []byte) (PublicJWK, error) {
	return PublicJWK{}, ErrNotImplemented
}

// VerifyRefreshJWT verifies a refresh JWT signed by jwk.
func VerifyRefreshJWT(jwk PublicJWK, b []byte, challenge []byte, aud string) error {
	return ErrNotImplemented
}
