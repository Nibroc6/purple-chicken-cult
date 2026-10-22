// Package cosign is the single owner of "graph-changing writes need a passkey
// assertion" (ARCHITECTURE §3 Phase 1, §17.4; buildability C4). It implements
// core.Cosign, core.CosignPurposes and core.SignupCosign.
//
// Owner: P1-W01.
//
// An intent is a co-sign request of one user for one purpose. At Begin the
// purpose's builder (registered by the module that later calls Require, see
// core.CosignPurposes) produces the record body; the intent stores its exact
// draft — the record plus a "cosign" {nonce = intent id, issued_at} member
// (core.Signer.Draft) — and the WebAuthn challenge
// sigs.Challenge(kind, draft), so a challenge never repeats and an assertion
// signs exactly the record shown to the user (Intent.Record). Require, inside
// the caller's transaction, consumes the intent atomically
// (UPDATE … WHERE used_at IS NULL RETURNING), checks that the caller's body is
// the drafted record, verifies the assertion through
// core.Passkeys.VerifyAssertion (the viewer's own credential, exact origin, no
// crossOrigin/topOrigin, UP+UV, rpIdHash, BE flag, counter) and that it
// signed this intent's challenge. The returned assertion carries the draft, so
// core.Signer stores it with the record and exports verify offline.
//
// The invitee signup ceremony (fidelity #21, signup.go) is the one co-sign that
// runs in a guest session with a credential that is not yet bound to a
// committed user.
package cosign

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coreref"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/config"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/sigs"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// Purposes of co-sign intents.
const (
	PurposeFriendRequest   = "friend_request"
	PurposeFriendAccept    = "friend_accept"
	PurposeUnfriend        = "unfriend"
	PurposeBlock           = "block"
	PurposeInviteInviter   = "invite_inviter"
	PurposeInviteInvitee   = "invite_invitee"
	PurposePasskeyAdd      = "passkey_add"
	PurposePasskeyRemove   = "passkey_remove"
	PurposeAccountDelete   = "account_delete"
	PurposeRecoveryApprove = "recovery_approve"
	PurposeModAction       = "mod_action"
	// PurposeCheck is registered only when PCC_ENV=test: a draft nobody
	// stores, so end-to-end tests can drive the co-sign dialog.
	PurposeCheck = "cosign_check"
)

// TTL is the lifetime of an intent and of a signup ceremony.
const TTL = 5 * time.Minute

// ceremonyPurpose is the Passkeys ceremony purpose of every co-sign assertion.
const ceremonyPurpose = "cosign"

// JobPurge drops expired intents and signup ceremonies.
const JobPurge = "cosign.purge"

// PurgeEvery is the period of JobPurge; rows are kept a day past expiry.
const (
	PurgeEvery = time.Hour
	keepFor    = 24 * time.Hour
)

// Service is the cosign module.
type Service struct {
	d *core.Deps

	mu       sync.RWMutex
	purposes map[string]purpose
}

type purpose struct {
	kind  core.RecordKind
	build core.CosignBuilder
}

var (
	_ core.Cosign         = (*Service)(nil)
	_ core.CosignPurposes = (*Service)(nil)
	_ core.SignupCosign   = (*Service)(nil)
)

// New returns the service.
func New(d *core.Deps) *Service { return &Service{d: d, purposes: map[string]purpose{}} }

// Init registers the purge job (and, in test, PurposeCheck).
func (s *Service) Init(context.Context) error {
	if s.d.Cfg != nil && s.d.Cfg.Env == config.EnvTest {
		s.RegisterPurpose(PurposeCheck, core.RecPasskeyChange, s.checkBody)
	}
	if s.d.Jobs != nil {
		s.d.Jobs.Handle(JobPurge, func(ctx context.Context, _ json.RawMessage) error {
			_, err := s.Purge(ctx)
			return err
		})
		s.d.Jobs.Every(JobPurge, PurgeEvery)
	}
	return nil
}

// checkBody is the PurposeCheck record: {user, op: "check"} (never stored).
func (s *Service) checkBody(ctx context.Context, v core.Viewer, _ gate.Ref) (any, error) {
	m, err := s.d.Users.Get(ctx, v.ID)
	if err != nil {
		return nil, gate.ErrNotFound
	}
	return map[string]any{"user": m.ExtID, "op": "check"}, nil
}

// Purge deletes intents and signup ceremonies that expired more than a day ago.
func (s *Service) Purge(ctx context.Context) (int, error) {
	before := s.d.Clock.Now().Add(-keepFor)
	a, err := s.d.DB.Q().Exec(ctx, "DELETE FROM cosign_intents WHERE expires_at < $1", before)
	if err != nil {
		return 0, err
	}
	b, err := s.d.DB.Q().Exec(ctx, "DELETE FROM cosign_signups WHERE expires_at < $1", before)
	if err != nil {
		return 0, err
	}
	return int(a.RowsAffected() + b.RowsAffected()), nil
}

