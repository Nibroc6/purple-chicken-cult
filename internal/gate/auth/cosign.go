package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coreref"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/sigs"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// Co-signed passkey changes (ARCHITECTURE §17.4, plan P1-W07, hand-off
// p1-w01-2). A passkey change is an account change: from Phase 1 its
// PasskeyChange record carries an assertion of one of the account's EXISTING
// passkeys over the exact record, so an export's credential chain verifies
// offline — every key after the first was vouched for by a key the account
// already had (security #5). Enrollment (genesis, recovery) and join add the
// first credential of a chain and stay unsigned.
//
//   - passkey_add: AddPasskeyFinish verifies the new credential's
//     registration and parks it on the session (sessions.pending_passkey, 5
//     minutes); the client opens an intent (POST /api/cosign {purpose:
//     passkey_add}), whose builder drafts PasskeyChange{op: add} of the parked
//     credential, signs it with an existing passkey and finishes with
//     {intent, assertion}. The credential is stored only then.
//   - passkey_remove: the intent's target is the passkey's ref; the removal
//     sends {intent, assertion}.
//
// Like friends (p1-w03-1), a session created by a passkey ceremony
// (Viewer.Credential set — every production user session) must co-sign: a
// change without a valid co-signature is the uniform not-found. The co-sign
// assertion is itself a fresh user-verified passkey check, so the co-signed
// steps need no separate step-up. Credential-less sessions exist only through
// the test hooks and keep the Phase 0 path (fresh step-up, server-signed).

// Co-sign purposes registered by this module.
const (
	PurposePasskeyAdd    = "passkey_add"
	PurposePasskeyRemove = "passkey_remove"
)

// PendingTTL bounds a verified credential waiting for its co-signature.
const PendingTTL = 5 * time.Minute

// pendingPasskey is sessions.pending_passkey.
type pendingPasskey struct {
	Cred core.Credential `json:"cred"`
	Name string          `json:"name"`
	Exp  time.Time       `json:"exp"`
}

// mustCosign reports whether v's passkey changes must be co-signed.
func mustCosign(v core.Viewer) bool { return len(v.Credential) > 0 }

func (s *Service) registerPurposes() {
	if s.d.CosignPurposes == nil {
		return
	}
	s.d.CosignPurposes.RegisterPurpose(PurposePasskeyAdd, core.RecPasskeyChange, s.addDraft)
	s.d.CosignPurposes.RegisterPurpose(PurposePasskeyRemove, core.RecPasskeyChange, s.removeDraft)
}

// parked reads the session's parked credential (live only); take clears it
// atomically as it reads it (single use, whatever the outcome).
func (s *Service) parked(ctx context.Context, q store.Q, sess [32]byte, take bool) (pendingPasskey, bool) {
	sql := "SELECT pending_passkey FROM sessions WHERE id_hash = $1 AND pending_passkey IS NOT NULL"
	if take {
		sql = `WITH old AS (SELECT id_hash, pending_passkey FROM sessions WHERE id_hash = $1 AND pending_passkey IS NOT NULL FOR UPDATE)
			UPDATE sessions s SET pending_passkey = NULL FROM old WHERE s.id_hash = old.id_hash RETURNING old.pending_passkey`
	}
	var raw []byte
	if err := q.QueryRow(ctx, sql, sess[:]).Scan(&raw); err != nil {
		return pendingPasskey{}, false
	}
	var p pendingPasskey
	if json.Unmarshal(raw, &p) != nil || !s.d.Clock.Now().Before(p.Exp) || len(p.Cred.ID) == 0 {
		return pendingPasskey{}, false
	}
	return p, true
}

func (s *Service) changeBody(ctx context.Context, q store.Q, user core.UserID, op string, id, pub []byte, alg int, at time.Time) (passkeyChangeBody, error) {
	_, ext, _, err := s.d.Accounts.Account(ctx, q, user, false)
	if err != nil {
		return passkeyChangeBody{}, err
	}
	return passkeyChangeBody{User: ext, Op: op, CredentialID: id, PublicKey: pub, Alg: alg, At: sigs.T(at)}, nil
}

// addDraft drafts PasskeyChange{op: add} of the credential parked on v's session.
func (s *Service) addDraft(ctx context.Context, v core.Viewer, _ gate.Ref) (any, error) {
	p, ok := s.parked(ctx, s.d.DB.Q(), v.Session, false)
	if !ok {
		return nil, gate.ErrNotFound
	}
	return s.changeBody(ctx, s.d.DB.Q(), v.ID, opAdd, p.Cred.ID, p.Cred.PublicKey, p.Cred.Alg, s.d.Clock.Now())
}

