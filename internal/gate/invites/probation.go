package invites

import (
	"context"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// Probation (§8, P0): the time part (ProbationFor after joining) AND at least
// OutsideFriends edges to users outside the account's own invite subtree.
// Clearance is monotonic (only core.Probation.Reset undoes it) and grants one
// invite.

// outsideCount returns how many of u's friends are outside u's invite subtree.
func (s *Service) outsideCount(ctx context.Context, u core.UserID) (int, error) {
	sub, err := s.Subtree(ctx, u)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range s.d.Graph.Friends(u) {
		if !sub.Contains(uint32(e.To)) {
			n++
		}
	}
	return n, nil
}

// needOutside is how many more outside friends u needs (0 when enough).
func (s *Service) needOutside(ctx context.Context, u core.UserID) (int, error) {
	n, err := s.outsideCount(ctx, u)
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
	need, err := s.needOutside(ctx, u)
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

// probationUntil is the time part of a new account's probation.
func probationUntil(now time.Time) *time.Time {
	t := now.Add(ProbationFor)
	return &t
}
