package graph

import (
	"context"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/RoaringBitmap/roaring/v2"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// unreached marks a dense index the search did not reach.
const unreached = math.MaxUint16

// queuePool recycles the BFS frontier buffers (plan §4.7: scratch from a pool).
var queuePool = sync.Pool{New: func() any { s := make([]int32, 0, 256); return &s }}

// Search runs a full layered BFS from v (Phase 0: no laziness, no cache).
//
// Exclusions are the READER's own only (D7): users in ex.Blocked are never
// visited, users in ex.HiddenVia are reached but never expanded (transit
// forbidden). Being blocked by someone else changes nothing here.
//
// First hops (D22, fidelity #10): rank(f) = (level(V,f) == friend+ ? 0 : 1,
// since(V,f), f) over the reader's own edge only. Layer-1 nodes label
// themselves. Relaxing u→w with w ∈ L_{k+1}: an unvisited w takes u's first
// hop and parent u; an already-labelled w switches to u's first hop (and
// parent u) when it ranks strictly better, and among parents carrying the same
// first hop the lower user ID wins. Layer k is final before layer k+1 is
// relaxed, so by induction the result is independent of adjacency and queue
// order and equals the best first hop over all shortest paths.
//
// Labels are stored as rank positions (index into s.l1), so the hot loop
// compares integers only.
func (g *Graph) Search(ctx context.Context, v core.UserID, ex core.Exclusions) (core.Search, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	// Under g.mu (read) nothing can intern, so idx/ids/order are stable here.
	n := len(g.ids)
	s := &search{
		g: g, viewer: v, vi: -1, epoch: g.epoch.Load(), ids: g.ids[:n:n],
		layers: [][]core.UserID{{v}},
	}
	vi, ok := g.idx[v]
	if !ok {
		s.initBitmaps()
		return s, nil
	}
	s.vi = vi
	s.dist = make([]uint16, n)
	for i := range s.dist {
		s.dist[i] = unreached
	}
	s.first = make([]int32, n)
	s.parent = make([]int32, n)
	s.dist[vi] = 0
	s.first[vi], s.parent[vi] = -1, -1
	s.own = g.rows[vi]

	blocked := ex.Blocked
	if blocked != nil && blocked.IsEmpty() {
		blocked = nil
	}
	hidden := ex.HiddenVia
	if hidden != nil && hidden.IsEmpty() {
		hidden = nil
	}

	curp := queuePool.Get().(*[]int32)
	nextp := queuePool.Get().(*[]int32)
	defer func() {
		*curp, *nextp = (*curp)[:0], (*nextp)[:0]
		queuePool.Put(curp)
		queuePool.Put(nextp)
	}()
	cur := append((*curp)[:0], vi)
	next := (*nextp)[:0]
	sizes := []int{1}
	for k := 0; len(cur) > 0; k++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if k+1 >= unreached {
			break // deeper than any representable distance (never at real sizes)
		}
		d := uint16(k + 1)
		next = next[:0]
		for _, u := range cur {
			if k > 0 && hidden != nil && hidden.Contains(uint32(s.ids[u])) {
				continue // transit only: reachable, never routed through
			}
			fu := s.first[u]
			for _, w := range g.rows[u].to {
				switch s.dist[w] {
				case unreached:
					if blocked != nil && blocked.Contains(uint32(s.ids[w])) {
						continue
					}
					s.dist[w] = d
					next = append(next, w)
					s.first[w], s.parent[w] = fu, u
				case d:
					if k == 0 {
						continue
					}
					switch fw := s.first[w]; {
					case fu < fw:
						s.first[w], s.parent[w] = fu, u
					case fu == fw && s.ids[u] < s.ids[s.parent[w]]:
						s.parent[w] = u
					}
				}
			}
		}
		if k == 0 {
			s.rankLayer1(next)
		}
		if len(next) == 0 {
			break
		}
		sizes = append(sizes, len(next))
		cur, next = next, cur
	}
	*curp, *nextp = cur, next

	// Layers sorted by ID in one pass over the ID-ordered dense indices.
	for k := 1; k < len(sizes); k++ {
		s.layers = append(s.layers, make([]core.UserID, 0, sizes[k]))
	}
	for _, i := range g.order[:n] {
		if dk := s.dist[i]; dk != unreached && dk > 0 {
			s.layers[dk] = append(s.layers[dk], s.ids[i])
		}
	}
	s.initBitmaps()
	return s, nil
}

