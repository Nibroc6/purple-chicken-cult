package records

import (
	"archive/zip"
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

	"github.com/google/uuid"

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

// KeysFile is the content of keys.json. Users lists the custodial keys that
// signed an exported record (each record signed by one is countersigned by a
// server key); Origin is the first-party origin every passkey assertion must
// come from; CosignRequiredFrom is when co-signing became mandatory (absent
// while it is not).
type KeysFile struct {
	Server             []gate.KeyView `json:"server"`
	Users              []UserKeyView  `json:"users,omitempty"`
	Origin             string         `json:"origin,omitempty"`
	CosignRequiredFrom *time.Time     `json:"cosign_required_from,omitempty"`
}

// UserKeyView is a published per-user key: a KeyView plus its owner's ext_id.
type UserKeyView struct {
	gate.KeyView
	Owner uuid.UUID `json:"owner"`
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
	userKeys := map[string]bool{}
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
		if strings.HasPrefix(keyID, UserKeyPrefix) {
			userKeys[keyID] = true
		}
		if err := enc.Encode(env); err != nil {
			return nil, nil, err
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	rows.Close()
	if k := s.current(); k != nil {
		if err := s.ensureKey(ctx, q, k); err != nil && !errors.Is(err, errRetired) {
			return nil, nil, err
		}
	}
	keys, err := loadKeys(ctx, q)
	if err != nil {
		return nil, nil, err
	}
	kf := KeysFile{Server: keys, Origin: s.d.Cfg.Origin}
	if kf.Users, err = s.loadUserKeys(ctx, q, userKeys); err != nil {
		return nil, nil, err
	}
	if kf.CosignRequiredFrom, err = s.CosignRequiredFrom(ctx, q); err != nil {
		return nil, nil, err
	}
	keysJSON, err = json.MarshalIndent(kf, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return buf.Bytes(), keysJSON, nil
}

// loadUserKeys returns the published user keys among ids, oldest first.
func (s *Service) loadUserKeys(ctx context.Context, q store.Q, ids map[string]bool) ([]UserKeyView, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	list := make([]string, 0, len(ids))
	for id := range ids {
		list = append(list, id)
	}
	rows, err := q.Query(ctx, `SELECT k.key_id, k.alg, k.public_key, k.valid_from, k.valid_until, u.ext_id
		FROM signing_keys k JOIN users u ON u.id = k.owner_id
		WHERE k.key_id = ANY($1) ORDER BY k.valid_from, k.key_id`, list)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserKeyView
	for rows.Next() {
		var (
			kv  UserKeyView
			pub []byte
		)
		if err := rows.Scan(&kv.KeyID, &kv.Alg, &pub, &kv.ValidFrom, &kv.ValidUntil, &kv.Owner); err != nil {
			return nil, err
		}
		kv.PublicKey = base64.RawURLEncoding.EncodeToString(pub)
		kv.Fingerprint = sigs.Fingerprint(ed25519.PublicKey(pub))
		kv.ValidFrom = kv.ValidFrom.UTC()
		if kv.ValidUntil != nil {
			u := kv.ValidUntil.UTC()
			kv.ValidUntil = &u
		}
		out = append(out, kv)
	}
	return out, rows.Err()
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
