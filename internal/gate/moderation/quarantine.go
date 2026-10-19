package moderation

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// Invite-tree quarantine (§8). A subtree is the root inviter's transitive
// invitees (core.Invites.Subtree without the root). An account is confirmed
// bad once two moderators confirmed its suspension; only suspensions confirmed
// after the root's last quarantine count toward the next one.

func (s *Service) quarantineK() int {
	if s.d.Cfg == nil || s.d.Cfg.QuarantineK < 1 {
		return 3
	}
	return s.d.Cfg.QuarantineK
}

// lastQuarantine returns when root was last quarantined (nil = never).
func (s *Service) lastQuarantine(ctx context.Context, q store.Q, root core.UserID) (*time.Time, error) {
	var at time.Time
	err := q.QueryRow(ctx, "SELECT created_at FROM quarantines WHERE root_inviter = $1 ORDER BY created_at DESC LIMIT 1",
		int64(root)).Scan(&at)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &at, nil
}

// confirmedBad counts the accounts of root's subtree (root excluded) whose
// suspension two moderators confirmed since root's last quarantine.
func (s *Service) confirmedBad(ctx context.Context, q store.Q, root core.UserID) (int, error) {
	if s.d.Invites == nil {
		return 0, nil
	}
	tree, err := s.d.Invites.Subtree(ctx, root)
	if err != nil {
		return 0, err
	}
	tree.Remove(uint32(root))
	if tree.IsEmpty() {
		return 0, nil
	}
	ids := make([]int64, 0, tree.GetCardinality())
	for _, u := range tree.ToArray() {
		ids = append(ids, int64(u))
	}
	since := time.Time{}
	if last, err := s.lastQuarantine(ctx, q, root); err != nil {
		return 0, err
	} else if last != nil {
		since = *last
	}
	var n int
	err = q.QueryRow(ctx, `SELECT count(DISTINCT target_user) FROM mod_actions
		WHERE state = $1 AND action = 'suspend_user' AND target_user = ANY($2) AND done_at > $3`,
		actionDone, ids, since).Scan(&n)
	return n, err
}

// Quarantine starts the invite-tree quarantine of the subtree the subject of
// a queue item belongs to: its root is the subject's inviter. It needs a fresh
// step-up and at least PCC_QUARANTINE_K confirmed-bad accounts in the subtree,
// and only creates a two-person quarantine_subtree action that a second
// moderator confirms within 24 h (Confirm). reason defaults to the item's.
// Problems: reason_invalid, item_resolved, item_claimed, action_pending,
// no_subject (no subject, or a genesis subject without inviter),
// quarantine_active (the root's last quarantine is less than 30 days old),
// quarantine_threshold.
func (s *Service) Quarantine(ctx context.Context, ref gate.Ref, reason string) error {
	v, err := core.RequireModerator(ctx)
	if err != nil {
		return err
	}
	if _, ok := s.d.Refs.OpenModItem(v.ID, ref); !ok {
		return gate.ErrNotFound
	}
	if reason != "" && !validReason(core.ModReasons, reason) {
		return gate.NewProblem(gate.StatusUnprocessable, "reason_invalid", "reason")
	}
	if v, err = s.freshModerator(ctx); err != nil {
		return err
	}
	now := s.d.Clock.Now()
	return s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		it, err := s.lockItem(ctx, tx, v, ref)
		if err != nil {
			return err
		}
		if err := s.startable(ctx, tx, v, it, now); err != nil {
			return err
		}
		subject, _, _ := s.itemSubject(ctx, it)
		if subject == 0 {
			return gate.NewProblem(gate.StatusConflict, "no_subject")
		}
		m, err := s.d.Users.Get(ctx, subject)
		if err != nil || m.InvitedBy == 0 || m.InvitedBy == v.ID {
			return gate.NewProblem(gate.StatusConflict, "no_subject")
		}
		root := m.InvitedBy
		if err := s.quarantinable(ctx, tx, root, now); err != nil {
			return err
		}
		if reason == "" {
			reason = it.modReason()
		}
		return s.insertAction(ctx, tx, v, it, ActionQuarantine, reason, root, actionPending, now, nil)
	})
}

// quarantinable checks that root may be quarantined now.
func (s *Service) quarantinable(ctx context.Context, q store.Q, root core.UserID, now time.Time) error {
	last, err := s.lastQuarantine(ctx, q, root)
	if err != nil {
		return err
	}
	if last != nil && last.Add(QuarantineFor).After(now) {
		return gate.NewProblem(gate.StatusConflict, "quarantine_active")
	}
	n, err := s.confirmedBad(ctx, q, root)
	if err != nil {
		return err
	}
	if n < s.quarantineK() {
		return gate.NewProblem(gate.StatusConflict, "quarantine_threshold")
	}
	return nil
}

// quarantine applies a confirmed quarantine_subtree action in tx: the subtree
// (root excluded) goes back to probation for 30 days, the root's invites pause
// for 30 days (a second offense revokes them). Nothing outside the subtree
// changes, and nobody is notified.
func (s *Service) quarantine(ctx context.Context, tx store.Q, v core.Viewer, p pending, now time.Time) error {
	root := p.subject
	if root == 0 || s.d.Probation == nil || s.d.InvitePenalties == nil {
		return gate.NewProblem(gate.StatusConflict, "no_subject")
	}
	if err := s.quarantinable(ctx, tx, root, now); err != nil {
		return err
	}
	n, err := s.confirmedBad(ctx, tx, root)
	if err != nil {
		return err
	}
	tree, err := s.d.Invites.Subtree(ctx, root)
	if err != nil {
		return err
	}
	tree.Remove(uint32(root))
	users := make([]core.UserID, 0, tree.GetCardinality())
	for _, u := range tree.ToArray() {
		users = append(users, core.UserID(u))
	}
	until := now.Add(QuarantineFor)
	if err := s.d.Probation.Reset(ctx, tx, users, until); err != nil {
		return err
	}
	if _, err := s.d.InvitePenalties.Penalize(ctx, tx, root, until); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO quarantines (id, root_inviter, confirmed_bad, created_at, created_by, second_moderator)
		VALUES ($1, $2, $3, $4, $5, $6)`, s.d.IDs.New(), int64(root), n, now, int64(p.first), int64(v.ID))
	return err
}
