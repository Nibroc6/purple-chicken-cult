package engage

import (
	"context"

	"github.com/RoaringBitmap/roaring/v2"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// memoKids2 keys KidsN with grandchildren (gens = 2, P1) apart from direct KidsN.
const memoKids2 uint8 = 32

// likeSignal is the signal of a like (§5.2; a save, P1, adds 2).
const likeSignal = 1.0

// N returns network preference n for p as seen from sr (§5.2, Phase 0 subset
// §5.5), memoized per (snapshot, post):
//
//	n = Σ over likers u at distance 1 or 2 of the reader:
//	      weight(u) · proximity(u) · min(4, signals(u))
//
// proximity 1.0 for friends, 0.5 for the second degree; weight 0 for the
// post's author and for accounts on probation (or not active); all invitees of
// one inviter together contribute at most 4 units. Likes from farther away, or
// from people the reader cannot reach, never count, so n only ever reflects the
// reader's own 2-hop aggregate (§2.2(6)). Phase 0 signals are likes (+1); P1
// adds saves (+2).
func (s *Service) N(ctx context.Context, sr core.Search, p core.Checked, memo *core.Memo) float64 {
	pm := p.Meta()
	key := core.MemoKey{Kind: core.MemoN, Post: pm.ID}
	if v, ok := memo.Get(key); ok {
		if n, ok := v.(float64); ok {
			return n
		}
	}
	l1, l2 := sr.LayerBitmap(1), sr.LayerBitmap(2)
	var near1, near2 *roaring.Bitmap
	s.withLikers(ctx, []core.PostID{pm.ID}, func(_ core.PostID, b *roaring.Bitmap) {
		near1, near2 = roaring.And(b, l1), roaring.And(b, l2)
	})
	now := s.now()
	byInviter := map[core.UserID]float64{}
	var n float64
	add := func(set *roaring.Bitmap, proximity float64) {
		if set == nil {
			return
		}
		set.Iterate(func(x uint32) bool {
			u := core.UserID(x)
			if u == pm.Author || u == sr.Viewer() {
				return true
			}
			meta, err := s.d.Users.Get(ctx, u)
			if err != nil || meta.Status != core.StatusActive || meta.OnProbation(now) {
				return true
			}
			units := proximity * min(SignalCap, likeSignal)
			if inv, ok := s.d.Users.InviterOf(u); ok && inv != 0 {
				byInviter[inv] += units
				return true
			}
			n += units
			return true
		})
	}
	add(near1, 1.0)
	add(near2, 0.5)
	for _, units := range byInviter {
		n += min(InviterCap, units)
	}
	memo.Put(key, n)
	return n
}

// KidsN returns n_kid per child for the upward credit (§5.1): the children
// come from Visibility.VisibleChildren(RefOnly, NotSelf, Limit 20), so links
// the reader cannot see, cont links, detached and self-replies never count and
// the limit applies after those filters (fidelity #2). Shadow links are skipped
// (only their author sees them). A slippery parent (ref policy nobody) returns
// nil: no reply credit (fidelity #3). gens = 2 (P1) adds β·Σ n of each child's
// own children, at most K² grandchildren in total.
func (s *Service) KidsN(ctx context.Context, sr core.Search, p core.Checked, memo *core.Memo, gens int) []float64 {
	pm := p.Meta()
	if pm.RefPolicy == core.PolNobody {
		return nil
	}
	kind := core.MemoKidsN
	if gens >= 2 {
		kind = memoKids2
	}
	key := core.MemoKey{Kind: kind, Post: pm.ID}
	if v, ok := memo.Get(key); ok {
		if out, ok := v.([]float64); ok {
			return out
		}
	}
	f := core.ChildFilter{RefOnly: true, NotSelf: true, Limit: KidsK}
	kids := s.d.Vis.VisibleChildren(ctx, sr, pm, f)
	out := make([]float64, 0, len(kids))
	grand := 0
	for _, k := range kids {
		if k.Link.Shadow {
			continue
		}
		n := s.N(ctx, sr, k.Other, memo)
		if gens >= 2 && k.Other.Meta().RefPolicy != core.PolNobody {
			var sum float64
			for _, g := range s.d.Vis.VisibleChildren(ctx, sr, k.Other.Meta(), f) {
				if g.Link.Shadow || grand >= KidsK*KidsK {
					continue
				}
				grand++
				sum += s.N(ctx, sr, g.Other, memo)
			}
			n += Beta * sum
		}
		out = append(out, n)
	}
	memo.Put(key, out)
	return out
}
