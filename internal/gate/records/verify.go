package records

import (
	"archive/zip"
	"bufio"
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/sigs"
)

// Verify failure codes (gate.VerifyFailure.Code).
const (
	FailBadSig           = "bad_sig"
	FailKindMismatch     = "kind_mismatch"
	FailNotCanonical     = "not_canonical"
	FailUnknownKey       = "unknown_key"
	FailKeyWindow        = "key_window"
	FailCommitMismatch   = "commit_mismatch"
	FailBadCosig         = "bad_cosig"         // a user-signed record without a valid server countersignature
	FailBadAssertion     = "bad_assertion"     // an attached passkey assertion does not verify over the record
	FailMissingAssertion = "missing_assertion" // a co-signed draft, or a high-value record after the cutover, without one
	FailUnchainedPasskey = "unchained_passkey" // a passkey add, or an own assertion, not chained to an earlier credential
)

// windowSkew tolerates small differences between a body's own timestamp and
// the moment the record was signed.
const windowSkew = 10 * time.Minute

// MaxExportFile bounds every file read from an export (zip-bomb guard).
const MaxExportFile = 256 << 20

type pubKey struct {
	pub   ed25519.PublicKey
	until time.Time // zero while the key is current
	owner uuid.UUID // user keys only
}

// highValue are the kinds that need a passkey assertion once co-signing is
// mandatory (ARCHITECTURE §3 Phase 1; PasskeyChange is handled by the chain).
var highValue = map[string]bool{
	core.RecInvite.String(): true, core.RecFriendship.String(): true, core.RecRevocation.String(): true,
}

// verifier holds what VerifyExport learned so far: the trusted keys and, per
// account, the credentials its PasskeyChange records added (in file order).
type verifier struct {
	server   map[string]pubKey
	users    map[string]pubKey
	origin   string
	required *time.Time
	exporter uuid.UUID
	creds    map[uuid.UUID]map[string][]byte // account → credential id (b64) → COSE key
}

