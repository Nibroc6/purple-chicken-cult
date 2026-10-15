package coreref

import (
	"context"
	"sync"
	"time"

	"github.com/RoaringBitmap/roaring/v2"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// MemEngage holds likes, saves, seen, hides and show-less in memory.
type MemEngage struct {
	mu       sync.RWMutex
	likes    map[core.PostID]*roaring.Bitmap
	saves    map[core.PostID]*roaring.Bitmap
	seen     map[core.UserID]map[core.PostID]time.Time
	hidden   map[core.UserID]map[core.PostID]struct{}
	showLess map[core.UserID]map[core.UserID]int
}

var _ core.Engagement = (*MemEngage)(nil)

// NewMemEngage returns empty engagement state.
func NewMemEngage() *MemEngage {
	return &MemEngage{
		likes: map[core.PostID]*roaring.Bitmap{}, saves: map[core.PostID]*roaring.Bitmap{},
		seen: map[core.UserID]map[core.PostID]time.Time{}, hidden: map[core.UserID]map[core.PostID]struct{}{},
		showLess: map[core.UserID]map[core.UserID]int{},
	}
}

func setBit(m map[core.PostID]*roaring.Bitmap, p core.PostID, u core.UserID, on bool) {
	b := m[p]
	if b == nil {
		b = roaring.New()
		m[p] = b
	}
	if on {
		b.Add(uint32(u))
	} else {
		b.Remove(uint32(u))
	}
}

// SetLike records or removes a like (reference helper).
func (e *MemEngage) SetLike(u core.UserID, p core.PostID, on bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	setBit(e.likes, p, u, on)
}

// SetSave records or removes a save (reference helper, P1).
func (e *MemEngage) SetSave(u core.UserID, p core.PostID, on bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	setBit(e.saves, p, u, on)
}

// AddSeen marks p seen by u at t.
func (e *MemEngage) AddSeen(u core.UserID, p core.PostID, at time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.seen[u] == nil {
		e.seen[u] = map[core.PostID]time.Time{}
	}
	e.seen[u][p] = at
}

// AddHide hides p for u.
func (e *MemEngage) AddHide(u core.UserID, p core.PostID) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.hidden[u] == nil {
		e.hidden[u] = map[core.PostID]struct{}{}
	}
	e.hidden[u][p] = struct{}{}
}

// AddShowLess bumps u's show-less count for author.
func (e *MemEngage) AddShowLess(u, author core.UserID) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.showLess[u] == nil {
		e.showLess[u] = map[core.UserID]int{}
	}
	e.showLess[u][author]++
}

func clone(b *roaring.Bitmap) *roaring.Bitmap {
	if b == nil {
		return roaring.New()
	}
	return b.Clone()
}

// LikerSet returns a snapshot of p's likers.
func (e *MemEngage) LikerSet(_ context.Context, p core.PostID) *roaring.Bitmap {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return clone(e.likes[p])
}

// SaverSet returns a snapshot of p's savers.
func (e *MemEngage) SaverSet(_ context.Context, p core.PostID) *roaring.Bitmap {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return clone(e.saves[p])
}

// LikedByMe reports which of ps v liked.
func (e *MemEngage) LikedByMe(_ context.Context, v core.UserID, ps []core.PostID) map[core.PostID]bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make(map[core.PostID]bool, len(ps))
	for _, p := range ps {
		if b := e.likes[p]; b != nil && b.Contains(uint32(v)) {
			out[p] = true
		}
	}
	return out
}

// SeenSet returns the posts v saw at or after since.
func (e *MemEngage) SeenSet(_ context.Context, v core.UserID, since time.Time) map[core.PostID]struct{} {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := map[core.PostID]struct{}{}
	for p, at := range e.seen[v] {
		if !at.Before(since) {
			out[p] = struct{}{}
		}
	}
	return out
}

