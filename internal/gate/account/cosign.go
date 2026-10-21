package account

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coreref"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/sigs"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// Co-signed account changes (ARCHITECTURE §3 Phase 1, §17.4; plan P1-W10,
// hand-off p1-w01-2). Deleting the account and approving a recovery are signed
// with one of the signer's passkeys: the client opens an intent with POST
// /api/cosign {purpose, target} (the builders below draft the exact record),
// signs it in the co-sign dialog and sends {intent, assertion} with the
// change. The change's transaction consumes the intent (core.Cosign.Require),
// checks that the drafted record is the one this change writes and stores the
// assertion with the record (core.Signer), so it verifies offline.
//
//   - account_delete: the AccountDelete record {user, at}; target empty.
//   - recovery_approve: the RecoveryApproval record of the approval being
//     given {account, approver, first_approver, request, method, at}; the
//     target is the request ref the approver was shown (a guardian's
//     GET /api/recovery/requests item, or the moderator-scoped manual
//     request ID).
//
// A session authenticated with a passkey must co-sign; without a fresh
// assertion the change is the uniform not-found. Sessions without a credential
// exist only through the test hooks; their records stay server-signed.

// Co-sign purposes registered by this module (gate/cosign names them too).
const (
	PurposeAccountDelete   = "account_delete"
	PurposeRecoveryApprove = "recovery_approve"
)

// Recovery methods named in RecoveryApproval records.
const (
	methodModerators = "moderators"
	methodGuardians  = "guardians"
)

// Cosig is a co-signature sent with a change: the intent from POST
// /api/cosign and the passkey assertion over its draft.
type Cosig struct {
	Intent    gate.Ref        `json:"intent"`
	Assertion json.RawMessage `json:"assertion"`
}

func (c *Cosig) present() bool { return c != nil && (c.Intent != "" || len(c.Assertion) > 0) }

// deleteBody is the signed AccountDelete record body (party: the account).
type deleteBody struct {
	User uuid.UUID `json:"user"`
	At   sigs.Time `json:"at"`
}

// approvalBody is the signed RecoveryApproval record body (parties: the
// account and both approvers).
type approvalBody struct {
	Account       uuid.UUID `json:"account"`
	Approver      uuid.UUID `json:"approver"`
	FirstApprover uuid.UUID `json:"first_approver"`
	Request       uuid.UUID `json:"request"`
	Method        string    `json:"method"`
	At            sigs.Time `json:"at"`
}

// mustCosign reports whether v's changes must be co-signed: every session
// created by a passkey ceremony.
func mustCosign(v core.Viewer) bool { return len(v.Credential) > 0 }

// signed is a verified co-signature: the assertion and the record it signed.
type signed struct {
	a      core.Assertion
	record []byte
}

// verify consumes c for purpose in tx. No co-signature is fine only for a
// session that need not co-sign (nil result).
func (s *Service) verify(ctx context.Context, tx store.Q, v core.Viewer, purpose string, kind core.RecordKind, c *Cosig) (*signed, error) {
	if !c.present() {
		if mustCosign(v) {
			return nil, gate.ErrNotFound
		}
		return nil, nil
	}
	if s.d.Cosign == nil {
		return nil, gate.ErrNotFound
	}
	a, draft, err := s.d.Cosign.Require(ctx, tx, v, purpose, kind, nil, c.Intent, c.Assertion)
	if err != nil {
		return nil, err
	}
	rec, _, _, err := coreref.RecordFromDraft(draft)
	if err != nil {
		return nil, gate.ErrNotFound
	}
	return &signed{a: a, record: rec}, nil
}

// sign writes the record of kind for the change: the co-signed draft when
// there is one (its body must decode into want and pass check), else body.
func (s *Service) sign(ctx context.Context, tx store.Q, kind core.RecordKind, author core.UserID, body any,
	parties []core.UserID, sg *signed, check func(rec []byte) bool) error {
	if sg == nil {
		_, err := s.d.Signer.Sign(ctx, tx, kind, author, body, parties)
		return err
	}
	if !check(sg.record) {
		return gate.ErrNotFound
	}
	_, err := s.d.Signer.Sign(ctx, tx, kind, author, nil, parties, sg.a)
	return err
}

// registerPurposes registers the draft builders with gate/cosign.
func (s *Service) registerPurposes() {
	if s.d.CosignPurposes == nil {
		return
	}
	s.d.CosignPurposes.RegisterPurpose(PurposeAccountDelete, core.RecAccountDelete, s.deleteDraft)
	s.d.CosignPurposes.RegisterPurpose(PurposeRecoveryApprove, core.RecRecoveryApproval, s.approveDraft)
}

// deleteDraft drafts the AccountDelete record of the viewer's own account.
func (s *Service) deleteDraft(ctx context.Context, v core.Viewer, _ gate.Ref) (any, error) {
	m, err := s.d.Users.Get(ctx, v.ID)
	if err != nil || m.Status == core.StatusDeleted {
		return nil, gate.ErrNotFound
	}
	return deleteBody{User: m.ExtID, At: sigs.T(s.d.Clock.Now())}, nil
}

// approveDraft drafts the RecoveryApproval the viewer would give to the
// request target: a guardian's request ref, or (moderators) a manual request
// ID. Anything the viewer may not approve is the uniform not-found, except the
// problems the approval itself answers (second_guardian_required,
// second_moderator_required).
func (s *Service) approveDraft(ctx context.Context, v core.Viewer, target gate.Ref) (any, error) {
	if rid, ok := s.d.Refs.OpenHandshake(v.ID, target); ok {
		r, err := loadAny(ctx, s.d.DB.Q(), rid, false)
		if err != nil || r.kind != kindGuardian {
			return nil, gate.ErrNotFound
		}
		if err := s.canApproveGuardian(ctx, v, r); err != nil {
			return nil, err
		}
		return s.approval(ctx, r, v.ID, methodGuardians)
	}
	if v.Role >= core.RoleModerator {
		if rid, ok := s.d.Refs.OpenModItem(sharedModItem, target); ok {
			r, err := loadAny(ctx, s.d.DB.Q(), rid, false)
			if err != nil || r.kind != kindManual || r.state != stateOpen || r.user == v.ID {
				return nil, gate.ErrNotFound
			}
			if r.first == v.ID {
				return nil, gate.NewProblem(gate.StatusConflict, "second_moderator_required")
			}
			return s.approval(ctx, r, v.ID, methodModerators)
		}
	}
	return nil, gate.ErrNotFound
}

// approval is the RecoveryApproval body of approver's approval of r.
func (s *Service) approval(ctx context.Context, r recoveryRow, approver core.UserID, method string) (approvalBody, error) {
	exts := make([]uuid.UUID, 0, 3)
	for _, u := range []core.UserID{r.user, approver, r.first} {
		m, err := s.d.Users.Get(ctx, u)
		if err != nil {
			return approvalBody{}, gate.ErrNotFound
		}
		exts = append(exts, m.ExtID)
	}
	return approvalBody{Account: exts[0], Approver: exts[1], FirstApprover: exts[2], Request: r.id, Method: method,
		At: sigs.T(s.d.Clock.Now())}, nil
}

// sameApproval reports whether the signed record rec approves exactly want
// (its time is the draft's own).
func sameApproval(rec []byte, want approvalBody) bool {
	var got approvalBody
	if json.Unmarshal(rec, &got) != nil {
		return false
	}
	return got.Account == want.Account && got.Approver == want.Approver && got.FirstApprover == want.FirstApprover &&
		got.Request == want.Request && got.Method == want.Method
}