// VerifyExport verifies an export zip offline. Every records.jsonl line must be
// canonical as stored, its envelope kind and schema_version must match the
// body, the key must be listed in keys.json (server keys, when pinned is
// non-empty, must match the published fingerprint key_id → hex sha256), the
// body time must lie in the key's validity window and the signature must
// verify. A record signed by a user key must also carry a server
// countersignature that verifies the same way.
//
// Passkey assertions (Phase 1): every assertion a record carries must be a
// webauthn.get from keys.json's origin with UP and UV over exactly
// sigs.Challenge(kind, body), signed by the credential key it names, and one of
// them must be by the record's signer; a co-sign draft (a body with a "nonce")
// must carry one. From keys.json's cosign_required_from on, Invite, Friendship
// and Revocation records must carry one too, and every PasskeyChange add after
// an account's first must be co-signed by a credential the account added
// earlier and has not removed (the credential chain). The exporter's own
// assertions must come from credentials of that chain.
//
// posts.jsonl lines must open the body_commit of their Post record. Failure
// lines number the lines of the file the code refers to (records.jsonl;
// posts.jsonl for commit_mismatch). An error means the file is not a readable
// export.
func VerifyExport(r io.ReaderAt, size int64, pinned map[string]string) (gate.VerifyReport, error) {
	rep := gate.VerifyReport{KeysPinned: len(pinned) > 0}
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return rep, fmt.Errorf("records: not an export zip: %w", err)
	}
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		files[f.Name] = f
	}
	keysRaw, err := readZipFile(files, FileKeys)
	if err != nil {
		return rep, err
	}
	var kf KeysFile
	if err := json.Unmarshal(keysRaw, &kf); err != nil {
		return rep, fmt.Errorf("records: keys.json: %w", err)
	}
	v := &verifier{server: map[string]pubKey{}, users: map[string]pubKey{}, origin: kf.Origin,
		required: kf.CosignRequiredFrom, creds: map[uuid.UUID]map[string][]byte{}}
	for _, k := range kf.Server {
		pub, ok := trustedKey(k)
		if !ok || sigs.ServerKeyID(pub) != k.KeyID {
			continue // a key whose listed fingerprint or ID does not match its bytes is not trusted
		}
		if len(pinned) > 0 && !strings.EqualFold(strings.TrimSpace(pinned[k.KeyID]), k.Fingerprint) {
			continue
		}
		v.server[k.KeyID] = pubKey{pub: pub, until: until(k.ValidUntil)}
	}
	for _, k := range kf.Users {
		pub, ok := trustedKey(k.KeyView)
		if !ok || !strings.HasPrefix(k.KeyID, UserKeyPrefix) || k.Owner == uuid.Nil {
			continue
		}
		v.users[k.KeyID] = pubKey{pub: pub, until: until(k.ValidUntil), owner: k.Owner}
	}
	if raw, err := readZipFile(files, FileManifest); err == nil {
		var man struct {
			User struct {
				ID uuid.UUID `json:"id"`
			} `json:"user"`
		}
		if json.Unmarshal(raw, &man) == nil {
			v.exporter = man.User.ID
		}
	}

	recRaw, err := readZipFile(files, FileRecords)
	if err != nil {
		return rep, err
	}
	type postRef struct {
		id      string
		version int64
	}
	commits := map[postRef][]byte{}
	line := 0
	sc := bufio.NewScanner(bytes.NewReader(recRaw))
	sc.Buffer(make([]byte, 0, 64<<10), MaxExportFile)
	for sc.Scan() {
		raw := sc.Bytes()
		line++
		if len(bytes.TrimSpace(raw)) == 0 {
			line--
			continue
		}
		rep.Records++
		env, code := v.line(raw)
		if code != "" {
			rep.Failures = append(rep.Failures, gate.VerifyFailure{Line: line, Code: code})
			continue
		}
		rep.Valid++
		if env.Kind == core.RecPost.String() {
			var pb struct {
				ID         string     `json:"id"`
				Version    int64      `json:"version"`
				BodyCommit sigs.Bytes `json:"body_commit"`
			}
			if json.Unmarshal(env.Body, &pb) == nil && pb.ID != "" {
				commits[postRef{pb.ID, pb.Version}] = pb.BodyCommit
			}
		}
	}
	if err := sc.Err(); err != nil {
		return rep, fmt.Errorf("records: records.jsonl: %w", err)
	}

	if _, ok := files[FilePosts]; ok {
		postsRaw, err := readZipFile(files, FilePosts)
		if err != nil {
			return rep, err
		}
		sc := bufio.NewScanner(bytes.NewReader(postsRaw))
		sc.Buffer(make([]byte, 0, 64<<10), MaxExportFile)
		pl := 0
		for sc.Scan() {
			raw := sc.Bytes()
			if len(bytes.TrimSpace(raw)) == 0 {
				continue
			}
			pl++
			var p PostLine
			if err := json.Unmarshal(raw, &p); err != nil {
				rep.Failures = append(rep.Failures, gate.VerifyFailure{Line: pl, Code: FailCommitMismatch})
				continue
			}
			want, ok := commits[postRef{p.ID, int64(p.Version)}]
			if !ok || !bytes.Equal(want, BodyCommit(p.Salt, p.Body)) {
				rep.Failures = append(rep.Failures, gate.VerifyFailure{Line: pl, Code: FailCommitMismatch})
				continue
			}
			rep.BodiesChecked++
		}
		if err := sc.Err(); err != nil {
			return rep, fmt.Errorf("records: posts.jsonl: %w", err)
		}
	}
	return rep, nil
}

