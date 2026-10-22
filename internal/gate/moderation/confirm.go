package moderation

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/sigs"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// Cosig is the second moderator's co-signature: the intent from POST
// /api/cosign {purpose: mod_action, target: item} and the passkey assertion
// over its ModAction draft.
type Cosig struct {
	Intent    gate.Ref        `json:"intent"`
	Assertion json.RawMessage `json:"assertion"`
}

func (c *Cosig) present() bool { return c != nil && (c.Intent != "" || len(c.Assertion) > 0) }

// mustCosign reports whether v's confirmations must be co-signed: every
// session created by a passkey ceremony (sessions without one exist only
// through the test hooks; they confirm with the P0 fresh step-up).
func mustCosign(v core.Viewer) bool { return len(v.Credential) > 0 }

// modActionBody is the signed ModAction record of a confirmed two-person
// action (plan §4.7 records: ModAction{action, reason, at, second?}). Users are
// named by ext_id; the second moderator co-signs exactly these bytes.
type modActionBody struct {
	ID        uuid.UUID  `json:"id"`     // mod_actions.id
	Action    string     `json:"action"` // suspend_user | dismiss | quarantine_subtree | csam_report
	Reason    string     `json:"reason"`
	At        sigs.Time  `json:"at"`     // when the first moderator started it
	By        uuid.UUID  `json:"by"`     // the first moderator
	Second    uuid.UUID  `json:"second"` // the confirming moderator
	Subject   *uuid.UUID `json:"subject,omitempty"`
	ReportRef *string    `json:"report_ref,omitempty"`
}

// actionBody builds the ModAction record of p as confirmed by second.
func (s *Service) actionBody(ctx context.Context, p pending, second core.UserID) (modActionBody, error) {
	by, err := s.d.Users.Get(ctx, p.first)
	if err != nil {
		return modActionBody{}, err
	}
	me, err := s.d.Users.Get(ctx, second)
	if err != nil {
		return modActionBody{}, err
	}
	b := modActionBody{ID: p.id, Action: p.action, Reason: p.reason, At: sigs.T(p.at), By: by.ExtID, Second: me.ExtID,
		ReportRef: p.reportRef}
	if p.subject != 0 {
		if m, err := s.d.Users.Get(ctx, p.subject); err == nil {
			b.Subject = &m.ExtID
		}
	}
	return b, nil
}

// modActionDraft drafts the record a second moderator co-signs for the item
// target (core.CosignBuilder): the pending action, with the viewer as second.
// The first moderator gets second_moderator_required, an expired action
// action_expired; anything unresolvable is the uniform not-found.
func (s *Service) modActionDraft(ctx context.Context, v core.Viewer, target gate.Ref) (any, error) {
	if v.Role < core.RoleModerator {
		return nil, gate.ErrNotFound
	}
	id, ok := s.d.Refs.OpenModItem(v.ID, target)
	if !ok {
		return nil, gate.ErrNotFound
	}
	p, err := scanPending(s.d.DB.Q().QueryRow(ctx, "SELECT "+pendingCols+" FROM mod_actions WHERE item_id = $1 AND state = $2",
		id, actionPending))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, gate.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !p.at.Add(SecondWithin).After(s.d.Clock.Now()) {
		return nil, gate.NewProblem(gate.StatusConflict, "action_expired")
	}
	if p.first == v.ID {
		return nil, gate.NewProblem(gate.StatusConflict, "second_moderator_required")
	}
	return s.actionBody(ctx, p, v.ID)
}

