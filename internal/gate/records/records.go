// Package records owns signed records (ARCHITECTURE §3, §10.2): JCS + Ed25519
// under the server key (Phase 0; P1 adds per-user custodial keys and passkey
// assertions), signing_keys / signed_records / record_parties, key rotation,
// fingerprints, offline export verification and the edge audit. It implements
// core.Signer.
//
// Owner: P0-W11 → P1-W01.
//
// Records are stored as the exact canonical bytes that were signed: nothing is
// ever re-canonicalized on the way out, so two users' exports of a shared record
// are byte-identical. The signature covers "pcc-rec-v1" NUL key_id NUL body
// (domain separation and key binding, sigs.SignedMessage).
//
// Signer v2 (P1): a record with an author is signed by the author's custodial
// key (key_id "usr-…", userkeys.go) and countersigned by the server key
// (cosigs [{key_id, sig}] over the same message); server-authored records keep
// the P0 form. Passkey assertions attached by co-signing are stored with the
// draft each one signed (assertions.go), so exports verify offline.
package records

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coreref"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/sigs"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// Job kinds registered by the records module (plan §4.3).
const (
	JobPurge      = "records.purge"
	JobAuditEdges = "records.audit_edges"
)

// Periods of the records jobs.
const (
	PurgeEvery = 24 * time.Hour
	AuditEvery = 7 * 24 * time.Hour
)

// Service is the records module.
type Service struct {
	d *core.Deps

	mu  sync.RWMutex
	key *sigs.Key
}

var (
	_ core.Signer         = (*Service)(nil)
	_ core.RecordExporter = (*Service)(nil)
)

// New returns the service.
func New(d *core.Deps) *Service { return &Service{d: d} }

// SetKey installs the server key (tests, wiretest). Must be called before Init.
func (s *Service) SetKey(k *sigs.Key) { s.key = k }

// Init loads (dev/test: creates) the server signing key, publishes it in
// signing_keys and registers the purge and audit jobs.
func (s *Service) Init(ctx context.Context) error {
	if err := s.checkKEK(); err != nil {
		return err
	}
	if s.key == nil {
		k, err := LoadOrCreateServerKey(s.d.Cfg.ServerKeyFile, s.d.Cfg.DevLike())
		if err != nil {
			return err
		}
		s.key = k
	}
	if s.d.DB != nil {
		// A key already published keeps its validity window.
		var from time.Time
		var until *time.Time
		err := s.d.DB.Q().QueryRow(ctx, "SELECT valid_from, valid_until FROM signing_keys WHERE key_id = $1", s.key.ID).
			Scan(&from, &until)
		switch {
		case err == nil:
			s.key.From = from
			if until != nil {
				if !until.After(s.d.Clock.Now()) {
					return errors.New("records: the server key file holds a retired key")
				}
				s.key.Until = *until
			}
		case errors.Is(err, pgx.ErrNoRows):
		default:
			return err
		}
	}
	if s.key.From.IsZero() {
		s.key.From = s.d.Clock.Now()
	}
	if s.d.DB != nil {
		if err := s.ensureKey(ctx, s.d.DB.Q(), s.key); err != nil {
			return err
		}
	}
	if s.d.Jobs != nil {
		s.d.Jobs.Handle(JobPurge, func(ctx context.Context, _ json.RawMessage) error {
			_, err := s.Purge(ctx)
			return err
		})
		s.d.Jobs.Every(JobPurge, PurgeEvery)
		s.d.Jobs.Handle(JobAuditEdges, func(ctx context.Context, _ json.RawMessage) error {
			bad, err := s.AuditEdges(ctx)
			if err != nil {
				return err
			}
			if bad > 0 && s.d.Log != nil {
				s.d.Log.Warn("edge audit found edges without a valid friendship record", "job", JobAuditEdges, "count", bad)
			}
			return nil
		})
		s.d.Jobs.Every(JobAuditEdges, AuditEvery)
	}
	return nil
}

// LoadOrCreateServerKey loads the seed at path; when it does not exist and
// create is true (cfg.DevLike()) it generates one. Prod never generates a key.
func LoadOrCreateServerKey(path string, create bool) (*sigs.Key, error) {
	if path == "" {
		return nil, errors.New("records: PCC_SERVER_KEY_FILE is not set")
	}
	k, err := sigs.LoadServerKey(path)
	if errors.Is(err, fs.ErrNotExist) && create {
		return sigs.CreateServerKey(path)
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("records: server key file missing (never generated outside dev/test)")
	}
	return k, err
}

func (s *Service) current() *sigs.Key {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.key
}

var errRetired = errors.New("records: server key retired")

// ensureKey publishes k in signing_keys (idempotent: the table may have been
// truncated by the test hooks) and reports errRetired if k has been retired.
func (s *Service) ensureKey(ctx context.Context, q store.Q, k *sigs.Key) error {
	var until *time.Time
	err := q.QueryRow(ctx, `WITH ins AS (
		INSERT INTO signing_keys (key_id, alg, public_key, valid_from) VALUES ($1, 'ed25519', $2, $3)
		ON CONFLICT (key_id) DO NOTHING RETURNING valid_until)
		SELECT valid_until FROM ins UNION ALL SELECT valid_until FROM signing_keys WHERE key_id = $1 LIMIT 1`,
		k.ID, []byte(k.Pub), k.From).Scan(&until)
	if err != nil {
		return err
	}
	if until != nil && !until.After(s.d.Clock.Now()) {
		return errRetired
	}
	return nil
}

