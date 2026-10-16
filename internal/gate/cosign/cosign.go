// Package cosign is the single owner of "graph-changing writes need a passkey
// assertion" (ARCHITECTURE §3 Phase 1, §17.4): co-sign intents over JCS drafts
// with a nonce and issued_at, consumed in the caller's transaction, plus per-user
// custodial Ed25519 keys wrapped under PCC_KEK. It implements core.Cosign.
//
// Owner: P1-W01. Scaffold stub: delegates to coreref.MemCosign.
package cosign

import (
	"context"
	"encoding/json"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coreref"
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

// Service is the cosign module.
type Service struct {
	d   *core.Deps
	ref *coreref.MemCosign
}

var (
	_ core.Cosign         = (*Service)(nil)
	_ core.CosignPurposes = (*Service)(nil)
	_ core.SignupCosign   = (*Service)(nil)
)

// New returns the service.
func New(d *core.Deps) *Service { return &Service{d: d, ref: &coreref.MemCosign{D: d}} }

// Init does nothing yet.
func (s *Service) Init(context.Context) error { return nil }

// IntentView is what the client shows and signs.
type IntentView struct {
	Intent  gate.Ref        `json:"intent"`
	Record  json.RawMessage `json:"record"`
	Options json.RawMessage `json:"options"`
}

// Begin opens an intent for purpose (POST /api/cosign).
func (s *Service) Begin(ctx context.Context, purpose string, target gate.Ref) (IntentView, error) {
	if _, err := core.RequireUser(ctx); err != nil {
		return IntentView{}, err
	}
	return IntentView{}, gate.ErrNotImplemented
}

// BeginIntent creates an intent (core.Cosign).
func (s *Service) BeginIntent(ctx context.Context, v core.Viewer, purpose string, target gate.Ref) (core.Intent, error) {
	return s.ref.BeginIntent(ctx, v, purpose, target)
}

// Require verifies and consumes an intent (core.Cosign).
func (s *Service) Require(ctx context.Context, tx store.Q, v core.Viewer, purpose string, kind core.RecordKind, body any, intent gate.Ref, assertion []byte) (core.Assertion, []byte, error) {
	return s.ref.Require(ctx, tx, v, purpose, kind, body, intent, assertion)
}

// RegisterPurpose registers a purpose's draft builder (core.CosignPurposes).
func (s *Service) RegisterPurpose(purpose string, kind core.RecordKind, build core.CosignBuilder) {
	s.ref.RegisterPurpose(purpose, kind, build)
}

// BeginSignup starts the invitee signup ceremony (core.SignupCosign).
func (s *Service) BeginSignup(context.Context, [32]byte, core.Credential, []core.CosignRecord) (core.Intent, error) {
	return core.Intent{}, gate.ErrNotFound
}

// FinishSignup verifies the signup ceremony in tx (core.SignupCosign).
func (s *Service) FinishSignup(context.Context, store.Q, [32]byte, core.UserID, core.Credential, []core.CosignRecord, []byte) ([]core.Assertion, error) {
	return nil, gate.ErrNotFound
}
