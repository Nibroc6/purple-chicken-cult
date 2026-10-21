package engage

import (
	"context"
	"time"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// ---- handler-facing API ----

// Like sets (on) or clears the viewer's like of a visible post. Liking twice
// or clearing a like that is not there is a no-op.
func (s *Service) Like(ctx context.Context, p gate.Ref, on bool) error {
	v, sr, err := s.viewer(ctx)
	if err != nil {
		return err
	}
	c, err := s.resolve(ctx, sr, p)
	if err != nil {
		return err
	}
	id := c.Meta().ID
	var changed bool
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, engagementSQL(on), engagementArgs(v.ID, id, kindLike, on, s.now())...)
		changed = tag.RowsAffected() > 0
		return err
	})
	if err != nil {
		return err
	}
	s.setCached(v.ID, id, kindLike, on)
	if changed && s.d.Bus != nil {
		s.d.Bus.Publish(core.Liked{User: v.ID, Post: id, On: on})
	}
	return nil
}

// Likers returns the like summary of a visible post as the viewer sees it
// (§5.3): likes from within 2 hops, the viewer's friends who allow naming.
func (s *Service) Likers(ctx context.Context, p gate.Ref) (gate.LikeSummary, error) {
	_, sr, err := s.viewer(ctx)
	if err != nil {
		return gate.LikeSummary{}, err
	}
	c, err := s.resolve(ctx, sr, p)
	if err != nil {
		return gate.LikeSummary{}, err
	}
	vs, err := s.d.Views.Posts(ctx, sr, []core.Checked{c}, core.ViewOpts{})
	if err != nil {
		return gate.LikeSummary{}, err
	}
	if len(vs) != 1 {
		return gate.LikeSummary{}, gate.ErrNotFound
	}
	return vs[0].Likes, nil
}

// Save sets (on) or clears the viewer's private save of a visible post.
// Saves are never displayed or counted in any view: only the saver sees
// saved_by_me, and saves reach others solely as ranking signal (§5.2, +2).
// Saving twice or clearing a save that is not there is a no-op.
func (s *Service) Save(ctx context.Context, p gate.Ref, on bool) error {
	v, sr, err := s.viewer(ctx)
	if err != nil {
		return err
	}
	c, err := s.resolve(ctx, sr, p)
	if err != nil {
		return err
	}
	id := c.Meta().ID
	if err := s.setEngagement(ctx, v.ID, id, kindSave, on); err != nil {
		return err
	}
	s.setCached(v.ID, id, kindSave, on)
	return nil
}

// Saves lists the posts the viewer saved that the viewer can still see, most
// recently saved first.
func (s *Service) Saves(ctx context.Context, cursor string) (gate.Page[gate.PostView], error) {
	v, sr, err := s.viewer(ctx)
	if err != nil {
		return gate.Page[gate.PostView]{}, err
	}
	after, err := s.openCursor(v.ID, cursorSaves, cursor)
	if err != nil {
		return gate.Page[gate.PostView]{}, err
	}
	return s.listPage(ctx, sr, cursorSaves, after, func(cur *seenKey, n int) ([]seenKey, error) {
		return s.savesAfter(ctx, v.ID, cur, n)
	})
}

func (s *Service) savesAfter(ctx context.Context, v core.UserID, after *seenKey, n int) ([]seenKey, error) {
	q := `SELECT post_id, at FROM engagements WHERE user_id = $1 AND kind = $2
		ORDER BY at DESC, post_id DESC LIMIT $3`
	args := []any{int64(v), kindSave, n}
	if after != nil {
		q = `SELECT post_id, at FROM engagements WHERE user_id = $1 AND kind = $2 AND (at, post_id) < ($4, $5)
			ORDER BY at DESC, post_id DESC LIMIT $3`
		args = append(args, after.at, after.post)
	}
	return scanKeys(s.d.DB.Q().Query(ctx, q, args...))
}

// setEngagement writes or deletes one engagements row (idempotent).
func (s *Service) setEngagement(ctx context.Context, u core.UserID, p core.PostID, kind int16, on bool) error {
	_, err := s.d.DB.Q().Exec(ctx, engagementSQL(on), engagementArgs(u, p, kind, on, s.now())...)
	return err
}

func engagementSQL(on bool) string {
	if on {
		return `INSERT INTO engagements (user_id, post_id, kind, at) VALUES ($1, $2, $3, $4)
			ON CONFLICT (user_id, post_id, kind) DO NOTHING`
	}
	return "DELETE FROM engagements WHERE user_id = $1 AND post_id = $2 AND kind = $3"
}

func engagementArgs(u core.UserID, p core.PostID, kind int16, on bool, at time.Time) []any {
	args := []any{int64(u), p, kind}
	if on {
		args = append(args, at)
	}
	return args
}

// ---- memory ----

// engagers are the likers and savers of one post.
type engagers struct{ likes, saves *roaring.Bitmap }

func newEngagers() *engagers { return &engagers{likes: roaring.New(), saves: roaring.New()} }

func (e *engagers) set(kind int16) *roaring.Bitmap {
	if kind == kindSave {
		return e.saves
	}
	return e.likes
}

