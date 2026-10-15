// Package engage owns likes, saves (P1), seen state, hides and show-less
// (ARCHITECTURE §4.3, §5.2, §5.3). It is the sole writer of engagements, seen,
// hides and show_less (plan §3.4) and implements core.Engagement and
// core.Prefer (network preference n and the upward-credit inputs).
//
// Owner: P0-W06 → P1-W08.
//
// Check order (plan §0.8): the request was charged by apix → the post ref is
// resolved (ErrNotFound unless it opens for the viewer, names a post and the
// viewer can see it) → only then anything else. MarkSeen never fails on refs:
// garbage, foreign and invisible refs are dropped silently and write nothing.
//
// Memory: likers are roaring bitmaps per post (window posts preloaded by Load,
// others loaded on demand into an LRU). Seen, hides and show-less stay in
// Postgres (the feed reads them once per session).
package engage

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// Limits and retention (§4.3, §5.2).
const (
	// SeenBatch is the most refs one MarkSeen call records (the rest are ignored).
	SeenBatch = 50
	// SeenRetention is how long seen state is kept (§4.3: 14 days).
	SeenRetention = 14 * 24 * time.Hour
	// HideRetention is how long a hide is kept: hides only filter feed
	// candidates, so they outlive the feed window (default 7 days) by far.
	HideRetention = 30 * 24 * time.Hour
	// SeenPageSize is the recently-seen page size.
	SeenPageSize = 20
	// LikerLRU bounds the liker bitmaps kept in memory.
	LikerLRU = 100_000
	// PurgeEvery is the period of the nightly purges.
	PurgeEvery = 24 * time.Hour
)

// §5.2 / §5.5 weights.
const (
	SignalCap  = 4.0 // min(4, Σ of u's signals)
	InviterCap = 4.0 // one inviter's invitees together: at most 4 units per post
	KidsK      = 20  // children read per post for upward credit
	Beta       = 0.3 // grandchildren weight (P1, gens = 2)
)

// kindLike is engagements.kind for a like (2 = save, P1-W08).
const kindLike int16 = 1

// Jobs.
const (
	JobSeenPurge  = "seen.purge"
	JobHidesPurge = "engage.hides_purge"
)

// MetricImpressions counts first-time seen events by the post's tier at the
// time it was seen (aggregate only, no IDs).
const MetricImpressions = "feed.impressions_by_degree"

// Service is the engagement module.
type Service struct {
	d *core.Deps

	mu     sync.Mutex
	likers *lru[core.PostID, *roaring.Bitmap]
	seq    uint64 // bumped by every like mutation; a load that raced one is not cached
}

var (
	_ core.Engagement = (*Service)(nil)
	_ core.Prefer     = (*Service)(nil)
)

// New returns the service.
func New(d *core.Deps) *Service {
	return &Service{d: d, likers: newLRU[core.PostID, *roaring.Bitmap](LikerLRU)}
}

// Init registers the purge jobs and the deletion clean-ups, then loads the
// likers of the window posts.
func (s *Service) Init(ctx context.Context) error {
	if s.d.Bus != nil {
		core.Subscribe(s.d.Bus, func(e core.AccountDeleted) { s.forgetUser(e.ID) })
		core.Subscribe(s.d.Bus, func(e core.PostDeleted) { s.forgetPost(e.ID) })
	}
	if s.d.Jobs != nil {
		s.d.Jobs.Handle(JobSeenPurge, func(ctx context.Context, _ json.RawMessage) error {
			_, err := s.PurgeSeen(ctx)
			return err
		})
		s.d.Jobs.Handle(JobHidesPurge, func(ctx context.Context, _ json.RawMessage) error {
			_, err := s.PurgeHides(ctx)
			return err
		})
		s.d.Jobs.Every(JobSeenPurge, PurgeEvery)
		s.d.Jobs.Every(JobHidesPurge, PurgeEvery)
	}
	if s.d.DB == nil {
		return nil
	}
	return s.Load(ctx)
}

// ---- shared helpers ----

// viewer returns the signed-in viewer and their search.
func (s *Service) viewer(ctx context.Context) (core.Viewer, core.Search, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return v, nil, err
	}
	sr, err := s.d.Vis.SearchFor(ctx, v.ID)
	if err != nil {
		return v, nil, err
	}
	return v, sr, nil
}

// resolve opens a post ref for the viewer of sr: ErrNotFound unless the ref
// opens, the post exists and the viewer can see it.
func (s *Service) resolve(ctx context.Context, sr core.Search, ref gate.Ref) (core.Checked, error) {
	id, ok := s.d.Refs.OpenPost(sr.Viewer(), ref)
	if !ok {
		return core.Checked{}, gate.ErrNotFound
	}
	m, ok := s.d.Posts.Meta(ctx, id)
	if !ok {
		return core.Checked{}, gate.ErrNotFound
	}
	c, ok := s.d.Vis.CheckPost(ctx, sr, m)
	if !ok {
		return core.Checked{}, gate.ErrNotFound
	}
	return c, nil
}

func (s *Service) now() time.Time { return s.d.Clock.Now() }

// logErr logs a failed read behind an interface method that cannot return it.
func (s *Service) logErr(code string, err error) {
	if s.d.Log != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		s.d.Log.Error("engage read failed", "code", code, "err", err.Error())
	}
}

// forgetUser deletes the rows this module owns for a deleted account and
// drops the account from every cached liker set (plan §3.4 deletion fan-out).
func (s *Service) forgetUser(u core.UserID) {
	if s.d.DB != nil {
		ctx := context.Background()
		err := s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
			for _, q := range []string{
				"DELETE FROM engagements WHERE user_id = $1",
				"DELETE FROM seen WHERE user_id = $1",
				"DELETE FROM hides WHERE user_id = $1",
				"DELETE FROM show_less WHERE user_id = $1 OR author_id = $1",
			} {
				if _, err := tx.Exec(ctx, q, int64(u)); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			s.logErr("engage.forget_user", err)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	s.likers.each(func(_ core.PostID, b *roaring.Bitmap) { b.Remove(uint32(u)) })
}

// forgetPost drops a deleted post's likes: nobody can see the post any more,
// so they can never count again (data minimization, §2.3).
func (s *Service) forgetPost(p core.PostID) {
	if s.d.DB != nil {
		if _, err := s.d.DB.Q().Exec(context.Background(), "DELETE FROM engagements WHERE post_id = $1", p); err != nil {
			s.logErr("engage.forget_post", err)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	s.likers.remove(p)
}