// search is an immutable BFS result.
type search struct {
	g      *Graph
	viewer core.UserID
	vi     int32 // dense index of the viewer, -1 when the viewer has no edges
	epoch  uint64
	ids    []core.UserID // dense → user, as of the search

	dist   []uint16
	first  []int32 // rank position of the first hop (index into l1); -1 for the viewer
	parent []int32
	own    row     // the reader's own edges
	l1     []int32 // layer-1 dense indices in D22 rank order

	layers  [][]core.UserID // layer k sorted by ID; never modified after Search returns
	bitmaps []func() *roaring.Bitmap
}

var _ core.Search = (*search)(nil)

// rankLayer1 orders the reader's friends by the D22 rank and relabels every
// layer-1 node with its rank position.
func (s *search) rankLayer1(l1 []int32) {
	type cand struct {
		i     int32
		plus  bool
		since time.Time
		id    core.UserID
	}
	cs := make([]cand, 0, len(l1))
	for _, f := range l1 {
		c := cand{i: f, id: s.ids[f]}
		if k, ok := s.own.find(f); ok {
			c.plus, c.since = s.own.level[k] == core.LevelFriendPlus, s.own.since[k]
		}
		cs = append(cs, c)
	}
	slices.SortFunc(cs, func(a, b cand) int {
		if a.plus != b.plus {
			if a.plus {
				return -1
			}
			return 1
		}
		if c := a.since.Compare(b.since); c != 0 {
			return c
		}
		return cmpID(a.id, b.id)
	})
	s.l1 = make([]int32, len(cs))
	for pos, c := range cs {
		s.l1[pos] = c.i
		s.first[c.i] = int32(pos)
	}
}

// initBitmaps prepares the lazily built, cached layer bitmaps.
func (s *search) initBitmaps() {
	s.bitmaps = make([]func() *roaring.Bitmap, len(s.layers))
	for k := range s.layers {
		l := s.layers[k]
		s.bitmaps[k] = sync.OnceValue(func() *roaring.Bitmap {
			b := roaring.New()
			for _, u := range l {
				b.Add(uint32(u))
			}
			b.RunOptimize()
			return b
		})
	}
}

// index returns u's dense index if the search reached u.
func (s *search) index(u core.UserID) (int32, bool) {
	if s.vi < 0 {
		return 0, false
	}
	i, ok := s.g.lookup(u)
	if !ok || int(i) >= len(s.dist) || s.dist[i] == unreached {
		return 0, false
	}
	return i, true
}

func (s *search) Viewer() core.UserID { return s.viewer }
func (s *search) Epoch() uint64       { return s.epoch }

// Dist is the exact shortest distance avoiding the reader's exclusions.
func (s *search) Dist(u core.UserID) (int, bool) {
	if u == s.viewer {
		return 0, true
	}
	i, ok := s.index(u)
	if !ok {
		return 0, false
	}
	return int(s.dist[i]), true
}

// FirstHop is the chain's first hop (0 when dist ≤ 1 or unreachable).
func (s *search) FirstHop(u core.UserID) core.UserID {
	i, ok := s.index(u)
	if !ok || s.dist[i] <= 1 {
		return 0
	}
	return s.ids[s.l1[s.first[i]]]
}

// FirstHopPlus reports whether the reader's edge to u's first hop is friend+.
func (s *search) FirstHopPlus(u core.UserID) bool {
	i, ok := s.index(u)
	if !ok || s.dist[i] == 0 {
		return false
	}
	k, ok := s.own.find(s.l1[s.first[i]])
	return ok && s.own.level[k] == core.LevelFriendPlus
}

// Path is a shortest path viewer→u (inclusive) consistent with FirstHop.
func (s *search) Path(u core.UserID) []core.UserID {
	if u == s.viewer {
		return []core.UserID{s.viewer}
	}
	i, ok := s.index(u)
	if !ok {
		return nil
	}
	p := make([]core.UserID, 0, int(s.dist[i])+1)
	for ; i != s.vi; i = s.parent[i] {
		p = append(p, s.ids[i])
	}
	p = append(p, s.viewer)
	slices.Reverse(p)
	return p
}

// Layer is the exact set at distance k, sorted by ID (a fresh copy).
func (s *search) Layer(k int) []core.UserID {
	if k < 0 || k >= len(s.layers) {
		return nil
	}
	return slices.Clone(s.layers[k])
}

// LayerBitmap is Layer(k) as a fresh bitmap (callers may modify it).
func (s *search) LayerBitmap(k int) *roaring.Bitmap {
	if k < 0 || k >= len(s.bitmaps) {
		return roaring.New()
	}
	return s.bitmaps[k]().Clone()
}

// MaxLayer is the deepest non-empty layer.
func (s *search) MaxLayer() int { return len(s.layers) - 1 }
