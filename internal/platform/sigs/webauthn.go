package sigs

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"

	"github.com/go-webauthn/webauthn/protocol/webauthncose"
)

// Errors of offline assertion verification (Phase 1 co-signing).
var (
	ErrAssertion        = errors.New("sigs: malformed passkey assertion")
	ErrAssertionType    = errors.New("sigs: assertion is not a webauthn.get")
	ErrAssertionChal    = errors.New("sigs: assertion signs another challenge")
	ErrAssertionOrigin  = errors.New("sigs: assertion origin mismatch")
	ErrAssertionFlags   = errors.New("sigs: assertion lacks user presence or verification")
	ErrAssertionBadSig  = errors.New("sigs: assertion signature does not verify")
	ErrAssertionBadCOSE = errors.New("sigs: credential public key is not a COSE key")
)

// Authenticator data flags (WebAuthn §6.1).
const (
	flagUP = 0x01
	flagUV = 0x04
)

// AssertionCheck is what an offline verifier knows about an assertion: the
// credential's COSE public key (from its PasskeyChange record), the challenge
// the assertion must sign (Challenge over the record) and, when known, the
// exact first-party origin.
type AssertionCheck struct {
	PublicKey []byte // COSE_Key
	Challenge [32]byte
	Origin    string // "" = not checked (the verifier does not know it)
}

// VerifyAssertion checks a WebAuthn assertion offline (ARCHITECTURE §3 Phase 1,
// security #5): clientDataJSON is a webauthn.get over exactly c.Challenge, not
// cross-origin and without topOrigin, from c.Origin when given; the
// authenticator data has UP and UV; and the signature over
// authenticatorData ‖ sha256(clientDataJSON) verifies under c.PublicKey. The
// rpIdHash, sign counter and credential ownership are the online verifier's
// (gate/auth) job.
func VerifyAssertion(c AssertionCheck, authenticatorData, clientDataJSON, signature []byte) error {
	var cd struct {
		Type        string `json:"type"`
		Challenge   string `json:"challenge"`
		Origin      string `json:"origin"`
		CrossOrigin *bool  `json:"crossOrigin"`
		TopOrigin   string `json:"topOrigin"`
	}
	if err := json.Unmarshal(clientDataJSON, &cd); err != nil {
		return ErrAssertion
	}
	if cd.Type != "webauthn.get" {
		return ErrAssertionType
	}
	got, err := base64.RawURLEncoding.DecodeString(cd.Challenge)
	if err != nil || subtle.ConstantTimeCompare(got, c.Challenge[:]) != 1 {
		return ErrAssertionChal
	}
	if (cd.CrossOrigin != nil && *cd.CrossOrigin) || cd.TopOrigin != "" || (c.Origin != "" && cd.Origin != c.Origin) {
		return ErrAssertionOrigin
	}
	// rpIdHash (32) ‖ flags (1) ‖ signCount (4)
	if len(authenticatorData) < 37 {
		return ErrAssertion
	}
	if flags := authenticatorData[32]; flags&flagUP == 0 || flags&flagUV == 0 {
		return ErrAssertionFlags
	}
	key, err := webauthncose.ParsePublicKey(c.PublicKey)
	if err != nil {
		return ErrAssertionBadCOSE
	}
	h := sha256.Sum256(clientDataJSON)
	msg := make([]byte, 0, len(authenticatorData)+len(h))
	msg = append(append(msg, authenticatorData...), h[:]...)
	if ok, err := webauthncose.VerifySignature(key, msg, signature); err != nil || !ok {
		return ErrAssertionBadSig
	}
	return nil
}

// AssertionChallenge returns the base64url challenge inside clientDataJSON
// (nil when it is not readable).
func AssertionChallenge(clientDataJSON []byte) []byte {
	var cd struct {
		Challenge string `json:"challenge"`
	}
	if json.Unmarshal(clientDataJSON, &cd) != nil {
		return nil
	}
	b, err := base64.RawURLEncoding.DecodeString(cd.Challenge)
	if err != nil {
		return nil
	}
	return b
}