// RegisterPurpose registers how intents of purpose are drafted
// (core.CosignPurposes). Registering a purpose twice replaces its builder.
func (s *Service) RegisterPurpose(name string, kind core.RecordKind, build core.CosignBuilder) {
	if name == "" || kind.String() == "" || build == nil {
		panic(errors.New("cosign: invalid purpose registration"))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purposes[name] = purpose{kind: kind, build: build}
}

func (s *Service) purpose(name string) (purpose, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.purposes[name]
	return p, ok
}

// IntentView is what the client shows and signs (POST /api/cosign).
type IntentView struct {
	Intent  gate.Ref        `json:"intent"`
	Record  json.RawMessage `json:"record"`
	Options json.RawMessage `json:"options"`
}

// Begin opens an intent of the signed-in viewer for purpose (POST /api/cosign).
// An unknown purpose, an unresolvable target or a viewer without passkeys is
// the uniform not-found.
func (s *Service) Begin(ctx context.Context, purpose string, target gate.Ref) (IntentView, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return IntentView{}, err
	}
	in, err := s.BeginIntent(ctx, v, purpose, target)
	if err != nil {
		return IntentView{}, err
	}
	return IntentView{Intent: in.ID, Record: in.Record, Options: in.Options}, nil
}

func (s *Service) newID() uuid.UUID {
	if s.d.IDs != nil {
		if id := s.d.IDs.New(); id != uuid.Nil {
			return id
		}
	}
	return uuid.Must(uuid.NewV7())
}

// BeginIntent creates an intent of v for purpose (core.Cosign): the record is
// drafted by the purpose's builder and the passkey ceremony is started in v's
// session with the draft's challenge.
func (s *Service) BeginIntent(ctx context.Context, v core.Viewer, name string, target gate.Ref) (core.Intent, error) {
	if v.Kind != core.SessUser || v.ID == 0 {
		return core.Intent{}, gate.ErrNotFound
	}
	p, ok := s.purpose(name)
	if !ok {
		return core.Intent{}, gate.ErrNotFound
	}
	body, err := p.build(ctx, v, target)
	if err != nil {
		return core.Intent{}, err
	}
	id := s.newID()
	now := s.d.Clock.Now()
	draft, ch, err := s.d.Signer.Draft(p.kind, body, id, now)
	if err != nil {
		return core.Intent{}, err
	}
	opts, err := s.d.Passkeys.BeginAssertion(ctx, v, ch[:], ceremonyPurpose)
	if err != nil {
		return core.Intent{}, err
	}
	if _, err := s.d.DB.Q().Exec(ctx, `INSERT INTO cosign_intents (id, user_id, purpose, record_kind, draft, challenge,
		created_at, expires_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		id, int64(v.ID), name, int16(p.kind), draft, ch[:], now, now.Add(TTL)); err != nil {
		return core.Intent{}, err
	}
	return core.Intent{ID: s.d.Refs.Handshake(v.ID, id), Record: json.RawMessage(draft), Options: opts}, nil
}

// Require consumes v's intent for purpose in tx and verifies assertion for it
// (core.Cosign). body must canonicalize to the drafted record; nil binds to the
// stored draft. Unknown, foreign, expired, used or mismatched intents are the
// uniform not-found; a rejected assertion is the Passkeys error (Problem
// passkey_rejected) or not-found when it signed another challenge. On any
// error the caller's transaction must roll back.
func (s *Service) Require(ctx context.Context, tx store.Q, v core.Viewer, name string, kind core.RecordKind, body any, intent gate.Ref, assertion []byte) (core.Assertion, []byte, error) {
	if v.Kind != core.SessUser || v.ID == 0 {
		return core.Assertion{}, nil, gate.ErrNotFound
	}
	id, ok := s.d.Refs.OpenHandshake(v.ID, intent)
	if !ok {
		return core.Assertion{}, nil, gate.ErrNotFound
	}
	q := tx
	if q == nil {
		q = s.d.DB.Q()
	}
	var draft, challenge []byte
	err := q.QueryRow(ctx, `UPDATE cosign_intents SET used_at = $5
		WHERE id = $1 AND user_id = $2 AND purpose = $3 AND record_kind = $4 AND used_at IS NULL AND expires_at > $5
		RETURNING draft, challenge`, id, int64(v.ID), name, int16(kind), s.d.Clock.Now()).Scan(&draft, &challenge)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.Assertion{}, nil, gate.ErrNotFound
	}
	if err != nil {
		return core.Assertion{}, nil, err
	}
	if body != nil {
		rec, _, _, err := coreref.RecordFromDraft(draft)
		if err != nil {
			return core.Assertion{}, nil, err
		}
		canon, err := sigs.CanonicalRecord(kind.String(), core.RecordSchemaVersion, body)
		if err != nil || !bytes.Equal(rec, canon) {
			return core.Assertion{}, nil, gate.ErrNotFound
		}
	}
	a, err := s.d.Passkeys.VerifyAssertion(ctx, v, ceremonyPurpose, assertion)
	if err != nil {
		return core.Assertion{}, nil, err
	}
	// The session holds one ceremony: an assertion over a later intent's
	// challenge must not consume this one.
	if !challengeIs(a.ClientDataJSON, challenge) {
		return core.Assertion{}, nil, gate.ErrNotFound
	}
	a.User, a.Draft = v.ID, draft
	return a, draft, nil
}

// challengeIs reports whether clientDataJSON carries challenge.
func challengeIs(clientDataJSON, challenge []byte) bool {
	var cd struct {
		Challenge string `json:"challenge"`
	}
	if json.Unmarshal(clientDataJSON, &cd) != nil {
		return false
	}
	got, err := base64.RawURLEncoding.DecodeString(cd.Challenge)
	return err == nil && len(challenge) > 0 && bytes.Equal(got, challenge)
}
