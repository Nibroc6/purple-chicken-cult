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
	"strings"
	"time"

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

// KeysFile is the content of keys.json.
type KeysFile struct {
	Server []gate.KeyView `json:"server"`
}

// ExportRecords returns records.jsonl (one sigs.Envelope per line: the exact
// stored bytes of every record user is a party to, oldest first; Report
// records are moderation-only and never exported to anyone but their author)
// and keys.json (every published server key with validity and fingerprint).
func (s *Service) ExportRecords(ctx context.Context, q store.Q, user core.UserID) (recordsJSONL, keysJSON []byte, err error) {
	q = s.q(q)
	rows, err := q.Query(ctx, `SELECT r.key_id, r.body, r.sig, r.cosigs, r.assertions FROM signed_records r
		JOIN record_parties p ON p.record_hash = r.hash
		WHERE p.user_id = $1 AND (r.kind <> $2 OR r.author_id = $1)
		ORDER BY r.created_at, r.hash`, int64(user), int16(core.RecReport))
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // never touch the signed bytes
	for rows.Next() {
		var (
			keyID              string
			body, sig          []byte
			cosigs, assertions []byte
		)
		if err := rows.Scan(&keyID, &body, &sig, &cosigs, &assertions); err != nil {
			return nil, nil, err
		}
		kind, sv, err := sigs.RecordFields(body)
		if err != nil {
			return nil, nil, err
		}
		env := sigs.Envelope{Kind: kind, SchemaVersion: sv, KeyID: keyID, Body: json.RawMessage(body),
			Sig: base64.RawURLEncoding.EncodeToString(sig)}
		if len(cosigs) > 0 {
			if err := json.Unmarshal(cosigs, &env.Cosigs); err != nil {
				return nil, nil, err
			}
		}
		if len(assertions) > 0 {
			if err := json.Unmarshal(assertions, &env.Assertions); err != nil {
				return nil, nil, err
			}
		}
		if err := enc.Encode(env); err != nil {
			return nil, nil, err
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
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
	keysJSON, err = json.MarshalIndent(KeysFile{Server: keys}, "", "  ")
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
)

// windowSkew tolerates small differences between a body's own timestamp and
// the moment the record was signed.
const windowSkew = 10 * time.Minute

// MaxExportFile bounds every file read from an export (zip-bomb guard).
const MaxExportFile = 256 << 20

type pubKey struct {
	pub         ed25519.PublicKey
	until time.Time // zero while the key is current
}

// VerifyExport verifies an export zip offline. Every records.jsonl line must be
// canonical as stored, its envelope kind and schema_version must match the
// body, the key must be listed in keys.json (and, when pinned is non-empty,
// match the published fingerprint key_id → hex sha256), the body time must lie
// in the key's validity window and the signature must verify. posts.jsonl lines
// must open the body_commit of their Post record. Failure lines number the
// lines of the file the code refers to (records.jsonl; posts.jsonl for
// commit_mismatch). An error means the file is not a readable export.
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
		pub, err := base64.RawURLEncoding.Strict().DecodeString(k.PublicKey)
		if err != nil || len(pub) != ed25519.PublicKeySize {
			continue
		}
		fp := sigs.Fingerprint(pub)
		if !strings.EqualFold(fp, k.Fingerprint) || sigs.ServerKeyID(pub) != k.KeyID {
			continue // a key whose listed fingerprint or ID does not match its bytes is not trusted
		}
		if len(pinned) > 0 && !strings.EqualFold(strings.TrimSpace(pinned[k.KeyID]), fp) {
			continue
		}
		pk := pubKey{pub: pub}
		if k.ValidUntil != nil {
			pk.until = *k.ValidUntil
		}
		keys[k.KeyID] = pk
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
		code := verifyLine(raw, keys)
		if code != "" {
			rep.Failures = append(rep.Failures, gate.VerifyFailure{Line: line, Code: code})
			continue
		}
		rep.Valid++
		var env sigs.Envelope
		_ = json.Unmarshal(raw, &env)
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

// verifyLine returns "" for a valid records.jsonl line, else its failure code.
func verifyLine(raw []byte, keys map[string]pubKey) string {
	var env sigs.Envelope
	if err := json.Unmarshal(raw, &env); err != nil || len(env.Body) == 0 {
		return FailNotCanonical
	}
	k, ok := keys[env.KeyID]
	if !ok {
		return FailUnknownKey
	}
	switch err := sigs.VerifyEnvelope(env, k.pub); {
	case err == nil:
	case errors.Is(err, sigs.ErrNotCanonical):
		return FailNotCanonical
	case errors.Is(err, sigs.ErrKindMismatch), errors.Is(err, sigs.ErrNoRecord):
		return FailKindMismatch
	case errors.Is(err, sigs.ErrBadSig):
		return FailBadSig
	default:
		return FailNotCanonical
	}
	// Only the upper bound is checked: a retired key cannot have signed a
	// record dated after it was retired. There is no lower bound against
	// valid_from, because a record signed after a rotation may legitimately
	// carry an earlier time of its own (an edited Post version keeps the
	// post's created_at; an Invite's issued_at is the handshake start).
	if t, ok := bodyTime(env.Body); ok {
		if !k.until.IsZero() && t.After(k.until.Add(windowSkew)) {
			return FailKeyWindow
		}
	}
	return ""
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
