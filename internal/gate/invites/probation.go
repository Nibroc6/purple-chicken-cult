package invites

import (
	"context"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// Probation (§8): a time part AND at least OutsideFriends edges to users
// outside the account's own invite subtree. Phase 1 rule (every account
// created from the Phase 1 cut-over on): ProbationFor (30 days) and the
// outside edges must be friend+. Accounts that were mid-probation at the
// cut-over keep the Phase 0 rule (7 days, any edges; users.probation_rule = 0,
// fidelity #32). Clearance is monotonic (only core.Probation.Reset undoes it)
// and grants one invite.

// rule returns u's probation rule.
func (s *Service) rule(ctx context.Context, u core.UserID) (int16, error) {
	var r int16
	err := s.d.DB.Q().QueryRow(ctx, "SELECT probation_rule FROM users WHERE id = $1", int64(u)).Scan(&r)
	return r, notFoundOr(err)
}

// outsideCount returns how many of u's friends (friend+ ones only under the
// Phase 1 rule) are outside u's invite subtree.
func (s *Service) outsideCount(ctx context.Context, u core.UserID, rule int16) (int, error) {
	sub, err := s.Subtree(ctx, u)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range s.d.Graph.Friends(u) {
		if rule == RuleP1 && e.Level != core.LevelFriendPlus {
			continue
		}
		if !sub.Contains(uint32(e.To)) {
			n++
		}
	}
	return n, nil
}

// needOutside is how many more outside friends u needs (0 when enough).
func (s *Service) needOutside(ctx context.Context, u core.UserID, rule int16) (int, error) {
	n, err := s.outsideCount(ctx, u, rule)
	if err != nil {
		return 0, err
	}
	return max(OutsideFriends-n, 0), nil
}

// evaluate clears u's probation when both parts hold; it reports whether it
// cleared it now.
func (s *Service) evaluate(ctx context.Context, u core.UserID) (bool, error) {
	m, err := s.d.Users.Get(ctx, u)
	if err != nil {
		return false, nil //nolint:nilerr // unknown or deleted account: nothing to evaluate
	}
	now := s.d.Clock.Now()
	if !m.OnProbation(now) || m.Status != core.StatusActive || now.Before(*m.ProbationUntil) {
		return false, nil
	}
	rule, err := s.rule(ctx, u)
	if err != nil {
		return false, err
	}
	need, err := s.needOutside(ctx, u, rule)
	if err != nil || need > 0 {
		return false, err
	}
	tag, err := s.d.DB.Q().Exec(ctx, `UPDATE users SET probation_cleared_at = $2
		WHERE id = $1 AND probation_cleared_at IS NULL AND probation_until <= $2`, int64(u), now)
	if err != nil || tag.RowsAffected() == 0 {
		return false, err
	}
	if _, err := s.d.DB.Q().Exec(ctx, `INSERT INTO invite_budgets (user_id, tokens, cap, refreshed_at) VALUES ($1, 1, $2, $3)
		ON CONFLICT (user_id) DO UPDATE SET tokens = 1, refreshed_at = EXCLUDED.refreshed_at`,
		int64(u), int16(DefaultCap), now); err != nil {
		return false, err
	}
	s.d.Bus.Publish(core.UserChanged{ID: u})
	s.d.Bus.Publish(core.ProbationCleared{ID: u, At: now})
	return true, nil
}

// EvaluateAll evaluates every account whose time part is over (job
// probation.evaluate) and returns how many it cleared.
func (s *Service) EvaluateAll(ctx context.Context) (int, error) {
	rows, err := s.d.DB.Q().Query(ctx, `SELECT id FROM users WHERE probation_cleared_at IS NULL
		AND probation_until IS NOT NULL AND probation_until <= $1 AND status = $2 AND NOT is_genesis ORDER BY id`,
		s.d.Clock.Now(), int16(core.StatusActive))
	if err != nil {
		return 0, err
	}
	var ids []core.UserID
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, core.UserID(id)) //nolint:gosec // users.id < 2^32 (DB CHECK)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, u := range ids {
		ok, err := s.evaluate(ctx, u)
		if err != nil {
			return n, err
		}
		if ok {
			n++
		}
	}
	return n, nil
}

// probationUntil is the time part of a new account's probation (Phase 1 rule).
func probationUntil(now time.Time) *time.Time {
	t := now.Add(ProbationFor)
	return &t
}
