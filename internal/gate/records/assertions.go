package records

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"

	"github.com/go-webauthn/webauthn/protocol/webauthncose"
	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coreref"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/sigs"
)

// Passkey assertions attached to records (ARCHITECTURE §3 Phase 1, security #5).
//
// A record may be co-signed by several people, each over their own intent, so
// the stored body is the plain record and every assertion carries the exact
// draft it signed: the record plus a "cosign" member {nonce, issued_at}.
// Offline, an assertion is valid for a record when
//   - its draft without the member is byte-identical to the record body,
//   - clientDataJSON is type "webauthn.get" and its challenge is
//     sigs.Challenge(kind, draft) — or, for a bundle (one assertion over the
//     drafts of several records, the signup ceremony), core.BundleChallenge of
//     the listed challenges, one of which is sigs.Challenge(kind, draft),
//   - authenticatorData has UP and UV set and is not cross-origin
//     (clientData crossOrigin false, no topOrigin), and
//   - the signature verifies under the credential's COSE public key, taken from
//     the signer's PasskeyChange records in the same export.

// assertionJSON is one entry of signed_records.assertions and of an export
// envelope's "assertions".
type assertionJSON struct {
	CredentialID      sigs.Bytes   `json:"credential_id"`
	AuthenticatorData sigs.Bytes   `json:"authenticator_data"`
	ClientDataJSON    sigs.Bytes   `json:"client_data_json"`
	Signature         sigs.Bytes   `json:"signature"`
	Signer            *uuid.UUID   `json:"signer,omitempty"` // ext_id of the asserting user (P1)
	Draft             sigs.Bytes   `json:"draft,omitempty"`  // P1
	Bundle            []sigs.Bytes `json:"bundle,omitempty"` // P1, signup ceremony
}

// errBinding is an assertion that does not belong to the record it is attached to.
var errBinding = errors.New("records: assertion does not sign this record")

type clientData struct {
	Type        string `json:"type"`
	Challenge   string `json:"challenge"`
	Origin      string `json:"origin"`
	CrossOrigin bool   `json:"crossOrigin"`
	TopOrigin   string `json:"topOrigin"`
}

// bindAssertion checks the offline-verifiable binding of a to the record
// (kind, canon) and returns its client data: draft ↔ record, challenge ↔ draft
// (or bundle), get ceremony, no cross-origin use, UP and UV.
func bindAssertion(kind string, canon []byte, draft []byte, bundle [][]byte, cdJSON, authData []byte) (clientData, error) {
	var cd clientData
	if len(draft) == 0 {
		return cd, errBinding
	}
	if k, _, err := sigs.RecordFields(draft); err != nil || k != kind {
		return cd, errBinding
	}
	rec, _, _, err := coreref.RecordFromDraft(draft)
	if err != nil || !bytes.Equal(rec, canon) {
		return cd, errBinding
	}
	if err := json.Unmarshal(cdJSON, &cd); err != nil {
		return cd, errBinding
	}
	got, err := base64.RawURLEncoding.DecodeString(cd.Challenge)
	if err != nil {
		return cd, errBinding
	}
	own := sigs.Challenge(kind, draft)
	want := own[:]
	if len(bundle) > 0 {
		if !slices.ContainsFunc(bundle, func(c []byte) bool { return bytes.Equal(c, own[:]) }) {
			return cd, errBinding
		}
		b := core.BundleChallenge(bundle)
		want = b[:]
	}
	if !bytes.Equal(got, want) || cd.Type != "webauthn.get" || cd.CrossOrigin || cd.TopOrigin != "" {
		return cd, errBinding
	}
	// rpIdHash(32) ‖ flags(1) ‖ signCount(4): UP (0x01) and UV (0x04) set.
	if len(authData) < 37 || authData[32]&0x01 == 0 || authData[32]&0x04 == 0 {
		return cd, errBinding
	}
	return cd, nil
}

// verifyWebAuthnSig checks sig over authData ‖ sha256(clientDataJSON) under
// the COSE public key.
func verifyWebAuthnSig(coseKey, authData, cdJSON, sig []byte) bool {
	key, err := webauthncose.ParsePublicKey(coseKey)
	if err != nil {
		return false
	}
	h := sha256.Sum256(cdJSON)
	ok, err := webauthncose.VerifySignature(key, slices.Concat(authData, h[:]), sig)
	return err == nil && ok
}

func bundleBytes(b []sigs.Bytes) [][]byte {
	out := make([][]byte, 0, len(b))
	for _, c := range b {
		out = append(out, c)
	}
	return out
}
