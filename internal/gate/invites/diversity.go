package invites

import (
	"context"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// Invite diversity decay (§8, Phase 1): accounts whose edges mostly stay
// inside their inviter's subtree earn invites more slowly. Job
// invites.diversity (daily) sets each invited account's multiplier
//
//	diversity = clamp(outside / edges / DiversityTarget, DiversityFloor, 1)
//
// where outside counts the edges to users outside the inviter's invite
// subtree (the inviter included). No edges, genesis accounts and accounts
// without an inviter keep 1. The accrual so far is materialized at the old
// rate before the new one applies, so a change never acts retroactively.

// diversityOf is u's multiplier, with inviter's subtree from cache.
func (s *Service) diversityOf(ctx context.Context, u, inviter core.UserID, cache map[core.UserID]*roaring.Bitmap) (float64, error) {
	friends := s.d.Graph.Friends(u)
	if inviter == 0 || len(friends) == 0 {
		return 1, nil
	}
	sub, ok := cache[inviter]
	if !ok {
		var err error
		if sub, err = s.Subtree(ctx, inviter); err != nil {
			return 0, err
		}
		cache[inviter] = sub
	}
	out := 0
	for _, e := range friends {
		if !sub.Contains(uint32(e.To)) {
			out++
		}
	}
	return min(max(float64(out)/float64(len(friends))/DiversityTarget, DiversityFloor), 1), nil
}

// EvaluateDiversity recomputes every invite budget's multiplier (job
// invites.diversity) and returns how many changed.
func (s *Service) EvaluateDiversity(ctx context.Context) (int, error) {
	rows, err := s.d.DB.Q().Query(ctx, `SELECT b.user_id, COALESCE(u.invited_by, 0), b.diversity FROM invite_budgets b
		JOIN users u ON u.id = b.user_id WHERE u.status = $1 ORDER BY b.user_id`, int16(core.StatusActive))
	if err != nil {
		return 0, err
	}
	type row struct {
		id, inviter core.UserID
		old         float64
	}
	var list []row
	for rows.Next() {
		var (
			id, inv int64
			old     float64
		)
		if err := rows.Scan(&id, &inv, &old); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, row{core.UserID(id), core.UserID(inv), old}) //nolint:gosec // users.id < 2^32 (DB CHECK)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	cache := map[core.UserID]*roaring.Bitmap{}
	n := 0
	for _, r := range list {
		d, err := s.diversityOf(ctx, r.id, r.inviter, cache)
		if err != nil {
			return n, err
		}
		if d == r.old {
			continue
		}
		if err := s.setDiversity(ctx, r.id, d); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// setDiversity materializes u's accrual at the old rate, then sets d.
func (s *Service) setDiversity(ctx context.Context, u core.UserID, d float64) error {
	m, err := s.d.Users.Get(ctx, u)
	if err != nil {
		return nil //nolint:nilerr // the account went away meanwhile
	}
	now := s.d.Clock.Now()
	return s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		b, err := s.loadBudget(ctx, tx, m, now, true)
		if err != nil {
			return err
		}
		if b.probation || b.revoked || b.paused != nil {
			_, err = tx.Exec(ctx, "UPDATE invite_budgets SET diversity = $2 WHERE user_id = $1", int64(u), d)
			return err
		}
		_, err = tx.Exec(ctx, "UPDATE invite_budgets SET tokens = $2, refreshed_at = $3, diversity = $4 WHERE user_id = $1",
			int64(u), b.tokens, now, d)
		return err
	})
}