// setCached applies a committed like or save to the cached bitmaps (when
// cached; an uncached post loads from the database on its next read).
func (s *Service) setCached(u core.UserID, p core.PostID, kind int16, on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	e, ok := s.likers.peek(p)
	if !ok {
		return
	}
	if on {
		e.set(kind).Add(uint32(u))
	} else {
		e.set(kind).Remove(uint32(u))
	}
}

// SetLike mirrors a like written to the database by someone else (devseed's
// fixtures) into memory.
func (s *Service) SetLike(u core.UserID, p core.PostID, on bool) { s.setCached(u, p, kindLike, on) }

// SetSave mirrors a save written to the database by someone else (fixtures)
// into memory.
func (s *Service) SetSave(u core.UserID, p core.PostID, on bool) { s.setCached(u, p, kindSave, on) }

// Load forgets every cached engager set and preloads the engagers of the
// window posts (Init, test reset, fixture seeding).
func (s *Service) Load(ctx context.Context) error {
	var ids []string
	s.d.Posts.WindowScan(func(m core.PostMeta) bool {
		ids = append(ids, m.ID.String())
		return len(ids) < LikerLRU
	})
	s.mu.Lock()
	s.seq++
	s.likers.clear()
	seq := s.seq
	s.mu.Unlock()
	if len(ids) == 0 || s.d.DB == nil {
		return nil
	}
	loaded, err := s.loadEngagers(ctx, ids)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seq != seq {
		return nil // an engagement raced the load: keep loading on demand
	}
	for _, id := range ids {
		pid := uuid.MustParse(id)
		e := loaded[pid]
		if e == nil {
			e = newEngagers()
		}
		s.likers.put(pid, e)
	}
	return nil
}

// loadEngagers reads the like and save bitmaps of ids (absent ⇒ none).
func (s *Service) loadEngagers(ctx context.Context, ids []string) (map[core.PostID]*engagers, error) {
	rows, err := s.d.DB.Q().Query(ctx, `SELECT post_id, user_id, kind FROM engagements
		WHERE kind IN ($2, $3) AND post_id = ANY($1::uuid[])`, ids, kindLike, kindSave)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[core.PostID]*engagers{}
	for rows.Next() {
		var (
			p    core.PostID
			u    int64
			kind int16
		)
		if err := rows.Scan(&p, &u, &kind); err != nil {
			return nil, err
		}
		e := out[p]
		if e == nil {
			e = newEngagers()
			out[p] = e
		}
		e.set(kind).Add(uint32(u)) //nolint:gosec // users.id < 2^32 (CHECK)
	}
	return out, rows.Err()
}

// withEngagers calls fn with the (read-only, locked) engagers of every id;
// misses are loaded from the database first. fn must not retain the bitmaps.
func (s *Service) withEngagers(ctx context.Context, ids []core.PostID, fn func(core.PostID, *engagers)) {
	var miss []string
	s.mu.Lock()
	for _, id := range ids {
		if _, ok := s.likers.get(id); !ok {
			miss = append(miss, id.String())
		}
	}
	seq := s.seq
	s.mu.Unlock()

	var loaded map[core.PostID]*engagers
	if len(miss) > 0 && s.d.DB != nil {
		var err error
		if loaded, err = s.loadEngagers(ctx, miss); err != nil {
			s.logErr("engage.likers_load", err)
			loaded = map[core.PostID]*engagers{}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	cache := s.seq == seq
	for _, id := range ids {
		e, ok := s.likers.get(id)
		if !ok {
			if e = loaded[id]; e == nil {
				e = newEngagers()
			}
			if cache {
				s.likers.put(id, e)
			}
		}
		fn(id, e)
	}
}

// ---- core.Engagement ----

// LikerSet returns a snapshot of p's likers.
func (s *Service) LikerSet(ctx context.Context, p core.PostID) *roaring.Bitmap {
	out := roaring.New()
	s.withEngagers(ctx, []core.PostID{p}, func(_ core.PostID, e *engagers) { out = e.likes.Clone() })
	return out
}

// SaverSet returns a snapshot of p's savers. Saves are private: callers use it
// only for the saver's own saved_by_me and never display or count it.
func (s *Service) SaverSet(ctx context.Context, p core.PostID) *roaring.Bitmap {
	out := roaring.New()
	s.withEngagers(ctx, []core.PostID{p}, func(_ core.PostID, e *engagers) { out = e.saves.Clone() })
	return out
}

// SavedByMe reports which of ps v saved.
func (s *Service) SavedByMe(ctx context.Context, v core.UserID, ps []core.PostID) map[core.PostID]bool {
	out := make(map[core.PostID]bool, len(ps))
	s.withEngagers(ctx, ps, func(id core.PostID, e *engagers) {
		if e.saves.Contains(uint32(v)) {
			out[id] = true
		}
	})
	return out
}

// LikedByMe reports which of ps v liked.
func (s *Service) LikedByMe(ctx context.Context, v core.UserID, ps []core.PostID) map[core.PostID]bool {
	out := make(map[core.PostID]bool, len(ps))
	s.withEngagers(ctx, ps, func(id core.PostID, e *engagers) {
		if e.likes.Contains(uint32(v)) {
			out[id] = true
		}
	})
	return out
}
