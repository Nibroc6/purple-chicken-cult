package records

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/sigs"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// Export file names (§10.2). account.Export writes the zip; records produces
// the signed parts and verifies the whole offline.
const (
	FileRecords     = "records.jsonl"
	FileKeys        = "keys.json"
	FilePosts       = "posts.jsonl"
	FileCredentials = "credentials.jsonl"
	FileManifest    = "manifest.json"
)

// KeysFile is the content of keys.json: every published server key, the
// custodial public keys of the users whose keys signed an exported record (P1),
// and the co-sign cut-over (P1): high-value records dated from then on must
// carry a valid passkey assertion.
type KeysFile struct {
	Server      []gate.KeyView `json:"server"`
	Users       []UserKeyView  `json:"users,omitempty"`
	CosignSince *time.Time     `json:"cosign_since,omitempty"`
}

// ExportRecords returns records.jsonl (one sigs.Envelope per line: the exact
// stored bytes of every record user is a party to, oldest first; Report
// records are moderation-only and never exported to anyone but their author),
// followed by the credential chain of every other person whose passkey
// assertion is attached to one of those records (their PasskeyChange records,
// so each assertion verifies offline), and keys.json (every published server
// key, the user keys that signed an exported record, the co-sign cut-over).
func (s *Service) ExportRecords(ctx context.Context, q store.Q, user core.UserID) (recordsJSONL, keysJSON []byte, err error) {
	q = s.q(q)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // never touch the signed bytes
	seen := map[string]bool{}
	keyIDs := map[string]bool{}
	signers := map[uuid.UUID]bool{}
	write := func(rows pgx.Rows) error {
		defer rows.Close()
		for rows.Next() {
			var (
				keyID              string
				hash, body, sig    []byte
				cosigs, assertions []byte
			)
			if err := rows.Scan(&hash, &keyID, &body, &sig, &cosigs, &assertions); err != nil {
				return err
			}
			if seen[string(hash)] {
				continue
			}
			seen[string(hash)] = true
			kind, sv, err := sigs.RecordFields(body)
			if err != nil {
				return err
			}
			env := sigs.Envelope{Kind: kind, SchemaVersion: sv, KeyID: keyID, Body: json.RawMessage(body),
				Sig: base64.RawURLEncoding.EncodeToString(sig)}
			keyIDs[keyID] = true
			if len(cosigs) > 0 {
				if err := json.Unmarshal(cosigs, &env.Cosigs); err != nil {
					return err
				}
			}
			if len(assertions) > 0 {
				if err := json.Unmarshal(assertions, &env.Assertions); err != nil {
					return err
				}
				for _, raw := range env.Assertions {
					var a assertionJSON
					if json.Unmarshal(raw, &a) == nil && a.Signer != nil {
						signers[*a.Signer] = true
					}
				}
			}
			if err := enc.Encode(env); err != nil {
				return err
			}
		}
		return rows.Err()
	}
	rows, err := q.Query(ctx, `SELECT r.hash, r.key_id, r.body, r.sig, r.cosigs, r.assertions FROM signed_records r
		JOIN record_parties p ON p.record_hash = r.hash
		WHERE p.user_id = $1 AND (r.kind <> $2 OR r.author_id = $1)
		ORDER BY r.created_at, r.hash`, int64(user), int16(core.RecReport))
	if err != nil {
		return nil, nil, err
	}
	if err := write(rows); err != nil {
		return nil, nil, err
	}
	if len(signers) > 0 {
		exts := make([]uuid.UUID, 0, len(signers))
		for e := range signers {
			exts = append(exts, e)
		}
		rows, err := q.Query(ctx, `SELECT r.hash, r.key_id, r.body, r.sig, r.cosigs, r.assertions FROM signed_records r
			JOIN record_parties p ON p.record_hash = r.hash JOIN users u ON u.id = p.user_id
			WHERE r.kind = $1 AND u.ext_id = ANY($2) AND u.id <> $3
			ORDER BY r.created_at, r.hash`, int16(core.RecPasskeyChange), exts, int64(user))
		if err != nil {
			return nil, nil, err
		}
		if err := write(rows); err != nil {
			return nil, nil, err
		}
	}
	if k := s.current(); k != nil {
		if err := s.ensureKey(ctx, q, k); err != nil && !errors.Is(err, errRetired) {
			return nil, nil, err
		}
	}
	keys, err := loadKeys(ctx, q)
	if err != nil {
		return nil, nil, err
	}
	ids := make([]string, 0, len(keyIDs))
	for id := range keyIDs {
		if IsUserKeyID(id) {
			ids = append(ids, id)
		}
	}
	urows, err := q.Query(ctx, `SELECT k.key_id, k.alg, k.public_key, k.valid_from, k.valid_until, u.ext_id
		FROM signing_keys k JOIN users u ON u.id = k.owner_id
		WHERE k.owner_id = $1 OR k.key_id = ANY($2) ORDER BY k.valid_from, k.key_id`, int64(user), ids)
	if err != nil {
		return nil, nil, err
	}
	users, err := scanUserKeys(urows)
	if err != nil {
		return nil, nil, err
	}
	since, err := s.cosignSince(ctx, q)
	if err != nil {
		return nil, nil, err
	}
	keysJSON, err = json.MarshalIndent(KeysFile{Server: keys, Users: users, CosignSince: since}, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return buf.Bytes(), keysJSON, nil
}

// PostLine is one line of posts.jsonl: a post body the user wrote, with the
// salt that opens the signed body_commit = sha256(salt ‖ utf8(body)).
type PostLine struct {
	ID      string     `json:"id"`
	Version int        `json:"version"`
	Body    string     `json:"body"`
	Salt    sigs.Bytes `json:"salt"`
}

// BodyCommit is the commitment a Post record signs (D3, security #25).
func BodyCommit(salt []byte, body string) []byte {
	h := sha256.New()
	h.Write(salt)
	h.Write([]byte(body))
	return h.Sum(nil)
}

// Verify failure codes (gate.VerifyFailure.Code).
const (
	FailBadSig         = "bad_sig"
	FailKindMismatch   = "kind_mismatch"
	FailNotCanonical   = "not_canonical"
	FailUnknownKey     = "unknown_key"
	FailKeyWindow      = "key_window"
	FailCommitMismatch = "commit_mismatch"
	// P1
	FailBadCosig         = "bad_cosig"         // a user-key record without a valid server countersign
	FailBadAssertion     = "bad_assertion"     // an attached assertion does not verify for this record
	FailMissingAssertion = "missing_assertion" // a high-value record after the cut-over without a party's assertion
	FailBrokenChain      = "broken_chain"      // a passkey add not co-signed by an earlier credential of the user
)

// windowSkew tolerates small differences between a body's own timestamp and
// the moment the record was signed.
const windowSkew = 10 * time.Minute

// MaxExportFile bounds every file read from an export (zip-bomb guard).
const MaxExportFile = 256 << 20

type pubKey struct {
	pub   ed25519.PublicKey
	until time.Time // zero while the key is current
}

// VerifyExport verifies an export zip offline. Every records.jsonl line must be
// canonical as stored, its envelope kind and schema_version must match the
// body, and its signature must verify: a server-key record under a key listed
// in keys.json (and, when pinned is non-empty, matching the published
// fingerprint key_id → hex sha256) whose validity window covers the body time;
// a user-key record (P1) under a self-certifying user key listed in keys.json
// AND a countersign by such a server key. Every attached passkey assertion must
// verify for its record (assertions.go) under a credential from the export's
// PasskeyChange records belonging to the assertion's signer. From the co-sign
// cut-over in keys.json on, Invite/Friendship/Revocation records and passkey
// changes need a valid assertion of a party (missing_assertion), and a
// passkey add after the user's first must be co-signed by an earlier, still
// present credential of the same user — the first being the one the user's
// Invite names (broken_chain). posts.jsonl lines must open the body_commit of
// their Post record. Failure lines number the lines of the file the code
// refers to (records.jsonl; posts.jsonl for commit_mismatch). An error means
// the file is not a readable export.
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
	keys := map[string]pubKey{}
	for _, k := range kf.Server {
		pub, ok := trustedPub(k)
		if !ok || sigs.ServerKeyID(pub) != k.KeyID {
			continue // a key whose listed fingerprint or ID does not match its bytes is not trusted
		}
		if len(pinned) > 0 && !strings.EqualFold(strings.TrimSpace(pinned[k.KeyID]), sigs.Fingerprint(pub)) {
			continue
		}
		pk := pubKey{pub: pub}
		if k.ValidUntil != nil {
			pk.until = *k.ValidUntil
		}
		keys[k.KeyID] = pk
	}
	userKeys := map[string]pubKey{}
	for _, k := range kf.Users {
		if pub, ok := trustedPub(k.KeyView); ok && UserKeyID(pub) == k.KeyID {
			userKeys[k.KeyID] = pubKey{pub: pub}
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
	var lines []*vline
	failed := map[int]bool{}
	fail := func(line int, code string) {
		if failed[line] {
			return
		}
		failed[line] = true
		rep.Failures = append(rep.Failures, gate.VerifyFailure{Line: line, Code: code})
	}
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
		var env sigs.Envelope
		code := verifyLine(raw, keys, userKeys, &env)
		if code != "" {
			fail(line, code)
			continue
		}
		lines = append(lines, &vline{line: line, env: env})
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
	checkAttestations(lines, kf.CosignSince, fail)
	rep.Valid = rep.Records - len(rep.Failures)

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

// trustedPub decodes a listed key whose fingerprint matches its bytes.
func trustedPub(k gate.KeyView) (ed25519.PublicKey, bool) {
	pub, err := base64.RawURLEncoding.Strict().DecodeString(k.PublicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize || !strings.EqualFold(sigs.Fingerprint(pub), k.Fingerprint) {
		return nil, false
	}
	return pub, true
}

func readZipFile(files map[string]*zip.File, name string) ([]byte, error) {
	f, ok := files[name]
	if !ok {
		return nil, fmt.Errorf("records: export lacks %s", name)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(io.LimitReader(rc, MaxExportFile+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxExportFile {
		return nil, fmt.Errorf("records: %s is too large", name)
	}
	return b, nil
}

// verifyLine returns "" for a records.jsonl line whose signatures are valid,
// else its failure code; env receives the parsed envelope.
func verifyLine(raw []byte, keys, userKeys map[string]pubKey, env *sigs.Envelope) string {
	if err := json.Unmarshal(raw, env); err != nil || len(env.Body) == 0 {
		return FailNotCanonical
	}
	k, server := keys[env.KeyID]
	if !server {
		if k, ok := userKeys[env.KeyID]; ok {
			if code := envelopeCode(*env, k.pub); code != "" {
				return code
			}
			return countersignCode(*env, keys)
		}
		return FailUnknownKey
	}
	if code := envelopeCode(*env, k.pub); code != "" {
		return code
	}
	return windowCode(env.Body, k)
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

// windowCode checks the record time against a server key's window. Only the
// upper bound is checked: a retired key cannot have signed a record dated
// after it was retired. There is no lower bound against valid_from, because a
// record signed after a rotation may legitimately carry an earlier time of its
// own (an edited Post version keeps the post's created_at; an Invite's
// issued_at is the handshake start).
func windowCode(body []byte, k pubKey) string {
	if t, ok := bodyTime(body); ok {
		if !k.until.IsZero() && t.After(k.until.Add(windowSkew)) {
			return FailKeyWindow
		}
	}
	return ""
}

// countersignCode requires one valid server countersign within its window.
func countersignCode(env sigs.Envelope, keys map[string]pubKey) string {
	code := FailBadCosig
	for _, c := range env.Cosigs {
		k, ok := keys[c.KeyID]
		if !ok {
			continue
		}
		sig, err := base64.RawURLEncoding.Strict().DecodeString(c.Sig)
		if err != nil || !sigs.Verify(k.pub, c.KeyID, env.Body, sig) {
			continue
		}
		if code = windowCode(env.Body, k); code == "" {
			return ""
		}
	}
	return code
}

// vline is a records.jsonl line whose signatures verified.
type vline struct {
	line int
	env  sigs.Envelope
	at   time.Time
	// parsed fields used by the attestation checks
	pc      *passkeyChange
	parties []uuid.UUID
	signers []uuid.UUID // signers of the valid assertions
}

type passkeyChange struct {
	User         uuid.UUID  `json:"user"`
	Op           string     `json:"op"`
	CredentialID sigs.Bytes `json:"credential_id"`
	PublicKey    sigs.Bytes `json:"public_key"`
	At           sigs.Time  `json:"at"`
}

type credInfo struct {
	owner   uuid.UUID
	pub     []byte
	removed *time.Time
}

// highValue reports whether a record kind needs a party's assertion after the
// co-sign cut-over (ARCHITECTURE §3 Phase 1).
func highValue(kind string) bool {
	switch kind {
	case core.RecInvite.String(), core.RecFriendship.String(), core.RecRevocation.String(), core.RecPasskeyChange.String():
		return true
	}
	return false
}

// checkAttestations verifies every attached assertion and, from the cut-over
// on, the assertion requirements and the per-user credential chain.
func checkAttestations(lines []*vline, since *time.Time, fail func(int, string)) {
	creds := map[string]*credInfo{}
	adds := map[uuid.UUID][]*vline{}
	firstCred := map[uuid.UUID][]byte{} // invitee → credential named by the Invite
	for _, l := range lines {
		l.at, _ = bodyTime(l.env.Body)
		switch l.env.Kind {
		case core.RecPasskeyChange.String():
			var pc passkeyChange
			if json.Unmarshal(l.env.Body, &pc) != nil || len(pc.CredentialID) == 0 {
				continue
			}
			l.pc, l.parties = &pc, []uuid.UUID{pc.User}
			id := string(pc.CredentialID)
			switch pc.Op {
			case "add":
				if _, dup := creds[id]; !dup {
					creds[id] = &credInfo{owner: pc.User, pub: pc.PublicKey}
					adds[pc.User] = append(adds[pc.User], l)
				}
			case "remove":
				if c, ok := creds[id]; ok && c.owner == pc.User {
					t := pc.At.Time()
					c.removed = &t
				}
			}
		case core.RecInvite.String():
			var ib struct {
				Inviter           uuid.UUID  `json:"inviter"`
				Invitee           uuid.UUID  `json:"invitee"`
				InviteeCredential sigs.Bytes `json:"invitee_credential"`
			}
			if json.Unmarshal(l.env.Body, &ib) == nil {
				l.parties = []uuid.UUID{ib.Inviter, ib.Invitee}
				if len(ib.InviteeCredential) > 0 {
					firstCred[ib.Invitee] = ib.InviteeCredential
				}
			}
		case core.RecFriendship.String():
			var fb struct {
				A uuid.UUID `json:"a"`
				B uuid.UUID `json:"b"`
			}
			if json.Unmarshal(l.env.Body, &fb) == nil {
				l.parties = []uuid.UUID{fb.A, fb.B}
			}
		case core.RecRevocation.String():
			var rb struct {
				By uuid.UUID `json:"by"`
			}
			if json.Unmarshal(l.env.Body, &rb) == nil {
				l.parties = []uuid.UUID{rb.By}
			}
		}
	}
	// Attached assertions.
	signedBy := map[*vline]map[string]bool{} // line → credential IDs that validly signed it
	for _, l := range lines {
		for _, raw := range l.env.Assertions {
			var a assertionJSON
			if json.Unmarshal(raw, &a) != nil {
				fail(l.line, FailBadAssertion)
				break
			}
			if _, err := bindAssertion(l.env.Kind, l.env.Body, a.Draft, bundleBytes(a.Bundle), a.ClientDataJSON,
				a.AuthenticatorData); err != nil {
				fail(l.line, FailBadAssertion)
				break
			}
			c, ok := creds[string(a.CredentialID)]
			if !ok || (a.Signer != nil && *a.Signer != c.owner) ||
				!verifyWebAuthnSig(c.pub, a.AuthenticatorData, a.ClientDataJSON, a.Signature) {
				fail(l.line, FailBadAssertion)
				break
			}
			if signedBy[l] == nil {
				signedBy[l] = map[string]bool{}
			}
			signedBy[l][string(a.CredentialID)] = true
			l.signers = append(l.signers, c.owner)
		}
	}
	if since == nil {
		return
	}
	cut := since.Add(-windowSkew)
	// The credential chain: every add after the user's first is co-signed by
	// an earlier credential of the same user that was not removed before it.
	for user, ls := range adds {
		// By time; within one instant an add co-signed by another's credential
		// comes after it (export order alone breaks no tie).
		slices.SortStableFunc(ls, func(a, b *vline) int {
			if c := a.pc.At.Time().Compare(b.pc.At.Time()); c != 0 {
				return c
			}
			switch {
			case signedBy[a][string(b.pc.CredentialID)]:
				return 1
			case signedBy[b][string(a.pc.CredentialID)]:
				return -1
			}
			return 0
		})
		first := ls[0]
		if want, ok := firstCred[user]; ok && !bytes.Equal(want, first.pc.CredentialID) {
			fail(first.line, FailBrokenChain)
		}
		for i, l := range ls {
			if i == 0 || l.at.Before(cut) {
				continue
			}
			ok := false
			for _, prev := range ls[:i] {
				c := creds[string(prev.pc.CredentialID)]
				if signedBy[l][string(prev.pc.CredentialID)] && (c.removed == nil || c.removed.After(l.at)) {
					ok = true
				}
			}
			if !ok {
				fail(l.line, FailBrokenChain)
			}
		}
	}
	// Every other high-value record needs an assertion of one of its parties.
	for _, l := range lines {
		if !highValue(l.env.Kind) || l.at.Before(cut) {
			continue
		}
		if l.pc != nil && l.pc.Op == "add" {
			continue // the chain rule above
		}
		ok := false
		for _, sgn := range l.signers {
			if slices.Contains(l.parties, sgn) {
				ok = true
			}
		}
		if !ok {
			fail(l.line, FailMissingAssertion)
		}
	}
}

// bodyTime is the record's own timestamp (created_at, at or issued_at).
func bodyTime(body []byte) (time.Time, bool) {
	var b struct {
		CreatedAt *sigs.Time `json:"created_at"`
		At        *sigs.Time `json:"at"`
		IssuedAt  *sigs.Time `json:"issued_at"`
	}
	if json.Unmarshal(body, &b) != nil {
		return time.Time{}, false
	}
	for _, t := range []*sigs.Time{b.CreatedAt, b.At, b.IssuedAt} {
		if t != nil {
			return t.Time(), true
		}
	}
	return time.Time{}, false
}
