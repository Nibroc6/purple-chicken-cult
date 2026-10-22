package engage

import (
	"context"
	"errors"
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
		lower := less && m.Author != v.ID
		tag, err := tx.Exec(ctx, `INSERT INTO hides (user_id, post_id, at, less) VALUES ($1, $2, $3, $4)
			ON CONFLICT (user_id, post_id) DO NOTHING`, int64(v.ID), m.ID, now, lower)
		if err != nil {
			return err
		}
		fresh = tag.RowsAffected() > 0
		if !lower || !fresh {
			return nil
		}
		_, err = tx.Exec(ctx, `INSERT INTO show_less (user_id, author_id, n, last_at) VALUES ($1, $2, 1, $3)
			ON CONFLICT (user_id, author_id) DO UPDATE SET n = show_less.n + 1, last_at = EXCLUDED.last_at`,
			int64(v.ID), int64(m.Author), now)
		return err
	})
	return fresh, err
}

// Unhide undoes a hide or a show-less of a visible post (idempotent). A
// show-less also gives back the step it took off the author.
func (s *Service) Unhide(ctx context.Context, p gate.Ref) error {
	v, sr, err := s.viewer(ctx)
	if err != nil {
		return err
	}
	c, err := s.resolve(ctx, sr, p)
	if err != nil {
		return err
	}
	m := c.Meta()
	return s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		var less bool
		err := tx.QueryRow(ctx, "DELETE FROM hides WHERE user_id = $1 AND post_id = $2 RETURNING less",
			int64(v.ID), m.ID).Scan(&less)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && !less) {
			return nil
		}
		if err != nil {
			return err
		}
		return s.stepBack(ctx, tx, v.ID, m.Author)
	})
}

// stepBack takes one show-less step off author for v, dropping the row at zero.
func (s *Service) stepBack(ctx context.Context, tx pgx.Tx, v, author core.UserID) error {
	if _, err := tx.Exec(ctx, "UPDATE show_less SET n = n - 1 WHERE user_id = $1 AND author_id = $2",
		int64(v), int64(author)); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, "DELETE FROM show_less WHERE user_id = $1 AND author_id = $2 AND n <= 0", int64(v), int64(author))
	return err
}

// Hidden lists the posts the viewer hid and can still see, most recently
// hidden first.
func (s *Service) Hidden(ctx context.Context, cursor string) (gate.Page[gate.PostView], error) {
	v, sr, err := s.viewer(ctx)
	if err != nil {
		return gate.Page[gate.PostView]{}, err
	}
	after, err := s.openCursor(v.ID, cursorHidden, cursor)
	if err != nil {
		return gate.Page[gate.PostView]{}, err
	}
	return s.listPage(ctx, sr, cursorHidden, after, func(cur *seenKey, n int) ([]seenKey, error) {
		q := `SELECT post_id, at FROM hides WHERE user_id = $1 ORDER BY at DESC, post_id DESC LIMIT $2`
		args := []any{int64(v.ID), n}
		if cur != nil {
			q = `SELECT post_id, at FROM hides WHERE user_id = $1 AND (at, post_id) < ($3, $4)
				ORDER BY at DESC, post_id DESC LIMIT $2`
			args = append(args, cur.at, cur.post)
		}
		return scanKeys(s.d.DB.Q().Query(ctx, q, args...))
	})
}

// ShowLessMax is the most authors ShowLessList returns.
const ShowLessMax = 200

// ShowLessList lists the authors the viewer asked to see less of, most
// recent first.
func (s *Service) ShowLessList(ctx context.Context) ([]gate.ShowLessView, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.d.DB.Q().Query(ctx, `SELECT author_id, n FROM show_less WHERE user_id = $1
		ORDER BY last_at DESC, author_id LIMIT $2`, int64(v.ID), ShowLessMax)
	if err != nil {
		return nil, err
	}
	type entry struct {
		id core.UserID
		n  int
	}
	var es []entry
	for rows.Next() {
		var (
			a int64
			n int
		)
		if err := rows.Scan(&a, &n); err != nil {
			rows.Close()
			return nil, err
		}
		es = append(es, entry{core.UserID(a), n}) //nolint:gosec // users.id < 2^32 (CHECK)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	ids := make([]core.UserID, len(es))
	for i, e := range es {
		ids[i] = e.id
	}
	metas, err := s.d.Users.GetMany(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := []gate.ShowLessView{}
	for _, e := range es {
		m, ok := metas[e.id]
		if !ok {
			continue
		}
		out = append(out, gate.ShowLessView{
			Ref: s.d.Refs.User(v.ID, e.id), Handle: m.Handle, DisplayName: m.DisplayName, Count: e.n,
		})
	}
	return out, nil
}

// ResetShowLess clears the viewer's show-less count for one author
// (idempotent). The posts stay hidden.
func (s *Service) ResetShowLess(ctx context.Context, author gate.Ref) error {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return err
	}
	a, ok := s.d.Refs.OpenUser(v.ID, author)
	if !ok {
		return gate.ErrNotFound
	}
	_, err = s.d.DB.Q().Exec(ctx, "DELETE FROM show_less WHERE user_id = $1 AND author_id = $2", int64(v.ID), int64(a))
	return err
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

// PurgeHides deletes hides older than HideRetention (one year; job
// engage.hides_purge): hides filter the feed and family-tree rows, so they are
// kept far longer than the feed window.
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
