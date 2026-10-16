package engage

import (
	"context"

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
		q := "DELETE FROM engagements WHERE user_id = $1 AND post_id = $2 AND kind = $3"
		args := []any{int64(v.ID), id, kindLike}
		if on {
			q = `INSERT INTO engagements (user_id, post_id, kind, at) VALUES ($1, $2, $3, $4)
				ON CONFLICT (user_id, post_id, kind) DO NOTHING`
			args = append(args, s.now())
		}
		tag, err := tx.Exec(ctx, q, args...)
		changed = tag.RowsAffected() > 0
		return err
	})
	if err != nil {
		return err
	}
	s.setLike(v.ID, id, on)
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

// Save sets or clears the viewer's private save (P1-W08).
func (s *Service) Save(ctx context.Context, _ gate.Ref, _ bool) error {
	if _, err := core.RequireUser(ctx); err != nil {
		return err
	}
	return gate.ErrNotImplemented
}

// Saves lists the viewer's saves (P1-W08).
func (s *Service) Saves(ctx context.Context, _ string) (gate.Page[gate.PostView], error) {
	if _, err := core.RequireUser(ctx); err != nil {
		return gate.Page[gate.PostView]{}, err
	}
	return gate.Page[gate.PostView]{}, gate.ErrNotImplemented
}

// ---- memory ----

// setLike applies a committed like to the cached bitmap (when cached; an
// uncached post loads from the database on its next read).
func (s *Service) setLike(u core.UserID, p core.PostID, on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	b, ok := s.likers.peek(p)
	if !ok {
		return
	}
	if on {
		b.Add(uint32(u))
	} else {
		b.Remove(uint32(u))
	}
}

// SetLike mirrors a like written to the database by someone else (devseed's
// fixtures) into memory.
func (s *Service) SetLike(u core.UserID, p core.PostID, on bool) { s.setLike(u, p, on) }

// Load forgets every cached liker set and preloads the likers of the window
// posts (Init, test reset, fixture seeding).
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
	loaded, err := s.loadLikers(ctx, ids)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seq != seq {
		return nil // a like raced the load: keep loading on demand
	}
	for _, id := range ids {
		pid := uuid.MustParse(id)
		b := loaded[pid]
		if b == nil {
			b = roaring.New()
		}
		s.likers.put(pid, b)
	}
	return nil
}

// loadLikers reads the like bitmaps of ids (absent ⇒ no likes).
func (s *Service) loadLikers(ctx context.Context, ids []string) (map[core.PostID]*roaring.Bitmap, error) {
	rows, err := s.d.DB.Q().Query(ctx, `SELECT post_id, user_id FROM engagements
		WHERE kind = $2 AND post_id = ANY($1::uuid[])`, ids, kindLike)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[core.PostID]*roaring.Bitmap{}
	for rows.Next() {
		var (
			p core.PostID
			u int64
		)
		if err := rows.Scan(&p, &u); err != nil {
			return nil, err
		}
		b := out[p]
		if b == nil {
			b = roaring.New()
			out[p] = b
		}
		b.Add(uint32(u)) //nolint:gosec // users.id < 2^32 (CHECK)
	}
	return out, rows.Err()
}

// withLikers calls fn with the (read-only, locked) like bitmap of every id;
// misses are loaded from the database first. fn must not retain the bitmaps.
func (s *Service) withLikers(ctx context.Context, ids []core.PostID, fn func(core.PostID, *roaring.Bitmap)) {
	var miss []string
	s.mu.Lock()
	for _, id := range ids {
		if _, ok := s.likers.get(id); !ok {
			miss = append(miss, id.String())
		}
	}
	seq := s.seq
	s.mu.Unlock()

	var loaded map[core.PostID]*roaring.Bitmap
	if len(miss) > 0 && s.d.DB != nil {
		var err error
		if loaded, err = s.loadLikers(ctx, miss); err != nil {
			s.logErr("engage.likers_load", err)
			loaded = map[core.PostID]*roaring.Bitmap{}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	cache := s.seq == seq
	for _, id := range ids {
		b, ok := s.likers.get(id)
		if !ok {
			if b = loaded[id]; b == nil {
				b = roaring.New()
			}
			if cache {
				s.likers.put(id, b)
			}
		}
		fn(id, b)
	}
}

// ---- core.Engagement ----

// LikerSet returns a snapshot of p's likers.
func (s *Service) LikerSet(ctx context.Context, p core.PostID) *roaring.Bitmap {
	out := roaring.New()
	s.withLikers(ctx, []core.PostID{p}, func(_ core.PostID, b *roaring.Bitmap) { out = b.Clone() })
	return out
}

// SaverSet returns p's savers. Phase 0 has no saves (P1-W08).
func (s *Service) SaverSet(context.Context, core.PostID) *roaring.Bitmap { return roaring.New() }

// LikedByMe reports which of ps v liked.
func (s *Service) LikedByMe(ctx context.Context, v core.UserID, ps []core.PostID) map[core.PostID]bool {
	out := make(map[core.PostID]bool, len(ps))
	s.withLikers(ctx, ps, func(id core.PostID, b *roaring.Bitmap) {
		if b.Contains(uint32(v)) {
			out[id] = true
		}
	})
	return out
}