// Confirm is the second moderator's confirmation of the two-person action
// pending on a queue item: a different moderator than the first, within 24 h,
// co-signing the ModAction record with their own passkey (c; a session
// without a passkey — test hooks only — needs a fresh step-up instead). The
// action is applied in the same transaction:
//
//   - suspend_user: suspends the subject, ends their sessions, closes the item,
//     writes the blame entry, the invitee_action notification and the
//     inviter's removed-account mark;
//   - dismiss (csam hold): lifts the subject's upload hold, closes the item;
//   - csam_report: records the CyberTipline report (core.CSAMHolds.Report),
//     closes the item;
//   - quarantine_subtree: the invite-tree quarantine (quarantine.go).
//
// Problems: second_moderator_required, action_expired, subject_inactive,
// no_subject, quarantine_active, csam_already_reported.
func (s *Service) Confirm(ctx context.Context, ref gate.Ref, c *Cosig) error {
	v, err := core.RequireModerator(ctx)
	if err != nil {
		return err
	}
	if _, ok := s.d.Refs.OpenModItem(v.ID, ref); !ok {
		return gate.ErrNotFound
	}
	switch {
	case c.present():
		if s.d.Cosign == nil {
			return gate.ErrNotFound
		}
	case mustCosign(v):
		return gate.ErrNotFound // a passkey session confirms only with its co-signature
	default:
		if v, err = s.freshModerator(ctx); err != nil {
			return err
		}
	}
	now := s.d.Clock.Now()
	var (
		done    after
		expired bool
	)
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		done, expired = after{}, false
		it, err := s.lockItem(ctx, tx, v, ref)
		if err != nil {
			return err
		}
		p, err := scanPending(tx.QueryRow(ctx, "SELECT "+pendingCols+" FROM mod_actions WHERE item_id = $1 AND state = $2 FOR UPDATE",
			it.id, actionPending))
		if errors.Is(err, pgx.ErrNoRows) {
			return gate.ErrNotFound // nothing awaits a second moderator
		}
		if err != nil {
			return err
		}
		if !p.at.Add(SecondWithin).After(now) {
			expired = true
			_, err := tx.Exec(ctx, "UPDATE mod_actions SET state = $1 WHERE id = $2", actionExpired, p.id)
			return err
		}
		if p.first == v.ID {
			return gate.NewProblem(gate.StatusConflict, "second_moderator_required")
		}
		body, err := s.actionBody(ctx, p, v.ID)
		if err != nil {
			return err
		}
		var as []core.Assertion
		if c.present() {
			a, _, err := s.d.Cosign.Require(ctx, tx, v, PurposeModAction, core.RecModAction, body, c.Intent, c.Assertion)
			if err != nil {
				return err
			}
			as = append(as, a)
		}
		if done, err = s.apply(ctx, tx, v, it, p, now); err != nil {
			return err
		}
		hash, err := s.d.Signer.Sign(ctx, tx, core.RecModAction, v.ID, body, []core.UserID{p.first, v.ID}, as...)
		if err != nil {
			return err
		}
		second, err := json.Marshal(map[string]any{"at": now, "auth_at": v.AuthAt, "cosigned": len(as) > 0,
			"record": hex.EncodeToString(hash)})
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE mod_actions SET state = $2, second_moderator = $3, second_auth = $4, done_at = $5
			WHERE id = $1`, p.id, actionDone, int64(v.ID), second, now)
		return err
	})
	if err != nil {
		return err
	}
	if expired {
		return gate.NewProblem(gate.StatusConflict, "action_expired")
	}
	s.publish(done)
	return nil
}

// apply carries out the confirmed action p on it in tx.
func (s *Service) apply(ctx context.Context, tx store.Q, v core.Viewer, it item, p pending, now time.Time) (after, error) {
	switch p.action {
	case ActionSuspendUser:
		target := p.subject
		if target == 0 || target == v.ID {
			return after{}, gate.NewProblem(gate.StatusConflict, "no_subject")
		}
		if err := s.d.Suspensions.Suspend(ctx, tx, target); err != nil {
			if errors.Is(err, gate.ErrNotFound) {
				return after{}, gate.NewProblem(gate.StatusConflict, "subject_inactive")
			}
			return after{}, err
		}
		// A suspended account has no live sessions: end them (and with them
		// the account's open event streams) when the suspension commits.
		if s.d.Passkeys != nil {
			if err := s.d.Passkeys.RevokeAll(ctx, tx, target); err != nil {
				return after{}, err
			}
		}
		if err := s.closeItem(ctx, tx, v, it, reportActioned, ActionSuspendUser, now); err != nil {
			return after{}, err
		}
		if err := s.mark(ctx, tx, target, now); err != nil {
			return after{}, err
		}
		return after{suspended: target}, s.blame(ctx, tx, target, p.action, p.reason, nil, now)
	case ActionDismiss:
		if it.media != uuid.Nil && s.d.CSAM != nil {
			if err := s.d.CSAM.ReleaseAccount(ctx, tx, it.media); err != nil {
				return after{}, err
			}
		}
		return after{}, s.closeItem(ctx, tx, v, it, reportDismissed, ActionDismiss, now)
	case ActionCSAMReport:
		if it.media == uuid.Nil || p.reportRef == nil || s.d.CSAM == nil {
			return after{}, gate.NewProblem(gate.StatusUnprocessable, "action_invalid", "action")
		}
		if err := s.d.CSAM.Report(ctx, tx, it.media, *p.reportRef); err != nil {
			return after{}, err
		}
		return after{}, s.closeItem(ctx, tx, v, it, reportActioned, ActionCSAMReport, now)
	case ActionQuarantine:
		return after{}, s.quarantine(ctx, tx, v, p, now)
	}
	return after{}, gate.ErrNotFound
}