// signingKey returns the key to sign with inside q, following a rotation done by
// another process (the CLI) by reloading the key file once.
func (s *Service) signingKey(ctx context.Context, q store.Q) (*sigs.Key, error) {
	k := s.current()
	if k == nil {
		return nil, errors.New("records: not initialized")
	}
	err := s.ensureKey(ctx, q, k)
	if !errors.Is(err, errRetired) {
		return k, err
	}
	nk, lerr := sigs.LoadServerKey(s.d.Cfg.ServerKeyFile)
	if lerr != nil {
		return nil, fmt.Errorf("%w (reload: %w)", err, lerr)
	}
	var from time.Time
	if err := q.QueryRow(ctx, "SELECT valid_from FROM signing_keys WHERE key_id = $1 AND valid_until IS NULL", nk.ID).
		Scan(&from); err != nil {
		return nil, fmt.Errorf("records: reloaded server key is not published: %w", err)
	}
	nk.From = from
	s.mu.Lock()
	s.key = nk
	s.mu.Unlock()
	return nk, nil
}

func (s *Service) q(tx store.Q) store.Q {
	if tx != nil {
		return tx
	}
	return s.d.DB.Q()
}

// ---- handler / CLI API ----

// PublishedKeys is Keys for a signed-in user (GET /api/keys).
func (s *Service) PublishedKeys(ctx context.Context) ([]gate.KeyView, error) {
	if _, err := core.RequireUser(ctx); err != nil {
		return nil, err
	}
	return s.Keys(ctx)
}

// Keys returns every published server key (current and retired) with its
// validity window and fingerprint, oldest first.
func (s *Service) Keys(ctx context.Context) ([]gate.KeyView, error) {
	if k := s.current(); k != nil {
		if err := s.ensureKey(ctx, s.d.DB.Q(), k); err != nil && !errors.Is(err, errRetired) {
			return nil, err
		}
	}
	return loadKeys(ctx, s.d.DB.Q())
}

