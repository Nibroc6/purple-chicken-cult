// Package cosign is the single owner of "graph-changing writes need a passkey
// assertion" (ARCHITECTURE §3 Phase 1, §17.4): co-sign intents over JCS drafts
// with a nonce and issued_at, consumed in the caller's transaction. It
// implements core.Cosign and core.CosignDrafters. The per-user custodial keys
// the records are then signed with live in gate/records.
//
// Owner: P1-W01.
//
// The flow: the module that owns a purpose registers a core.Drafter in its
// Init. The client asks POST /api/cosign {purpose, target} for an intent: the
// drafter builds the record body, the intent injects "nonce" (its own random
// id) and "issued_at", and the WebAuthn get options challenge
// sigs.Challenge(kind, draft) with one of the viewer's own credentials. The
// client shows the record, gets the assertion and sends {intent, assertion}
// with the write; the owning module calls Require inside its transaction,
// which consumes the intent (UPDATE … WHERE used_at IS NULL RETURNING), checks
// the body it is about to write against the draft, verifies the assertion
// through Passkeys.VerifyAssertion (own credential, exact origin, UV, counter,
// BE flag) and returns the draft bytes to sign. A replayed assertion, an
// assertion over another draft, an expired or used intent and another user's
// intent are all refused.
package cosign

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
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
)

// Purposes is the closed set of purposes an intent may have.
var Purposes = map[string]bool{
	PurposeFriendRequest: true, PurposeFriendAccept: true, PurposeUnfriend: true, PurposeBlock: true,
	PurposeInviteInviter: true, PurposeInviteInvitee: true, PurposePasskeyAdd: true, PurposePasskeyRemove: true,
	PurposeAccountDelete: true, PurposeRecoveryApprove: true, PurposeModAction: true,
}

// TTL is the lifetime of an intent (§17.4 "fresh").
const TTL = 5 * time.Minute

// JobPurge deletes intents a day after they expired.
const JobPurge = "cosign.purge"

// ceremonyPrefix namespaces the WebAuthn ceremony of an intent from step-up's.
const ceremonyPrefix = "cosign:"

// Service is the cosign module.
type Service struct {
	d *core.Deps

	mu       sync.RWMutex
	drafters map[string]core.Drafter
}

var (
	_ core.Cosign         = (*Service)(nil)
	_ core.CosignDrafters = (*Service)(nil)
)

// New returns the service.
func New(d *core.Deps) *Service { return &Service{d: d, drafters: map[string]core.Drafter{}} }

// Init registers the purge job.
func (s *Service) Init(context.Context) error {
	if s.d.Jobs != nil {
		s.d.Jobs.Handle(JobPurge, func(ctx context.Context, _ json.RawMessage) error {
			_, err := s.Purge(ctx)
			return err
		})
		s.d.Jobs.Every(JobPurge, time.Hour)
	}
	return nil
}