// removeDraft drafts PasskeyChange{op: remove} of one of v's passkeys.
func (s *Service) removeDraft(ctx context.Context, v core.Viewer, target gate.Ref) (any, error) {
	id, ok := s.d.Refs.OpenCred(v.ID, target)
	if !ok {
		return nil, gate.ErrNotFound
	}
	creds, err := s.credentials(ctx, s.d.DB.Q(), v.ID, false)
	if err != nil {
		return nil, err
	}
	for _, c := range creds {
		if bytes.Equal(c.id, id) {
			return s.changeBody(ctx, s.d.DB.Q(), v.ID, opRemove, c.id, c.publicKey, c.alg, s.d.Clock.Now())
		}
	}
	return nil, gate.ErrNotFound
}

// signedChange is a verified co-signature of a PasskeyChange.
type signedChange struct {
	a    core.Assertion
	body passkeyChangeBody
}

// changeRecord is the part of a signed PasskeyChange record that is checked.
type changeRecord struct {
	User         uuid.UUID  `json:"user"`
	Op           string     `json:"op"`
	CredentialID sigs.Bytes `json:"credential_id"`
	PublicKey    sigs.Bytes `json:"public_key"`
	Alg          int        `json:"alg"`
}

// cosigned consumes v's intent for purpose in tx and checks that the drafted
// record is the change of credential id (op) on v's account.
func (s *Service) cosigned(ctx context.Context, tx store.Q, v core.Viewer, purpose, op string, id []byte, intent gate.Ref, assertion []byte) (signedChange, error) {
	if s.d.Cosign == nil || intent == "" {
		return signedChange{}, gate.ErrNotFound
	}
	a, draft, err := s.d.Cosign.Require(ctx, tx, v, purpose, core.RecPasskeyChange, nil, intent, assertion)
	if err != nil {
		return signedChange{}, err
	}
	rec, _, _, err := coreref.RecordFromDraft(draft)
	if err != nil {
		return signedChange{}, gate.ErrNotFound
	}
	var cr changeRecord
	if json.Unmarshal(rec, &cr) != nil {
		return signedChange{}, gate.ErrNotFound
	}
	_, ext, _, err := s.d.Accounts.Account(ctx, tx, v.ID, false)
	if err != nil {
		return signedChange{}, err
	}
	if cr.User != ext || cr.Op != op || !bytes.Equal(cr.CredentialID, id) {
		return signedChange{}, gate.ErrNotFound
	}
	return signedChange{a: a, body: passkeyChangeBody{User: cr.User, Op: cr.Op, CredentialID: cr.CredentialID,
		PublicKey: cr.PublicKey, Alg: cr.Alg}}, nil
}

// park stores a verified new credential on the session until it is co-signed.
func (s *Service) park(ctx context.Context, sess [32]byte, c core.Credential, name string) error {
	raw, err := json.Marshal(pendingPasskey{Cred: c, Name: name, Exp: s.d.Clock.Now().Add(PendingTTL)})
	if err != nil {
		return err
	}
	tag, err := s.d.DB.Q().Exec(ctx, "UPDATE sessions SET pending_passkey = $2 WHERE id_hash = $1", sess[:], raw)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return gate.ErrNotFound
	}
	return nil
}

// AddPasskeyResult says whether the new credential awaits its co-signature.
type AddPasskeyResult struct {
	Cosign string `json:"cosign,omitempty"` // the purpose to sign ("passkey_add"), when it does
}

// finishCosignedAdd stores the parked credential with its co-signed
// PasskeyChange record (the second step of a co-signed add).
func (s *Service) finishCosignedAdd(ctx context.Context, v core.Viewer, intent gate.Ref, assertion []byte) error {
	// Taken before the transaction: the co-sign verification updates this
	// session's ceremony from another connection.
	p, ok := s.parked(ctx, s.d.DB.Q(), v.Session, true)
	if !ok {
		return gate.ErrNotFound
	}
	return s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		sc, err := s.cosigned(ctx, tx, v, PurposePasskeyAdd, opAdd, p.Cred.ID, intent, assertion)
		if err != nil {
			return err
		}
		if !bytes.Equal(sc.body.PublicKey, p.Cred.PublicKey) || sc.body.Alg != p.Cred.Alg {
			return gate.ErrNotFound
		}
		if err := s.insertCredential(ctx, tx, v.ID, p.Cred, p.Name); err != nil {
			if errors.Is(err, gate.ErrNotFound) {
				return rejected()
			}
			return err
		}
		if _, err := s.d.Signer.Sign(ctx, tx, core.RecPasskeyChange, v.ID, nil, []core.UserID{v.ID}, sc.a); err != nil {
			return err
		}
		return s.notifyFriends(ctx, tx, v.ID, reasonPasskeyAdded)
	})
}
