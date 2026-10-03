// Package authtest is a software WebAuthn authenticator for Go tests: ES256
// keys, "none" attestation, discoverable credentials, user verification. It
// consumes the options JSON the server sends ({"publicKey": {...}}) and returns
// the PublicKeyCredential JSON a browser would POST, so tests drive the real
// ceremonies end to end. Knobs let tests produce hostile responses (wrong
// origin, cross-origin, no UV, replayed counters).
//
// Owner: P0-W01. Never imported by production code.
package authtest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"sync"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
)

// Errors.
var (
	ErrNoCredential = errors.New("authtest: no matching credential")
	ErrExcluded     = errors.New("authtest: a credential in excludeCredentials is present")
)

// Credential is one stored key.
type Credential struct {
	ID         []byte
	RPID       string
	UserHandle []byte
	Key        *ecdsa.PrivateKey
	Counter    uint32
}

// Authenticator is a software authenticator bound to one origin.
type Authenticator struct {
	Origin string

	// Knobs (zero values produce a well-formed, verified response).
	NoUV           bool   // clear the UV flag
	NoUP           bool   // clear the UP flag
	CrossOrigin    bool   // clientData.crossOrigin = true
	TopOrigin      string // clientData.topOrigin
	OriginOverride string // clientData.origin
	BackupEligible bool   // BE flag on every response
	FrozenCounter  bool   // never increment the signature counter
	StartCounter   uint32 // initial counter of new credentials

	mu    sync.Mutex
	creds []*Credential
}

// New returns an authenticator for origin.
func New(origin string) *Authenticator { return &Authenticator{Origin: origin} }

// Credentials returns the stored credentials (oldest first).
func (a *Authenticator) Credentials() []*Credential {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]*Credential(nil), a.creds...)
}

// Import adds a credential created by another authenticator (a stolen or
// shared key, for negative tests).
func (a *Authenticator) Import(c *Credential) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.creds = append(a.creds, c)
}

// Forget drops every stored credential.
func (a *Authenticator) Forget() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.creds = nil
}

type b64 []byte

func (b *b64) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	v, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		v, err = base64.URLEncoding.DecodeString(s)
	}
	*b = v
	return err
}

type descriptor struct {
	ID b64 `json:"id"`
}

type creationOptions struct {
	Challenge b64 `json:"challenge"`
	RP        struct {
		ID string `json:"id"`
	} `json:"rp"`
	User struct {
		ID b64 `json:"id"`
	} `json:"user"`
	Exclude []descriptor `json:"excludeCredentials"`
}

type requestOptions struct {
	Challenge b64          `json:"challenge"`
	RPID      string       `json:"rpId"`
	Allow     []descriptor `json:"allowCredentials"`
}

// unwrap accepts {"publicKey": {...}} or the bare options, or a server answer
// {"options": {"publicKey": {...}}}.
func unwrap(raw []byte) []byte {
	var outer struct {
		PublicKey json.RawMessage `json:"publicKey"`
		Options   json.RawMessage `json:"options"`
	}
	if json.Unmarshal(raw, &outer) == nil {
		if len(outer.Options) > 0 {
			return unwrap(outer.Options)
		}
		if len(outer.PublicKey) > 0 {
			return outer.PublicKey
		}
	}
	return raw
}

func enc(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (a *Authenticator) clientData(typ string, challenge []byte) []byte {
	origin := a.Origin
	if a.OriginOverride != "" {
		origin = a.OriginOverride
	}
	cd := map[string]any{"type": typ, "challenge": enc(challenge), "origin": origin, "crossOrigin": a.CrossOrigin}
	if a.TopOrigin != "" {
		cd["topOrigin"] = a.TopOrigin
	}
	b, _ := json.Marshal(cd)
	return b
}

func (a *Authenticator) flags(at bool) byte {
	var f byte
	if !a.NoUP {
		f |= 0x01
	}
	if !a.NoUV {
		f |= 0x04
	}
	if a.BackupEligible {
		f |= 0x08 | 0x10
	}
	if at {
		f |= 0x40
	}
	return f
}

func authData(rpID string, flags byte, counter uint32) []byte {
	h := sha256.Sum256([]byte(rpID))
	out := append([]byte{}, h[:]...)
	out = append(out, flags)
	return binary.BigEndian.AppendUint32(out, counter)
}

func coseKey(pub *ecdsa.PublicKey) ([]byte, error) {
	b, err := pub.Bytes() // 0x04 ‖ X ‖ Y
	if err != nil {
		return nil, err
	}
	return webauthncbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: b[1:33], -3: b[33:65]})
}

