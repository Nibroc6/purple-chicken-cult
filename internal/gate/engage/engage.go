// Package engage owns likes, saves (P1), seen state, hides and show-less
// (ARCHITECTURE §4.3, §5.2, §5.3). It implements core.Engagement and core.Prefer.
//
// Owner: P0-W06 → P1-W08. Scaffold stub: core interfaces delegate to coreref
// (in-memory); the handler-facing API returns gate.ErrNotImplemented, except
// MarkSeen which by contract always succeeds.
package engage

import (
	"context"
	"time"

	"github.com/RoaringBitmap/roaring/v2"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coreref"
)

// Service is the engagement module.
type Service struct {
	d      *core.Deps
	ref    *coreref.MemEngage
	prefer *coreref.MemPrefer
}

var (
	_ core.Engagement = (*Service)(nil)
	_ core.Prefer     = (*Service)(nil)
)

// New returns the service.
func New(d *core.Deps) *Service {
	return &Service{d: d, ref: coreref.NewMemEngage(), prefer: &coreref.MemPrefer{D: d}}
}

// Init registers seen.purge / engage.hides_purge in W06.
func (s *Service) Init(context.Context) error { return nil }

// ---- handler-facing API ----

// Like sets or clears the viewer's like.
func (s *Service) Like(ctx context.Context, p gate.Ref, on bool) error { return s.todo(ctx) }

// Save sets or clears the viewer's private save (P1).
func (s *Service) Save(ctx context.Context, p gate.Ref, on bool) error { return s.todo(ctx) }

// Saves lists the viewer's saves (P1).
func (s *Service) Saves(ctx context.Context, cursor string) (gate.Page[gate.PostView], error) {
	return gate.Page[gate.PostView]{}, s.todo(ctx)
}

// MarkSeen records seen posts (≤50); invalid or invisible refs are silently ignored; always nil.
func (s *Service) MarkSeen(ctx context.Context, ps []gate.Ref) error {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return err
	}
	now := s.d.Clock.Now()
	for i, r := range ps {
		if i >= 50 {
			break
		}
		if id, ok := s.d.Refs.OpenPost(v.ID, r); ok {
			s.ref.AddSeen(v.ID, id, now)
		}
	}
	return nil
}

// RecentlySeen lists recently seen posts.
func (s *Service) RecentlySeen(ctx context.Context, cursor string) (gate.Page[gate.PostView], error) {
	return gate.Page[gate.PostView]{}, s.todo(ctx)
}

// Hide hides a post for the viewer.
func (s *Service) Hide(ctx context.Context, p gate.Ref) error { return s.todo(ctx) }

// ShowLess hides a post and lowers its author for the viewer.
func (s *Service) ShowLess(ctx context.Context, p gate.Ref) error { return s.todo(ctx) }

// Likers returns the like summary of a visible post.
func (s *Service) Likers(ctx context.Context, p gate.Ref) (gate.LikeSummary, error) {
	return gate.LikeSummary{}, s.todo(ctx)
}

func (s *Service) todo(ctx context.Context) error {
	if _, err := core.RequireUser(ctx); err != nil {
		return err
	}
	return gate.ErrNotImplemented
}

// ---- core.Engagement / core.Prefer (reference delegation) ----

// LikerSet returns p's likers.
func (s *Service) LikerSet(ctx context.Context, p core.PostID) *roaring.Bitmap {
	return s.ref.LikerSet(ctx, p)
}

// SaverSet returns p's savers (P1).
func (s *Service) SaverSet(ctx context.Context, p core.PostID) *roaring.Bitmap {
	return s.ref.SaverSet(ctx, p)
}

// LikedByMe reports which of ps v liked.
func (s *Service) LikedByMe(ctx context.Context, v core.UserID, ps []core.PostID) map[core.PostID]bool {
	return s.ref.LikedByMe(ctx, v, ps)
}

// SeenSet returns the posts v saw since since.
func (s *Service) SeenSet(ctx context.Context, v core.UserID, since time.Time) map[core.PostID]struct{} {
	return s.ref.SeenSet(ctx, v, since)
}

// HiddenSet returns the posts v hid.
func (s *Service) HiddenSet(ctx context.Context, v core.UserID) map[core.PostID]struct{} {
	return s.ref.HiddenSet(ctx, v)
}

// ShowLessCounts returns v's show-less counts per author.
func (s *Service) ShowLessCounts(ctx context.Context, v core.UserID) map[core.UserID]int {
	return s.ref.ShowLessCounts(ctx, v)
}

// N returns network preference n for p as seen from sr.
func (s *Service) N(ctx context.Context, sr core.Search, p core.Checked, memo *core.Memo) float64 {
	return s.prefer.N(ctx, sr, p, memo)
}

// KidsN returns n_kid per visible child.
func (s *Service) KidsN(ctx context.Context, sr core.Search, p core.Checked, memo *core.Memo, gens int) []float64 {
	return s.prefer.KidsN(ctx, sr, p, memo, gens)
}
