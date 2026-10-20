package records

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/sigs"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// Per-user custodial keys (ARCHITECTURE §3 Phase 1, P1-W01, security #29).
//
// Every user who authors a record gets one Ed25519 key, created in the
// transaction of their first authored record (signup writes the first one: the
// join's Invite or the genesis PasskeyChange). The seed is stored in
// signing_keys.private_key_enc as nonce ‖ AES-256-GCM(KEK, seed) with the
// associated data key_id ‖ owner_id, so a wrapped key copied into another
// user's row (or under another key ID) does not unwrap. The KEK is PCC_KEK; dev
// and test without one derive it from the master key, prod refuses to start
// without it (config).

// UserKeyPrefix starts every user key ID: "usr-" ‖ uuidv7.
const UserKeyPrefix = "usr-"

// devKEKLabel derives the KEK in dev/test when PCC_KEK is unset.
const devKEKLabel = "pcc/dev-kek/v1"

var errNoKEK = errors.New("records: no KEK for per-user keys")

// kek returns the key-encryption key, or nil when per-user keys are off.
func (s *Service) kek() []byte {
	if s.d == nil || s.d.Cfg == nil {
		return nil
	}
	if s.d.Cfg.KEK != nil {
		return s.d.Cfg.KEK
	}
	if s.d.Cfg.DevLike() && len(s.d.Cfg.MasterKey) > 0 {
		return s.d.Cfg.Key(devKEKLabel, 32)
	}
	return nil
}

// UserKeysEnabled reports whether authored records are signed with per-user keys.
func (s *Service) UserKeysEnabled() bool { return s.kek() != nil }

func keyAAD(keyID string, owner core.UserID) []byte {
	aad := make([]byte, 0, len(keyID)+1+8)
	aad = append(aad, keyID...)
	aad = append(aad, 0)
	return binary.BigEndian.AppendUint64(aad, uint64(owner))
}

// wrapSeed seals seed under kek for (keyID, owner).
func wrapSeed(kek, seed []byte, keyID string, owner core.UserID) ([]byte, error) {
	gcm, err := newGCM(kek)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, seed, keyAAD(keyID, owner)), nil
}

// unwrapSeed opens a wrapped seed; it fails for any other (keyID, owner).
func unwrapSeed(kek, wrapped []byte, keyID string, owner core.UserID) ([]byte, error) {
	gcm, err := newGCM(kek)
	if err != nil {
		return nil, err
	}
	if len(wrapped) < gcm.NonceSize() {
		return nil, errors.New("records: wrapped key too short")
	}
	seed, err := gcm.Open(nil, wrapped[:gcm.NonceSize()], wrapped[gcm.NonceSize():], keyAAD(keyID, owner))
	if err != nil {
		return nil, errors.New("records: wrapped key does not open for this key and owner")
	}
	return seed, nil
}

func newGCM(kek []byte) (cipher.AEAD, error) {
	if len(kek) != 32 {
		return nil, errNoKEK
	}
	b, err := aes.NewCipher(kek)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}

// userKey returns owner's current key inside q, creating it on first use.
// Concurrent first uses serialize on the owner's users row.
func (s *Service) userKey(ctx context.Context, q store.Q, owner core.UserID) (*sigs.Key, error) {
	kek := s.kek()
	if kek == nil {
		return nil, errNoKEK
	}
	k, err := s.loadUserKey(ctx, q, kek, owner)
	if err == nil || !errors.Is(err, pgx.ErrNoRows) {
		return k, err
	}
	if _, err := q.Exec(ctx, "SELECT 1 FROM users WHERE id = $1 FOR UPDATE", int64(owner)); err != nil {
		return nil, err
	}
	if k, err := s.loadUserKey(ctx, q, kek, owner); !errors.Is(err, pgx.ErrNoRows) {
		return k, err
	}
	return s.createUserKey(ctx, q, kek, owner, s.d.Clock.Now())
}

func (s *Service) loadUserKey(ctx context.Context, q store.Q, kek []byte, owner core.UserID) (*sigs.Key, error) {
	var (
		keyID   string
		wrapped []byte
		from    time.Time
	)
	err := q.QueryRow(ctx, `SELECT key_id, private_key_enc, valid_from FROM signing_keys
		WHERE owner_id = $1 AND valid_until IS NULL AND private_key_enc IS NOT NULL
		ORDER BY valid_from DESC, key_id DESC LIMIT 1`, int64(owner)).Scan(&keyID, &wrapped, &from)
	if err != nil {
		return nil, err
	}
	seed, err := unwrapSeed(kek, wrapped, keyID, owner)
	if err != nil {
		return nil, err
	}
	priv := ed25519.NewKeyFromSeed(seed)
	return &sigs.Key{ID: keyID, Priv: priv, Pub: priv.Public().(ed25519.PublicKey), From: from}, nil
}

func (s *Service) createUserKey(ctx context.Context, q store.Q, kek []byte, owner core.UserID, now time.Time) (*sigs.Key, error) {
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		return nil, err
	}
	keyID := UserKeyPrefix + uuid.Must(uuid.NewV7()).String()
	wrapped, err := wrapSeed(kek, seed, keyID, owner)
	if err != nil {
		return nil, err
	}
	priv := ed25519.NewKeyFromSeed(seed)
	pub := priv.Public().(ed25519.PublicKey)
	if _, err := q.Exec(ctx, `INSERT INTO signing_keys (key_id, owner_id, alg, public_key, private_key_enc, valid_from)
		VALUES ($1, $2, 'ed25519', $3, $4, $5)`, keyID, int64(owner), []byte(pub), wrapped, now); err != nil {
		return nil, err
	}
	return &sigs.Key{ID: keyID, Priv: priv, Pub: pub, From: now}, nil
}

// EnsureUserKey creates owner's custodial key if it has none yet and returns
// its key ID (signup; also used by tests).
func (s *Service) EnsureUserKey(ctx context.Context, tx store.Q, owner core.UserID) (string, error) {
	k, err := s.userKey(ctx, s.q(tx), owner)
	if err != nil {
		return "", err
	}
	return k.ID, nil
}

// retireUserKeys closes the validity window of owner's keys and destroys the
// wrapped private halves (account deletion): published public keys stay so old
// exports keep verifying, but nothing can be signed as owner any more.
func (s *Service) retireUserKeys(ctx context.Context, q store.Q, owner core.UserID) error {
	_, err := q.Exec(ctx, `UPDATE signing_keys SET private_key_enc = NULL, valid_until = COALESCE(valid_until, $2)
		WHERE owner_id = $1`, int64(owner), s.d.Clock.Now())
	return err
}

// CosignRequiredFrom returns when co-signing became mandatory (nil: not yet).
func (s *Service) CosignRequiredFrom(ctx context.Context, q store.Q) (*time.Time, error) {
	var t *time.Time
	err := s.q(q).QueryRow(ctx, "SELECT cosign_required_from FROM records_policy WHERE id = 1").Scan(&t)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if t != nil {
		u := t.UTC()
		t = &u
	}
	return t, err
}

// SetCosignRequiredFrom makes co-signing mandatory from at (nil: not
// mandatory). INT-1 sets it once every high-value write co-signs.
func (s *Service) SetCosignRequiredFrom(ctx context.Context, at *time.Time) error {
	_, err := s.d.DB.Q().Exec(ctx, `INSERT INTO records_policy (id, cosign_required_from) VALUES (1, $1)
		ON CONFLICT (id) DO UPDATE SET cosign_required_from = EXCLUDED.cosign_required_from`, at)
	return err
}