// trustedKey decodes k's public key and checks its listed fingerprint.
func trustedKey(k gate.KeyView) (ed25519.PublicKey, bool) {
	pub, err := base64.RawURLEncoding.Strict().DecodeString(k.PublicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize || !strings.EqualFold(sigs.Fingerprint(pub), k.Fingerprint) {
		return nil, false
	}
	return pub, true
}

func until(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

// line verifies one records.jsonl line and returns its envelope, or the
// failure code.
func (v *verifier) line(raw []byte) (sigs.Envelope, string) {
	var env sigs.Envelope
	if err := json.Unmarshal(raw, &env); err != nil || len(env.Body) == 0 {
		return env, FailNotCanonical
	}
	t, hasTime := bodyTime(env.Body)
	signer := uuid.Nil
	if uk, ok := v.users[env.KeyID]; ok {
		if code := envelopeCode(env, uk.pub); code != "" {
			return env, code
		}
		if !v.countersigned(env, t, hasTime) {
			return env, FailBadCosig
		}
		signer = uk.owner
	} else {
		k, ok := v.server[env.KeyID]
		if !ok {
			return env, FailUnknownKey
		}
		if code := envelopeCode(env, k.pub); code != "" {
			return env, code
		}
		if hasTime && outsideWindow(k, t) {
			return env, FailKeyWindow
		}
	}
	return env, v.assertions(env, signer, t, hasTime)
}

func envelopeCode(env sigs.Envelope, pub ed25519.PublicKey) string {
	switch err := sigs.VerifyEnvelope(env, pub); {
	case err == nil:
		return ""
	case errors.Is(err, sigs.ErrNotCanonical):
		return FailNotCanonical
	case errors.Is(err, sigs.ErrKindMismatch), errors.Is(err, sigs.ErrNoRecord):
		return FailKindMismatch
	case errors.Is(err, sigs.ErrBadSig):
		return FailBadSig
	default:
		return FailNotCanonical
	}
}

// outsideWindow: only the upper bound is checked. A retired key cannot have
// signed a record dated after it was retired; there is no lower bound against
// valid_from, because a record signed after a rotation may legitimately carry
// an earlier time of its own (an edited Post version keeps the post's
// created_at; an Invite's issued_at is the handshake start).
func outsideWindow(k pubKey, t time.Time) bool {
	return !k.until.IsZero() && t.After(k.until.Add(windowSkew))
}

// countersigned reports whether a trusted server key countersigned env.
func (v *verifier) countersigned(env sigs.Envelope, t time.Time, hasTime bool) bool {
	for _, c := range env.Cosigs {
		k, ok := v.server[c.KeyID]
		if !ok || (hasTime && outsideWindow(k, t)) {
			continue
		}
		sig, err := base64.RawURLEncoding.Strict().DecodeString(c.Sig)
		if err == nil && sigs.Verify(k.pub, c.KeyID, env.Body, sig) {
			return true
		}
	}
	return false
}

// assertions checks env's passkey assertions and, for PasskeyChange records,
// advances the credential chain. signer is the user-key owner (Nil for a
// server-signed record).
func (v *verifier) assertions(env sigs.Envelope, signer uuid.UUID, t time.Time, hasTime bool) string {
	var body struct {
		Nonce *string `json:"nonce"`
	}
	_ = json.Unmarshal(env.Body, &body)
	mandatory := v.required != nil && hasTime && !t.Before(*v.required)
	var pc *passkeyChange
	if env.Kind == core.RecPasskeyChange.String() {
		pc = &passkeyChange{}
		if json.Unmarshal(env.Body, pc) != nil {
			return FailKindMismatch
		}
	}
	chained := pc != nil && pc.Op == opAdd && len(v.creds[pc.User]) > 0 && (mandatory || len(env.Assertions) > 0)
	if len(env.Assertions) == 0 {
		switch {
		case body.Nonce != nil, mandatory && highValue[env.Kind]:
			return FailMissingAssertion
		case chained:
			return FailUnchainedPasskey
		}
		v.advance(pc)
		return ""
	}
	challenge := sigs.Challenge(env.Kind, env.Body)
	bySigner, byChain := false, false
	for _, raw := range env.Assertions {
		var a AssertionJSON
		if json.Unmarshal(raw, &a) != nil || a.User == nil {
			return FailBadAssertion
		}
		if sigs.VerifyAssertion(sigs.AssertionCheck{PublicKey: a.PublicKey, Challenge: challenge, Origin: v.origin},
			a.AuthenticatorData, a.ClientDataJSON, a.Signature) != nil {
			return FailBadAssertion
		}
		known, have := v.creds[*a.User]
		inChain := have && bytes.Equal(known[credKey(a.CredentialID)], a.PublicKey)
		if *a.User == v.exporter && v.exporter != uuid.Nil && have && !inChain {
			return FailUnchainedPasskey // the exporter never added this credential
		}
		if *a.User == signer {
			bySigner = true
		}
		if pc != nil && *a.User == pc.User && inChain {
			byChain = true
		}
	}
	if signer != uuid.Nil && !bySigner {
		return FailBadAssertion
	}
	if chained && !byChain {
		return FailUnchainedPasskey
	}
	v.advance(pc)
	return ""
}

// advance applies a PasskeyChange to the credential chain.
func (v *verifier) advance(pc *passkeyChange) {
	if pc == nil {
		return
	}
	switch pc.Op {
	case opAdd:
		if v.creds[pc.User] == nil {
			v.creds[pc.User] = map[string][]byte{}
		}
		v.creds[pc.User][credKey(pc.CredentialID)] = pc.PublicKey
	case opRemove:
		delete(v.creds[pc.User], credKey(pc.CredentialID))
	}
}

func credKey(id []byte) string { return base64.RawURLEncoding.EncodeToString(id) }
