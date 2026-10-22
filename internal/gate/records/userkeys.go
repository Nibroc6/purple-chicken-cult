package records

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/sigs"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// Per-user custodial Ed25519 keys (ARCHITECTURE §3 Phase 1, security #29).
//
// Each user has one current key in signing_keys (owner_id set). Its 32-byte
// seed is stored wrapped: private_key_enc = nonce(12) ‖ AES-256-GCM(KEK, seed)
// with additional data key_id ‖ 0x00 ‖ owner_id (8 bytes big-endian), so a
// wrapped key copied to another user's row, or under another key ID, fails to
// unwrap. The key is created the first time the user authors a record — at
// signup that is the PasskeyChange of the first credential, inside the
// RegisterFinish transaction. A user key ID is self-certifying:
// "usr-" ‖ the first 16 hex chars of sha256(public key).

// userKeyPrefix starts every user key ID.
const userKeyPrefix = "usr-"

// UserKeyID derives the key ID of a user key from its public key.
func UserKeyID(pub ed25519.PublicKey) string {
	h := sha256.Sum256(pub)
	return userKeyPrefix + hex.EncodeToString(h[:8])
}

// IsUserKeyID reports whether id names a user key.
func IsUserKeyID(id string) bool { return strings.HasPrefix(id, userKeyPrefix) }

// errUnwrap is a wrapped key that does not open under the KEK for its row.
var errUnwrap = errors.New("records: user key does not unwrap for its owner")

// devKEKInfo derives the dev/test KEK from the master key (PCC_KEK is optional
// there; prod refuses to start without it).
const devKEKInfo = "pcc-dev-kek-v1"

// kekFor returns the key-encryption key: PCC_KEK, or in dev/test a key derived
// from the master key. Outside dev/test a missing KEK is an error (D20).
func (s *Service) kekFor() ([]byte, error) {
	cfg := s.d.Cfg
	if len(cfg.KEK) == 32 {
		return cfg.KEK, nil
	}
	if cfg.KEK != nil {
		return nil, errors.New("records: PCC_KEK must be 32 bytes")
	}
	if !cfg.DevLike() || len(cfg.MasterKey) == 0 {
		return nil, errors.New("records: PCC_KEK is required outside dev/test")
	}
	return hkdf.Key(sha256.New, cfg.MasterKey, nil, devKEKInfo, 32)
}

func wrapAAD(keyID string, owner core.UserID) []byte {
	aad := make([]byte, 0, len(keyID)+9)
	aad = append(aad, keyID...)
	aad = append(aad, 0)
	return binary.BigEndian.AppendUint64(aad, uint64(owner))
}

// WrapSeed seals seed under kek for (keyID, owner).
func WrapSeed(kek []byte, keyID string, owner core.UserID, seed []byte) ([]byte, error) {
	gcm, err := newGCM(kek)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, seed, wrapAAD(keyID, owner)), nil
}

// UnwrapSeed opens a wrapped seed for (keyID, owner).
func UnwrapSeed(kek []byte, keyID string, owner core.UserID, wrapped []byte) ([]byte, error) {
	gcm, err := newGCM(kek)
	if err != nil {
		return nil, err
	}
	if len(wrapped) < gcm.NonceSize()+gcm.Overhead() {
		return nil, errUnwrap
	}
	seed, err := gcm.Open(nil, wrapped[:gcm.NonceSize()], wrapped[gcm.NonceSize():], wrapAAD(keyID, owner))
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errUnwrap
	}
	return seed, nil
}

func newGCM(kek []byte) (cipher.AEAD, error) {
	if len(kek) != 32 {
		return nil, errors.New("records: KEK must be 32 bytes")
	}
	b, err := aes.NewCipher(kek)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}

// userKey returns owner's current custodial key, creating it inside q when the
// user has none. The private half is unwrapped for this call only.
func (s *Service) userKey(ctx context.Context, q store.Q, owner core.UserID) (*sigs.Key, error) {
	kek, err := s.kekFor()
	if err != nil {
		return nil, err
	}
	for range 2 {
		var (
			keyID   string
			wrapped []byte
			from    time.Time
		)
		err := q.QueryRow(ctx, `SELECT key_id, private_key_enc, valid_from FROM signing_keys
			WHERE owner_id = $1 AND valid_until IS NULL`, int64(owner)).Scan(&keyID, &wrapped, &from)
		switch {
		case err == nil:
			seed, err := UnwrapSeed(kek, keyID, owner, wrapped)
			if err != nil {
				return nil, err
			}
			k, err := sigs.KeyFromSeed(seed)
			if err != nil {
				return nil, err
			}
			if UserKeyID(k.Pub) != keyID {
				return nil, errUnwrap
			}
			k.ID, k.From = keyID, from
			return k, nil
		case errors.Is(err, pgx.ErrNoRows):
			if err := s.createUserKey(ctx, q, owner, kek); err != nil {
				return nil, err
			}
		default:
			return nil, err
		}
	}
	return nil, errors.New("records: user key creation raced")
}

func (s *Service) createUserKey(ctx context.Context, q store.Q, owner core.UserID, kek []byte) error {
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		return err
	}
	k, err := sigs.KeyFromSeed(seed)
	if err != nil {
		return err
	}
	id := UserKeyID(k.Pub)
	wrapped, err := WrapSeed(kek, id, owner, seed)
	if err != nil {
		return err
	}
	// The partial unique index keeps one current key per owner; a concurrent
	// creator's row wins and is read back by the caller.
	_, err = q.Exec(ctx, `INSERT INTO signing_keys (key_id, owner_id, alg, public_key, private_key_enc, valid_from)
		VALUES ($1, $2, 'ed25519', $3, $4, $5) ON CONFLICT DO NOTHING`,
		id, int64(owner), []byte(k.Pub), wrapped, s.d.Clock.Now())
	return err
}

// EnsureUserKey creates owner's custodial key in tx if it has none (signup).
func (s *Service) EnsureUserKey(ctx context.Context, tx store.Q, owner core.UserID) error {
	_, err := s.userKey(ctx, s.q(tx), owner)
	return err
}

// UserKeyView is a user's public key in keys.json (and GET /api/keys "mine").
type UserKeyView struct {
	gate.KeyView
	Owner uuid.UUID `json:"owner"` // the user's ext_id
}

func scanUserKeys(rows pgx.Rows) ([]UserKeyView, error) {
	defer rows.Close()
	out := []UserKeyView{}
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

// MyKeys returns the signed-in user's own custodial public keys (GET /api/keys
// "mine", P1).
func (s *Service) MyKeys(ctx context.Context) ([]gate.KeyView, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.d.DB.Q().Query(ctx, `SELECT k.key_id, k.alg, k.public_key, k.valid_from, k.valid_until, u.ext_id
		FROM signing_keys k JOIN users u ON u.id = k.owner_id WHERE k.owner_id = $1 ORDER BY k.valid_from, k.key_id`,
		int64(v.ID))
	if err != nil {
		return nil, err
	}
	ks, err := scanUserKeys(rows)
	if err != nil {
		return nil, err
	}
	out := make([]gate.KeyView, 0, len(ks))
	for _, k := range ks {
		out = append(out, k.KeyView)
	}
	return out, nil
}

// checkKEK is the startup check: a KEK must be usable (D20).
func (s *Service) checkKEK() error {
	_, err := s.kekFor()
	return err
}
