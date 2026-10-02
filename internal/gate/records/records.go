// Package records owns signed records (ARCHITECTURE §3, §10.2): JCS + Ed25519
// under the server key (Phase 0; P1 adds per-user custodial keys and passkey
// assertions), signing_keys / signed_records / record_parties, key rotation,
// fingerprints, offline export verification and the edge audit. It implements
// core.Signer.
//
// Owner: P0-W11 → P1-W01. Scaffold stub: core.Signer delegates to
// coreref.ServerSigner (real signatures, records kept in memory) with the server
// key from PCC_SERVER_KEY_FILE (auto-created only when cfg.DevLike()).
package records

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"io/fs"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coreref"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/sigs"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// Service is the records module.
type Service struct {
	d   *core.Deps
	key *sigs.Key
	ref *coreref.ServerSigner
}

var _ core.Signer = (*Service)(nil)

// New returns the service.
func New(d *core.Deps) *Service { return &Service{d: d} }

// SetKey installs the server key (tests, wiretest). Must be called before Init.
func (s *Service) SetKey(k *sigs.Key) { s.key = k }

// Init loads (dev/test: creates) the server signing key.
func (s *Service) Init(context.Context) error {
	if s.key == nil {
		k, err := LoadOrCreateServerKey(s.d.Cfg.ServerKeyFile, s.d.Cfg.DevLike())
		if err != nil {
			return err
		}
		s.key = k
	}
	if s.key.From.IsZero() {
		s.key.From = s.d.Clock.Now()
	}
	s.ref = coreref.NewServerSigner(s.key)
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

// ---- handler / CLI API ----

// Keys returns the published server keys with fingerprints.
func (s *Service) Keys(ctx context.Context) ([]gate.KeyView, error) {
	if s.key == nil {
		return nil, gate.ErrNotFound
	}
	kv := gate.KeyView{KeyID: s.key.ID, Alg: "ed25519", PublicKey: base64.RawURLEncoding.EncodeToString(s.key.Pub),
		Fingerprint: sigs.Fingerprint(s.key.Pub), ValidFrom: s.key.From}
	if !s.key.Until.IsZero() {
		u := s.key.Until
		kv.ValidUntil = &u
	}
	return []gate.KeyView{kv}, nil
}

// RotateServerKey creates a new server key and closes the old validity window (CLI).
func (s *Service) RotateServerKey(ctx context.Context) (string, error) {
	return "", gate.ErrNotImplemented
}

// AuditEdges checks every edges row against a valid, unrevoked Friendship record.
func (s *Service) AuditEdges(ctx context.Context) (int, error) { return 0, gate.ErrNotImplemented }

// VerifyExport verifies an export zip offline, optionally pinning keys to
// published fingerprints (key_id → hex sha256).
func VerifyExport(r io.ReaderAt, size int64, pinned map[string]string) (gate.VerifyReport, error) {
	return gate.VerifyReport{KeysPinned: len(pinned) > 0}, gate.ErrNotImplemented
}

// ---- core.Signer (reference delegation) ----

// Sign signs and stores a record.
func (s *Service) Sign(ctx context.Context, tx store.Q, kind core.RecordKind, author core.UserID, body any, parties []core.UserID, a ...core.Assertion) ([]byte, error) {
	return s.ref.Sign(ctx, tx, kind, author, body, parties, a...)
}

// Draft returns a co-sign draft and its passkey challenge (P1).
func (s *Service) Draft(kind core.RecordKind, body any, nonce uuid.UUID, issuedAt time.Time) ([]byte, [32]byte, error) {
	return s.ref.Draft(kind, body, nonce, issuedAt)
}

// SetPurgeAfter sets a record's purge time.
func (s *Service) SetPurgeAfter(ctx context.Context, tx store.Q, hash []byte, at time.Time) error {
	return s.ref.SetPurgeAfter(ctx, tx, hash, at)
}

// Delete deletes a record.
func (s *Service) Delete(ctx context.Context, tx store.Q, hash []byte) error {
	return s.ref.Delete(ctx, tx, hash)
}