// Purge deletes intents that expired more than a day ago and returns how many.
func (s *Service) Purge(ctx context.Context) (int, error) {
	tag, err := s.d.DB.Q().Exec(ctx, "DELETE FROM cosign_intents WHERE expires_at < $1", s.d.Clock.Now().Add(-24*time.Hour))
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// RegisterDrafter registers the drafter of purpose (core.CosignDrafters).
// Registering an unknown purpose panics: purposes are a closed set.
func (s *Service) RegisterDrafter(purpose string, f core.Drafter) {
	if !Purposes[purpose] {
		panic(fmt.Sprintf("cosign: purpose %q", purpose))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.drafters[purpose] = f
}

func (s *Service) drafter(purpose string) core.Drafter {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.drafters[purpose]
}

// IntentView is what the client shows and signs.
type IntentView struct {
	Intent  gate.Ref        `json:"intent"`
	Record  json.RawMessage `json:"record"`
	Options json.RawMessage `json:"options"`
}

// Begin opens an intent for purpose (POST /api/cosign).
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

// BeginIntent creates an intent of v for purpose over the record its drafter
// builds for target (core.Cosign). An unknown purpose is Problem bad_request; a
// purpose nobody drafts yet and a target the drafter cannot resolve are
// gate.ErrNotFound.
func (s *Service) BeginIntent(ctx context.Context, v core.Viewer, purpose string, target gate.Ref) (core.Intent, error) {
	if v.Kind != core.SessUser || v.ID == 0 {
		return core.Intent{}, gate.ErrNotFound
	}
	if !Purposes[purpose] {
		return core.Intent{}, gate.NewProblem(gate.StatusBadRequest, "bad_request", "purpose")
	}
	f := s.drafter(purpose)
	if f == nil {
		return core.Intent{}, gate.ErrNotFound
	}
	id, err := uuid.NewRandom() // the nonce: server-random, never guessable
	if err != nil {
		return core.Intent{}, err
	}
	issued := sigs.T(s.d.Clock.Now()).Time()
	kind, body, err := f(ctx, v, target, issued)
	if err != nil {
		return core.Intent{}, err
	}
	draft, challenge, err := s.draft(kind, body, id, issued)
	if err != nil {
		return core.Intent{}, err
	}
	if s.d.Passkeys == nil {
		return core.Intent{}, gate.ErrNotFound
	}
	opts, err := s.d.Passkeys.BeginAssertion(ctx, v, challenge[:], ceremonyPrefix+purpose)
	if err != nil {
		return core.Intent{}, err
	}
	if _, err := s.d.DB.Q().Exec(ctx, `INSERT INTO cosign_intents (id, user_id, purpose, record_kind, draft, challenge,
		created_at, expires_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, id, int64(v.ID), purpose, int16(kind), draft,
		challenge[:], issued, issued.Add(TTL)); err != nil {
		return core.Intent{}, err
	}
	return core.Intent{ID: s.d.Refs.Handshake(v.ID, id), Record: draft, Options: opts}, nil
}

func (s *Service) draft(kind core.RecordKind, body any, nonce uuid.UUID, issued time.Time) ([]byte, [32]byte, error) {
	if kind.String() == "" {
		return nil, [32]byte{}, errors.New("cosign: drafter returned an unknown record kind")
	}
	if s.d.Signer != nil {
		return s.d.Signer.Draft(kind, body, nonce, issued)
	}
	return coreref.DraftRecord(kind, body, nonce, issued)
}

// Require consumes v's intent for purpose in tx and verifies assertion over its
// draft (core.Cosign). body is the record the caller is about to write: it must
// match the draft (coreref.DraftMatches). It returns the assertion and the
// exact draft bytes, which the caller signs. An intent that does not resolve,
// belongs to someone else, has another purpose or kind, expired, was used, or
// whose draft differs from body is gate.ErrNotFound; an assertion that does
// not verify is Problem passkey_rejected. The intent is consumed in tx, so a
// rolled-back write leaves it unused, but the WebAuthn ceremony behind it is
// single use whatever happens.
func (s *Service) Require(ctx context.Context, tx store.Q, v core.Viewer, purpose string, kind core.RecordKind, body any, intent gate.Ref, assertion []byte) (core.Assertion, []byte, error) {
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
	var (
		gotPurpose       string
		gotKind          int16
		draft, challenge []byte
	)
	err := q.QueryRow(ctx, `UPDATE cosign_intents SET used_at = $3
		WHERE id = $1 AND user_id = $2 AND used_at IS NULL AND expires_at > $3
		RETURNING purpose, record_kind, draft, challenge`, id, int64(v.ID), s.d.Clock.Now()).
		Scan(&gotPurpose, &gotKind, &draft, &challenge)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.Assertion{}, nil, gate.ErrNotFound
	}
	if err != nil {
		return core.Assertion{}, nil, err
	}
	if gotPurpose != purpose || core.RecordKind(gotKind) != kind { //nolint:gosec // record kinds are small
		return core.Assertion{}, nil, gate.ErrNotFound
	}
	if err := coreref.DraftMatches(draft, kind, body); err != nil {
		return core.Assertion{}, nil, err
	}
	if want := sigs.Challenge(kind.String(), draft); !bytes.Equal(want[:], challenge) {
		return core.Assertion{}, nil, gate.ErrNotFound // a draft row that was tampered with
	}
	if s.d.Passkeys == nil {
		return core.Assertion{}, nil, gate.ErrNotFound
	}
	a, err := s.d.Passkeys.VerifyAssertion(ctx, v, ceremonyPrefix+purpose, assertion)
	if err != nil {
		return core.Assertion{}, nil, err
	}
	// The ceremony already pins the challenge; checking it against this intent
	// too means an assertion can never be attached to another intent's record.
	if subtle.ConstantTimeCompare(sigs.AssertionChallenge(a.ClientDataJSON), challenge) != 1 {
		return core.Assertion{}, nil, gate.NewProblem(gate.StatusBadRequest, "passkey_rejected")
	}
	return a, draft, nil
}
