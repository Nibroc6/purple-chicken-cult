package graph

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/RoaringBitmap/roaring/v2"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// Lazy layered search (ARCHITECTURE §4.1, plan §4.7 graph, P1-W03), behind
// PCC_LAZY_BFS=1.
//
//   - Laziness. A search starts with layer 1. Layers 2 and 3 are relaxed one
//     at a time, only when a caller asks for something at that depth (the
//     feed reaches the tier, a chain needs the distance). Everything deeper
//     — the "deep tiers" — is computed in one go, the first time anything at
//     depth ≥ DeepFrom is needed.
//   - Fixed-latency release. The deep part is released only at a whole
//     multiple of DeepBucket after it started (at least one bucket), so the
//     response time cannot tell how large the reader's component is (§2.2);
//     every deep search counts graph.deep_search.
//   - Caching. One search per reader is kept, keyed by (viewer, epoch) and
//     the reader's own exclusions, for CacheTTL; once its deep part ran, for
//     DeepReuse ("at most one deep search per reader per day"). Every epoch
//     bump — any edge removal, block, hide-via, downgrade or reload — drops
//     the whole cache; a change to the reader's own edges or the removal of
//     one of the reader's own blocks or hide-vias drops the reader's entry.
//     Other people's additions do not: a missed new path only delays content,
//     it never exposes anything (§4.1).
//
// Equivalence. Relaxing the layers one at a time runs exactly the same
// relaxation as the full search (search.go), so a lazy search over an
// unchanged graph answers exactly like the full BFS — distances, layers,
// D22 first hops and paths (TestLazyEqualsFullBFS). Graph rows are read under
// the graph's read lock at each step; a step after a later mutation sees the
// mutated rows, which can only drop or delay paths (removals bump the epoch,
// so such a search is no longer served from the cache).

// Tunables of the lazy search.
const (
	DeepFrom   = 4                      // the first layer of the deep tiers
	DeepBucket = 600 * time.Millisecond // release granularity of the deep tiers (plan §5.1)
	CacheTTL   = 30 * time.Minute       // lifetime of a cached search
	DeepReuse  = 24 * time.Hour         // lifetime once its deep tiers were computed
	CacheMax   = 1024                   // cached readers; the oldest entry goes first
)

// MetricDeepSearch counts deep searches (gate/metrics names, bucket "count").
const MetricDeepSearch = "graph.deep_search"

// lazy is the growth state of a lazy search.
type lazy struct {
	mu     sync.Mutex
	done   atomic.Bool
	cur    []int32      // the deepest relaxed layer (dense), the next to expand
	deepAt atomic.Int64 // clock time (unix ns) the deep tiers were released; 0 = not yet
}

// noop is the release of a hold that took no lock.
func noop() {}

// complete reports whether every layer is known.
func (s *search) complete() bool { return s.lz == nil || s.lz.done.Load() }

// hold makes layers 0..k available (all of them when the search ends sooner)
// and returns the function that releases the search for other goroutines.
// Readers of an incomplete lazy search call it before touching its state.
func (s *search) hold(k int) func() {
	lz := s.lz
	if lz == nil || lz.done.Load() {
		return noop
	}
	lz.mu.Lock()
	for !lz.done.Load() && len(s.layers) <= k {
		s.grow()
	}
	return lz.mu.Unlock
}

// grow computes the next layer, or all the deep ones. Callers hold lz.mu.
func (s *search) grow() {
	if len(s.layers) < DeepFrom {
		s.step()
		return
	}
	g := s.g
	start := time.Now()
	for !s.lz.done.Load() {
		s.step()
	}
	elapsed := time.Since(start)
	bucket := g.deepBucket
	release := bucket
	if bucket > 0 {
		release = ((elapsed + bucket - 1) / bucket) * bucket
		release = max(release, bucket)
	}
	if wait := release - elapsed; wait > 0 && g.sleep != nil {
		g.sleep(wait)
	}
	s.lz.deepAt.Store(g.now().UnixNano())
	if g.d != nil && g.d.Metrics != nil {
		g.d.Metrics.Inc(MetricDeepSearch, "count", 1)
	}
}

// step relaxes one layer. Callers hold lz.mu.
func (s *search) step() {
	g := s.g
	k := len(s.layers) - 1
	g.mu.RLock()
	next := s.relax(g.rows, k, s.lz.cur, nil)
	g.mu.RUnlock()
	if len(next) == 0 || k+1 >= unreached-1 {
		s.lz.cur = nil
		s.lz.done.Store(true)
		return
	}
	layer := make([]core.UserID, len(next))
	for j, i := range next {
		layer[j] = s.ids[i]
	}
	slices.Sort(layer)
	s.layers = append(s.layers, layer)
	s.bitmaps = append(s.bitmaps, layerBitmap(layer))
	s.lz.cur = next
}