// HiddenSet returns the posts v hid.
func (e *MemEngage) HiddenSet(_ context.Context, v core.UserID) map[core.PostID]struct{} {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make(map[core.PostID]struct{}, len(e.hidden[v]))
	for p := range e.hidden[v] {
		out[p] = struct{}{}
	}
	return out
}

// ShowLessCounts returns v's show-less count per author.
func (e *MemEngage) ShowLessCounts(_ context.Context, v core.UserID) map[core.UserID]int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make(map[core.UserID]int, len(e.showLess[v]))
	for a, n := range e.showLess[v] {
		out[a] = n
	}
	return out
}

// Ranking constants of §5.
const (
	SignalCap    = 4.0 // min(4, Σ signals)
	InviterCap   = 4.0 // one inviter's invitees together contribute at most 4 units per post
	Beta         = 0.3
	KidsK        = 20
	GrandKidsCap = KidsK * KidsK
)

// MemPrefer implements §5.5 n (likes; P1 adds saves ×2), probation/author weight
// 0, the per-inviter cap, and upward credit over VisibleChildren(RefOnly, NotSelf,
// Limit 20). It uses D.Engage, D.Users, D.Vis and D.Clock.
type MemPrefer struct{ D *core.Deps }

var _ core.Prefer = (*MemPrefer)(nil)

// N returns n for p as seen from s, memoized in memo.
func (m *MemPrefer) N(ctx context.Context, s core.Search, p core.Checked, memo *core.Memo) float64 {
	pm := p.Meta()
	key := core.MemoKey{Kind: core.MemoN, Post: pm.ID}
	if v, ok := memo.Get(key); ok {
		return v.(float64)
	}
	likes := m.D.Engage.LikerSet(ctx, pm.ID)
	saves := m.D.Engage.SaverSet(ctx, pm.ID)
	signals := map[core.UserID]float64{}
	likes.Iterate(func(x uint32) bool { signals[core.UserID(x)] += 1; return true })
	saves.Iterate(func(x uint32) bool { signals[core.UserID(x)] += 2; return true })

	now := time.Now().UTC()
	if m.D.Clock != nil {
		now = m.D.Clock.Now()
	}
	byInviter := map[core.UserID]float64{}
	var n float64
	for u, sig := range signals {
		d, ok := s.Dist(u)
		if !ok || d < 1 || d > 2 || u == pm.Author {
			continue
		}
		meta, err := m.D.Users.Get(ctx, u)
		if err != nil || meta.OnProbation(now) {
			continue
		}
		prox := 1.0
		if d == 2 {
			prox = 0.5
		}
		units := prox * min(SignalCap, sig)
		if inv, ok := m.D.Users.InviterOf(u); ok {
			byInviter[inv] += units
			continue
		}
		n += units
	}
	for _, units := range byInviter {
		n += min(InviterCap, units)
	}
	memo.Put(key, n)
	return n
}

// KidsN returns n_kid per visible child: n(C) (+ β·Σ n(grandchildren) when gens ≥ 2).
func (m *MemPrefer) KidsN(ctx context.Context, s core.Search, p core.Checked, memo *core.Memo, gens int) []float64 {
	pm := p.Meta()
	if pm.RefPolicy == core.PolNobody {
		return nil
	}
	f := core.ChildFilter{RefOnly: true, NotSelf: true, Limit: KidsK}
	kids := m.D.Vis.VisibleChildren(ctx, s, pm, f)
	out := make([]float64, 0, len(kids))
	grand := 0
	for _, k := range kids {
		if k.Link.Shadow {
			continue
		}
		n := m.N(ctx, s, k.Other, memo)
		if gens >= 2 && k.Other.Meta().RefPolicy != core.PolNobody {
			var sum float64
			for _, g := range m.D.Vis.VisibleChildren(ctx, s, k.Other.Meta(), f) {
				if g.Link.Shadow || grand >= GrandKidsCap {
					continue
				}
				grand++
				sum += m.N(ctx, s, g.Other, memo)
			}
			n += Beta * sum
		}
		out = append(out, n)
	}
	return out
}