func loadKeys(ctx context.Context, q store.Q) ([]gate.KeyView, error) {
	rows, err := q.Query(ctx, `SELECT key_id, alg, public_key, valid_from, valid_until FROM signing_keys
		WHERE owner_id IS NULL ORDER BY valid_from, key_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []gate.KeyView{}
	for rows.Next() {
		var (
			kv  gate.KeyView
			pub []byte
		)
		if err := rows.Scan(&kv.KeyID, &kv.Alg, &pub, &kv.ValidFrom, &kv.ValidUntil); err != nil {
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

// RotateServerKey creates a new server key, closes the current key's validity
// window and replaces the key file (CLI). It returns "key_id  fingerprint" of
// the new key: the line to publish out-of-band.
func (s *Service) RotateServerKey(ctx context.Context) (string, error) {
	path := s.d.Cfg.ServerKeyFile
	if path == "" {
		return "", errors.New("records: PCC_SERVER_KEY_FILE is not set")
	}
	old := s.current()
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		return "", err
	}
	nk, err := sigs.KeyFromSeed(seed)
	if err != nil {
		return "", err
	}
	now := s.d.Clock.Now()
	nk.From = now
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	tmp := path + ".new"
	if err := os.WriteFile(tmp, seed, 0o600); err != nil {
		return "", err
	}
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		if old != nil {
			if err := s.ensureKey(ctx, tx, old); err != nil && !errors.Is(err, errRetired) {
				return err
			}
			if _, err := tx.Exec(ctx, "UPDATE signing_keys SET valid_until = $2 WHERE key_id = $1 AND valid_until IS NULL",
				old.ID, now); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, "INSERT INTO signing_keys (key_id, alg, public_key, valid_from) VALUES ($1, 'ed25519', $2, $3)",
			nk.ID, []byte(nk.Pub), now)
		return err
	})
	if err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", fmt.Errorf("records: new key %s is published but the key file was not replaced: %w", nk.ID, err)
	}
	s.mu.Lock()
	s.key = nk
	s.mu.Unlock()
	return nk.ID + "  " + sigs.Fingerprint(nk.Pub), nil
}

// Purge deletes every record past its purge_after (§2.3) and returns how many.
func (s *Service) Purge(ctx context.Context) (int, error) {
	tag, err := s.d.DB.Q().Exec(ctx, "DELETE FROM signed_records WHERE purge_after IS NOT NULL AND purge_after <= $1",
		s.d.Clock.Now())
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// ---- core.Signer ----

// Sign canonicalizes body (value grammar; "kind" and "schema_version" are
// injected), signs it — with the author's custodial key plus a server
// countersign, or with the server key alone for server-authored records — and
// stores it with its parties and assertions in tx. It returns the record hash.
//
// Every assertion must carry the draft it signed (Assertion.Draft, filled by
// Cosign.Require / SignupCosign) and that draft must be a co-sign draft of
// exactly this record with a matching challenge; a nil body takes the record
// from the first assertion's draft. Signing an identical body twice is
// idempotent; new assertions on an existing record are appended.
func (s *Service) Sign(ctx context.Context, tx store.Q, kind core.RecordKind, author core.UserID, body any, parties []core.UserID, a ...core.Assertion) ([]byte, error) {
	if kind.String() == "" {
		return nil, fmt.Errorf("records: unknown record kind %d", kind)
	}
	var (
		canon []byte
		err   error
	)
	if body == nil && len(a) > 0 {
		canon, _, _, err = coreref.RecordFromDraft(a[0].Draft)
	} else {
		canon, err = sigs.CanonicalRecord(kind.String(), core.RecordSchemaVersion, body)
	}
	if err != nil {
		return nil, err
	}
	for _, as := range a {
		if _, err := bindAssertion(kind.String(), canon, as.Draft, as.Bundle, as.ClientDataJSON, as.AuthenticatorData); err != nil {
			return nil, err
		}
	}
	q := s.q(tx)
	k, err := s.signingKey(ctx, q)
	if err != nil {
		return nil, err
	}
	h := sigs.RecordHash(canon)
	var authorID *int64
	keyID, sig := k.ID, k.Sign(canon)
	var cosigs []byte
	if author != 0 {
		v := int64(author)
		authorID = &v
		uk, err := s.userKey(ctx, q, author)
		if err != nil {
			return nil, err
		}
		keyID, sig = uk.ID, uk.Sign(canon)
		if cosigs, err = json.Marshal([]sigs.Cosig{{KeyID: k.ID,
			Sig: base64.RawURLEncoding.EncodeToString(k.Sign(canon))}}); err != nil {
			return nil, err
		}
	}
	var assertions []byte
	if len(a) > 0 {
		if assertions, err = s.marshalAssertions(ctx, q, a); err != nil {
			return nil, err
		}
	}
	if _, err := q.Exec(ctx, `INSERT INTO signed_records (hash, kind, author_id, key_id, body, sig, cosigs, assertions,
		schema_version, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (hash) DO UPDATE SET assertions = COALESCE(signed_records.assertions, '[]'::jsonb) || EXCLUDED.assertions
		WHERE EXCLUDED.assertions IS NOT NULL
		  AND (signed_records.assertions IS NULL OR NOT signed_records.assertions @> EXCLUDED.assertions)`,
		h[:], int16(kind), authorID, keyID, canon, sig, cosigs, assertions, int16(core.RecordSchemaVersion),
		s.d.Clock.Now()); err != nil {
		return nil, err
	}
	seen := map[core.UserID]bool{}
	for _, p := range parties {
		if p == 0 || seen[p] {
			continue
		}
		seen[p] = true
		if _, err := q.Exec(ctx, `INSERT INTO record_parties (user_id, record_hash) VALUES ($1, $2)
			ON CONFLICT DO NOTHING`, int64(p), h[:]); err != nil {
			return nil, err
		}
	}
	return h[:], nil
}

func (s *Service) marshalAssertions(ctx context.Context, q store.Q, as []core.Assertion) ([]byte, error) {
	out := make([]assertionJSON, 0, len(as))
	for _, a := range as {
		j := assertionJSON{CredentialID: a.CredentialID, AuthenticatorData: a.AuthenticatorData,
			ClientDataJSON: a.ClientDataJSON, Signature: a.Signature, Draft: a.Draft}
		for _, c := range a.Bundle {
			j.Bundle = append(j.Bundle, c)
		}
		if a.User != 0 && s.d.Accounts != nil {
			// Read through q: the signer may have been created in this transaction (signup).
			_, ext, _, err := s.d.Accounts.Account(ctx, q, a.User, false)
			if err != nil {
				return nil, err
			}
			j.Signer = &ext
		}
		out = append(out, j)
	}
	return json.Marshal(out)
}

// Draft returns a co-sign draft — the record of body plus the "cosign"
// {nonce, issued_at} member — and its passkey challenge (P1).
func (s *Service) Draft(kind core.RecordKind, body any, nonce uuid.UUID, issuedAt time.Time) ([]byte, [32]byte, error) {
	return coreref.DraftRecord(kind, body, nonce, issuedAt)
}

// SetPurgeAfter sets a record's purge time (§2.3 revoked-edge retention).
func (s *Service) SetPurgeAfter(ctx context.Context, tx store.Q, hash []byte, at time.Time) error {
	tag, err := s.q(tx).Exec(ctx, "UPDATE signed_records SET purge_after = $2 WHERE hash = $1", hash, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return gate.ErrNotFound
	}
	return nil
}

// Delete deletes a record and its parties (a Block record on unblock).
func (s *Service) Delete(ctx context.Context, tx store.Q, hash []byte) error {
	_, err := s.q(tx).Exec(ctx, "DELETE FROM signed_records WHERE hash = $1", hash)
	return err
}