// lazySearch starts a lazy search of v: the viewer and layer 1.
func (g *Graph) lazySearch(v core.UserID, ex core.Exclusions) *search {
	g.mu.RLock()
	s, start := g.begin(v, ex)
	g.mu.RUnlock()
	s.lz = &lazy{}
	if start < 0 {
		s.lz.done.Store(true)
		return s
	}
	s.lz.cur = []int32{start}
	s.initBitmaps()
	s.lz.mu.Lock()
	s.step()
	s.lz.mu.Unlock()
	return s
}

// ---- the per-reader cache ----

type cacheEntry struct {
	s               *search
	blocked, hidden *roaring.Bitmap // the exclusions the search ran with (copies)
	at              time.Time
}

func (e *cacheEntry) fresh(now time.Time) bool {
	if now.Sub(e.at) < CacheTTL {
		return true
	}
	deep := e.s.lz.deepAt.Load()
	return deep != 0 && now.Sub(time.Unix(0, deep)) < DeepReuse
}

// sameSet compares two exclusion sets; nil is empty.
func sameSet(a, b *roaring.Bitmap) bool {
	ea, eb := a == nil || a.IsEmpty(), b == nil || b.IsEmpty()
	if ea || eb {
		return ea == eb
	}
	return a.Equals(b)
}

func cloneSet(b *roaring.Bitmap) *roaring.Bitmap {
	if b == nil || b.IsEmpty() {
		return nil
	}
	return b.Clone()
}

// cached returns v's cached lazy search for ex at the current epoch, or a new
// one (which it caches).
func (g *Graph) cached(_ context.Context, v core.UserID, ex core.Exclusions) (core.Search, error) {
	now := g.now()
	ep := g.epoch.Load()
	g.cmu.Lock()
	if e := g.cache[v]; e != nil && e.s.epoch == ep && e.fresh(now) &&
		sameSet(e.blocked, ex.Blocked) && sameSet(e.hidden, ex.HiddenVia) {
		g.cmu.Unlock()
		return e.s, nil
	}
	g.cmu.Unlock()
	s := g.lazySearch(v, ex)
	g.cmu.Lock()
	defer g.cmu.Unlock()
	if s.epoch != g.epoch.Load() {
		return s, nil // already stale: serve it once, never cache it
	}
	if _, ok := g.cache[v]; !ok && len(g.cache) >= CacheMax {
		g.evictLocked(now)
	}
	g.cache[v] = &cacheEntry{s: s, blocked: cloneSet(ex.Blocked), hidden: cloneSet(ex.HiddenVia), at: now}
	return s, nil
}

// evictLocked drops stale entries, or the oldest one when none is stale.
// Callers hold g.cmu.
func (g *Graph) evictLocked(now time.Time) {
	var oldest core.UserID
	var at time.Time
	for v, e := range g.cache {
		if !e.fresh(now) {
			delete(g.cache, v)
			continue
		}
		if oldest == 0 || e.at.Before(at) {
			oldest, at = v, e.at
		}
	}
	if len(g.cache) >= CacheMax {
		delete(g.cache, oldest)
	}
}

// drop forgets the cached searches of us.
func (g *Graph) drop(us ...core.UserID) {
	g.cmu.Lock()
	for _, u := range us {
		delete(g.cache, u)
	}
	g.cmu.Unlock()
}

// bump advances the epoch and drops every cached search.
func (g *Graph) bump() {
	g.epoch.Add(1)
	g.cmu.Lock()
	clear(g.cache)
	g.cmu.Unlock()
}

// CachedReaders returns how many readers have a cached search (tests, metrics).
func (g *Graph) CachedReaders() int {
	g.cmu.Lock()
	defer g.cmu.Unlock()
	return len(g.cache)
}

// SetLazy turns the lazy search on or off regardless of PCC_LAZY_BFS (tests);
// turning it off drops the cache.
func (g *Graph) SetLazy(on bool) {
	v := int32(-1)
	if on {
		v = 1
	}
	g.lazyOverride.Store(v)
	g.cmu.Lock()
	clear(g.cache)
	g.cmu.Unlock()
}

// SetDeepRelease sets the deep-tier release bucket and how to wait for it
// (tests: a recorder instead of time.Sleep). bucket ≤ 0 releases at once.
func (g *Graph) SetDeepRelease(bucket time.Duration, sleep func(time.Duration)) {
	g.deepBucket, g.sleep = bucket, sleep
}

func (g *Graph) lazyOn() bool {
	switch g.lazyOverride.Load() {
	case 1:
		return true
	case -1:
		return false
	}
	return g.d != nil && g.d.Cfg != nil && g.d.Cfg.LazyBFS
}

func (g *Graph) now() time.Time {
	if g.d != nil && g.d.Clock != nil {
		return g.d.Clock.Now()
	}
	return time.Now()
}
