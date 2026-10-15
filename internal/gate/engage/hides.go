package engage

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// Hide removes a visible post from the viewer's feed (§5.2 personal signal).
// Hides are personal: never shown to anyone, never a collective signal.
func (s *Service) Hide(ctx context.Context, p gate.Ref) error {
	_, err := s.hide(ctx, p, false)
	return err
}

// ShowLess hides a visible post and lowers its author for the viewer only
// (show_less(author) += 1; rank applies λ·min(n,3)·8h). Repeating it on the
// same post counts once; the viewer's own posts are only hidden.
func (s *Service) ShowLess(ctx context.Context, p gate.Ref) error {
	_, err := s.hide(ctx, p, true)
	return err
}

func (s *Service) hide(ctx context.Context, p gate.Ref, less bool) (bool, error) {
	v, sr, err := s.viewer(ctx)
	if err != nil {
		return false, err
	}
	c, err := s.resolve(ctx, sr, p)
	if err != nil {
		return false, err
	}
	m := c.Meta()
	now := s.now()
	var fresh bool
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO hides (user_id, post_id, at) VALUES ($1, $2, $3)
			ON CONFLICT (user_id, post_id) DO NOTHING`, int64(v.ID), m.ID, now)
		if err != nil {
			return err
		}
		fresh = tag.RowsAffected() > 0
		if !less || !fresh || m.Author == v.ID {
			return nil
		}
		_, err = tx.Exec(ctx, `INSERT INTO show_less (user_id, author_id, n, last_at) VALUES ($1, $2, 1, $3)
			ON CONFLICT (user_id, author_id) DO UPDATE SET n = show_less.n + 1, last_at = EXCLUDED.last_at`,
			int64(v.ID), int64(m.Author), now)
		return err
	})
	return fresh, err
}

// ---- core.Engagement ----

// HiddenSet returns the posts v hid (hide or show-less).
func (s *Service) HiddenSet(ctx context.Context, v core.UserID) map[core.PostID]struct{} {
	out := map[core.PostID]struct{}{}
	rows, err := s.d.DB.Q().Query(ctx, "SELECT post_id FROM hides WHERE user_id = $1", int64(v))
	if err != nil {
		s.logErr("engage.hides_load", err)
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id core.PostID
		if err := rows.Scan(&id); err != nil {
			s.logErr("engage.hides_load", err)
			return out
		}
		out[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		s.logErr("engage.hides_load", err)
	}
	return out
}

// ShowLessCounts returns v's show-less count per author.
func (s *Service) ShowLessCounts(ctx context.Context, v core.UserID) map[core.UserID]int {
	out := map[core.UserID]int{}
	rows, err := s.d.DB.Q().Query(ctx, "SELECT author_id, n FROM show_less WHERE user_id = $1", int64(v))
	if err != nil {
		s.logErr("engage.show_less_load", err)
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var (
			a int64
			n int
		)
		if err := rows.Scan(&a, &n); err != nil {
			s.logErr("engage.show_less_load", err)
			return out
		}
		out[core.UserID(a)] = n //nolint:gosec // users.id < 2^32 (CHECK)
	}
	if err := rows.Err(); err != nil {
		s.logErr("engage.show_less_load", err)
	}
	return out
}

// PurgeHides deletes hides older than HideRetention (job engage.hides_purge):
// a hide is recorded after its post was created, so by then the post has long
// left the feed window, the only place hides filter.
func (s *Service) PurgeHides(ctx context.Context) (int, error) {
	tag, err := s.d.DB.Q().Exec(ctx, "DELETE FROM hides WHERE at < $1", s.now().Add(-s.hideRetention()))
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// hideRetention is HideRetention, or longer when the feed window is.
func (s *Service) hideRetention() time.Duration {
	d := HideRetention
	if s.d.Cfg != nil && s.d.Cfg.FeedWindow > d {
		d = s.d.Cfg.FeedWindow
	}
	return d
}