// Create answers navigator.credentials.create with the given options.
func (a *Authenticator) Create(options []byte) (json.RawMessage, error) {
	var o creationOptions
	if err := json.Unmarshal(unwrap(options), &o); err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, ex := range o.Exclude {
		for _, c := range a.creds {
			if string(c.ID) == string(ex.ID) {
				return nil, ErrExcluded
			}
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	id := make([]byte, 32)
	if _, err := rand.Read(id); err != nil {
		return nil, err
	}
	c := &Credential{ID: id, RPID: o.RP.ID, UserHandle: o.User.ID, Key: key, Counter: a.StartCounter}
	cose, err := coseKey(&key.PublicKey)
	if err != nil {
		return nil, err
	}
	ad := authData(o.RP.ID, a.flags(true), c.Counter)
	ad = append(ad, make([]byte, 16)...)                    // AAGUID (zero: no attestation)
	ad = binary.BigEndian.AppendUint16(ad, uint16(len(id))) //nolint:gosec // 32-byte IDs
	ad = append(ad, id...)
	ad = append(ad, cose...)
	att, err := webauthncbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": ad})
	if err != nil {
		return nil, err
	}
	a.creds = append(a.creds, c)
	return json.Marshal(map[string]any{
		"id": enc(id), "rawId": enc(id), "type": "public-key", "authenticatorAttachment": "platform",
		"clientExtensionResults": map[string]any{},
		"response": map[string]any{
			"clientDataJSON":    enc(a.clientData("webauthn.create", o.Challenge)),
			"attestationObject": enc(att),
			"transports":        []string{"internal"},
		},
	})
}

// Get answers navigator.credentials.get: the first stored credential that the
// options allow (any credential for the RP ID when allowCredentials is empty).
func (a *Authenticator) Get(options []byte) (json.RawMessage, error) {
	return a.GetWith(options, nil)
}

// GetWith answers with the credential whose ID is credID (ignoring
// allowCredentials, as a hostile client could), or like Get when credID is nil.
func (a *Authenticator) GetWith(options []byte, credID []byte) (json.RawMessage, error) {
	var o requestOptions
	if err := json.Unmarshal(unwrap(options), &o); err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	var c *Credential
	for _, cand := range a.creds {
		if credID != nil {
			if string(cand.ID) == string(credID) {
				c = cand
			}
			continue
		}
		if cand.RPID != o.RPID {
			continue
		}
		if len(o.Allow) == 0 {
			c = cand
			break
		}
		for _, al := range o.Allow {
			if string(al.ID) == string(cand.ID) {
				c = cand
			}
		}
		if c != nil {
			break
		}
	}
	if c == nil {
		return nil, ErrNoCredential
	}
	if !a.FrozenCounter {
		c.Counter++
	}
	rpID := o.RPID
	if rpID == "" {
		rpID = c.RPID
	}
	ad := authData(rpID, a.flags(false), c.Counter)
	cd := a.clientData("webauthn.get", o.Challenge)
	h := sha256.Sum256(cd)
	digest := sha256.Sum256(append(append([]byte{}, ad...), h[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, c.Key, digest[:])
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"id": enc(c.ID), "rawId": enc(c.ID), "type": "public-key", "authenticatorAttachment": "platform",
		"clientExtensionResults": map[string]any{},
		"response": map[string]any{
			"clientDataJSON":    enc(cd),
			"authenticatorData": enc(ad),
			"signature":         enc(sig),
			"userHandle":        enc(c.UserHandle),
		},
	})
}
