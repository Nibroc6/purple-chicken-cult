package graph

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/RoaringBitmap/roaring/v2"
	"pgregory.net/rapid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coreref"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/clock"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/config"
)

// lazyGraph turns g lazy with an immediate deep release.
func lazyGraph(g *Graph) *Graph {
	g.SetLazy(true)
	g.SetDeepRelease(0, nil)
	return g
}

// layersComputed is how many layers (0 included) a lazy search has so far.
func layersComputed(t *testing.T, s core.Search) int {
	t.Helper()
	ls, ok := s.(*search)
	if !ok || ls.lz == nil {
		t.Fatalf("not a lazy search: %T", s)
	}
	ls.lz.mu.Lock()
	defer ls.lz.mu.Unlock()
	return len(ls.layers)
}

// Acceptance: lazy == full BFS on random graphs — distances, layers, bitmaps,
// D22 first hops, friend+ flags and paths — whatever order the lazy search
// is asked in.
func TestLazyEqualsFullBFS(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		w := genWorld(rt)
		full := run(rt, w.build(identity(len(w.edges))), w.reader)
		lg := lazyGraph(w.build(identity(len(w.edges))))
		lazy := run(rt, lg, w.reader)
		if _, ok := lazy.(*search); !ok || lazy.(*search).lz == nil {
			rt.Fatal("PCC_LAZY_BFS must yield a lazy search")
		}
		// Ask in a random order: a probe of a random user, a random layer, …
		for _, op := range rapid.SliceOfN(rapid.IntRange(0, 2*w.n+8), 0, 6).Draw(rt, "probes") {
			if op < w.n+2 {
				lazy.Dist(uid(op))
			} else {
				lazy.Layer(op - w.n - 2)
			}
		}
		ref := ReferenceBFS(w.adj(), w.reader, w.ex)
		for i := range w.n + 2 {
			u := uid(i)
			fd, fok := full.Dist(u)
			ld, lok := lazy.Dist(u)
			if fd != ld || fok != lok {
				rt.Fatalf("Dist(%d): lazy (%d,%v), full (%d,%v)", u, ld, lok, fd, fok)
			}
			if rd, rok := ref[u]; rok != lok || (lok && rd != ld) {
				rt.Fatalf("Dist(%d): lazy (%d,%v), reference (%d,%v)", u, ld, lok, rd, rok)
			}
			if full.FirstHop(u) != lazy.FirstHop(u) || full.FirstHopPlus(u) != lazy.FirstHopPlus(u) {
				rt.Fatalf("first hop of %d: lazy %d/%v, full %d/%v", u, lazy.FirstHop(u), lazy.FirstHopPlus(u),
					full.FirstHop(u), full.FirstHopPlus(u))
			}
			if !slices.Equal(full.Path(u), lazy.Path(u)) {
				rt.Fatalf("Path(%d): lazy %v, full %v", u, lazy.Path(u), full.Path(u))
			}
		}
		if full.MaxLayer() != lazy.MaxLayer() {
			rt.Fatalf("MaxLayer: lazy %d, full %d", lazy.MaxLayer(), full.MaxLayer())
		}
		for k := -1; k <= full.MaxLayer()+1; k++ {
			if !slices.Equal(full.Layer(k), lazy.Layer(k)) || !full.LayerBitmap(k).Equals(lazy.LayerBitmap(k)) {
				rt.Fatalf("layer %d: lazy %v, full %v", k, lazy.Layer(k), full.Layer(k))
			}
		}
		if lazy.Epoch() != lg.Epoch() {
			rt.Fatal("a lazy search carries the graph epoch")
		}
	})
}

// chainGraph is 1 – 2 – … – n (friends, one hour apart).
func chainGraph(n int) *Graph {
	g := New(&core.Deps{Metrics: &coreref.MemMetrics{}})
	for i := 1; i < n; i++ {
		g.AddEdge(core.UserID(i), core.UserID(i+1), core.LevelFriend, t0.Add(time.Duration(i)*time.Hour))
	}
	return g
}

// Layers 2 and 3 are relaxed one at a time on demand; the deep tiers are
// computed in one go, released at a whole bucket, and counted.
func TestLazyGrowsOnDemand(t *testing.T) {
	g := chainGraph(9)
	var waits []time.Duration
	g.SetLazy(true)
	g.SetDeepRelease(DeepBucket, func(d time.Duration) { waits = append(waits, d) })
	s := run(t, g, 1)
	if n := layersComputed(t, s); n != 2 {
		t.Fatalf("a new search holds the viewer and layer 1, got %d layers", n)
	}
	if d, ok := s.Dist(3); !ok || d != 2 || layersComputed(t, s) != 3 {
		t.Fatalf("Dist(3) = %d,%v after %d layers", d, ok, layersComputed(t, s))
	}
	if l := s.Layer(3); !slices.Equal(l, []core.UserID{4}) || layersComputed(t, s) != 4 {
		t.Fatalf("Layer(3) = %v after %d layers", l, layersComputed(t, s))
	}
	if len(waits) != 0 {
		t.Fatal("shallow layers are not padded")
	}
	m := g.d.Metrics.(*coreref.MemMetrics)
	if m.Counts[MetricDeepSearch+"|count"] != 0 {
		t.Fatal("no deep search yet")
	}
	// Anything at depth ≥ 4 computes every remaining layer at once.
	if d, ok := s.Dist(5); !ok || d != 4 {
		t.Fatalf("Dist(5) = %d,%v", d, ok)
	}
	if !s.(*search).complete() || s.MaxLayer() != 8 {
		t.Fatalf("deep tiers must be computed in one go (max layer %d)", s.MaxLayer())
	}
	if len(waits) != 1 || waits[0] <= 0 || waits[0] > DeepBucket {
		t.Fatalf("deep release waits %v, want one wait up to the bucket", waits)
	}
	if m.Counts[MetricDeepSearch+"|count"] != 1 {
		t.Fatalf("graph.deep_search = %d", m.Counts[MetricDeepSearch+"|count"])
	}
	// An unreachable user completes the search too, once.
	if _, ok := s.Dist(99); ok || len(waits) != 1 {
		t.Fatal("a complete search answers without growing")
	}
	// A shallow-only reader never pays the deep release.
	h := chainGraph(3)
	h.SetLazy(true)
	var hw []time.Duration
	h.SetDeepRelease(DeepBucket, func(d time.Duration) { hw = append(hw, d) })
	hs := run(t, h, 2)
	if hs.MaxLayer() != 1 || len(hw) != 0 {
		t.Fatalf("max layer %d, waits %v", hs.MaxLayer(), hw)
	}
}

// A lonely reader's lazy search is complete at once.
func TestLazyLonelyReader(t *testing.T) {
	g := lazyGraph(New(&core.Deps{}))
	s := run(t, g, 5)
	if d, ok := s.Dist(5); !ok || d != 0 || s.MaxLayer() != 0 || !slices.Equal(s.Layer(0), []core.UserID{5}) {
		t.Fatal("lonely lazy reader")
	}
	if _, ok := s.Dist(6); ok || s.FirstHop(6) != 0 || s.Path(6) != nil || s.FirstHopPlus(6) {
		t.Fatal("nobody else is reachable")
	}
}

// cacheGraph is a lazy chain graph on a fake clock.
func cacheGraph(n int) (*Graph, *clock.Fake) {
	c := clock.NewFake(t0)
	g := New(&core.Deps{Clock: c, Metrics: &coreref.MemMetrics{}})
	for i := 1; i < n; i++ {
		g.AddEdge(core.UserID(i), core.UserID(i+1), core.LevelFriend, t0)
	}
	return lazyGraph(g), c
}

// Acceptance: a cached search is invalidated by any removal anywhere (the
// epoch), and by changes to the reader's own edges and exclusions; other
// people's additions keep it.
func TestCachedSearchInvalidatedByAnyRemoval(t *testing.T) {
	ctx := context.Background()
	search := func(g *Graph, v core.UserID) core.Search {
		t.Helper()
		s, err := g.Search(ctx, v, g.Exclusions(v))
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	removals := []struct {
		name string
		fn   func(g *Graph)
	}{
		{"edge removal far away", func(g *Graph) { g.RemoveEdge(7, 8) }},
		{"someone else's block", func(g *Graph) { g.SetBlock(6, 7, true) }},
		{"someone else's hide-via", func(g *Graph) { g.SetHideVia(6, 7, true) }},
		{"a downgrade", func(g *Graph) { g.AddEdge(4, 5, core.LevelFriend, t0) }},
		{"reload", func(g *Graph) { g.LoadAdjacency(g.Adjacency()) }},
		{"account deletion", func(g *Graph) { g.forget(8) }},
	}
	for _, r := range removals {
		g, _ := cacheGraph(8)
		g.AddEdge(4, 5, core.LevelFriendPlus, t0) // for the downgrade
		s := search(g, 1)
		if search(g, 1) != s {
			t.Fatalf("%s: the same reader at the same epoch shares the search", r.name)
		}
		r.fn(g)
		if g.CachedReaders() != 0 || search(g, 1) == s {
			t.Fatalf("%s must drop every cached search", r.name)
		}
	}

	g, _ := cacheGraph(8)
	s := search(g, 1)
	g.AddEdge(5, 9, core.LevelFriend, t0)     // someone else's addition
	g.SetBlock(6, 7, false)                   // someone else's (absent) unblock
	g.AddEdge(8, 2, core.LevelFriendPlus, t0) // touches reader 2, not 1
	if search(g, 1) != s {
		t.Fatal("other people's additions keep the cached search")
	}
	g.AddEdge(1, 9, core.LevelFriend, t0)
	s2 := search(g, 1)
	if s2 == s {
		t.Fatal("the reader's own new edge must drop the reader's entry")
	}
	if d, ok := s2.Dist(9); !ok || d != 1 {
		t.Fatal("the new friend is at distance 1")
	}
	// The reader's own unblock / un-hide drops the entry (no epoch bump).
	g.SetBlock(1, 3, true)
	s3 := search(g, 1)
	g.SetBlock(1, 3, false)
	if s4 := search(g, 1); s4 == s3 {
		t.Fatal("the reader's own unblock must drop the entry")
	}
	g.SetHideVia(1, 2, true)
	s5 := search(g, 1)
	g.SetHideVia(1, 2, false)
	if search(g, 1) == s5 {
		t.Fatal("the reader's own un-hide must drop the entry")
	}
	// Exclusions other than the cached ones never share the search.
	s6 := search(g, 1)
	other, err := g.Search(ctx, 1, core.Exclusions{Blocked: roaring.BitmapOf(2)})
	if err != nil || other == s6 {
		t.Fatal("a search with other exclusions is separate")
	}
	if _, ok := other.Dist(2); ok {
		t.Fatal("the other exclusions apply")
	}

	// TTL: 30 minutes; a search whose deep tiers ran is reused for a day.
	g, c := cacheGraph(8)
	s = search(g, 1)
	c.Advance(CacheTTL - time.Second)
	if search(g, 1) != s {
		t.Fatal("still fresh")
	}
	c.Advance(2 * time.Second)
	s = search(g, 1)
	if s.MaxLayer() != 7 { // deep tiers run now
		t.Fatal("max layer")
	}
	c.Advance(2 * time.Hour)
	if search(g, 1) != s {
		t.Fatal("a deep search is reused for a day")
	}
	c.Advance(DeepReuse)
	if search(g, 1) == s {
		t.Fatal("a day later the deep search runs again")
	}
	if n := g.d.Metrics.(*coreref.MemMetrics).Counts[MetricDeepSearch+"|count"]; n != 1 {
		t.Fatalf("deep searches = %d", n)
	}
	// The full search is never cached.
	g.SetLazy(false)
	if g.CachedReaders() != 0 || search(g, 1) == search(g, 1) {
		t.Fatal("without PCC_LAZY_BFS every call is a fresh full BFS")
	}
}

// The cache stays bounded.
func TestCacheBounded(t *testing.T) {
	g, c := cacheGraph(3)
	for v := range core.UserID(CacheMax + 10) {
		c.Advance(time.Millisecond)
		run(t, g, v+1)
	}
	if n := g.CachedReaders(); n > CacheMax {
		t.Fatalf("%d cached readers, max %d", n, CacheMax)
	}
}

// PCC_LAZY_BFS selects the lazy search.
func TestLazyFlag(t *testing.T) {
	g := New(&core.Deps{})
	if g.lazyOn() {
		t.Fatal("off by default")
	}
	g.d.Cfg = &config.Config{LazyBFS: true}
	if !g.lazyOn() {
		t.Fatal("PCC_LAZY_BFS=1")
	}
	g.SetLazy(false)
	if g.lazyOn() {
		t.Fatal("override")
	}
}

// Concurrent readers of one lazy search agree (run with -race in CI).
func TestLazyConcurrentReaders(t *testing.T) {
	nodes := Synth(400, "ba", 3)
	g := lazyGraph(New(&core.Deps{}))
	g.LoadAdjacency(SynthAdjacency(nodes, 1, t0))
	full := New(&core.Deps{})
	full.LoadAdjacency(SynthAdjacency(nodes, 1, t0))
	want := run(t, full, 1)
	s := run(t, g, 1)
	var wg sync.WaitGroup
	errs := make(chan string, 16)
	for w := range 8 {
		wg.Go(func() {
			for i := range 400 {
				u := core.UserID(1 + (i*7+w*13)%400)
				d, ok := s.Dist(u)
				wd, wok := want.Dist(u)
				if d != wd || ok != wok || s.FirstHop(u) != want.FirstHop(u) {
					errs <- "mismatch"
					return
				}
				if i%50 == 0 {
					_ = s.Layer(i % 6)
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}

// Hide-via is transit only (D7, ARCHITECTURE §4 rule 2): on random graphs the
// hidden person keeps their own distance, nobody gets closer, and anyone with
// a shortest path avoiding them keeps their distance.
func TestHideViaKeepsTheHiddenReachable(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		w := genWorld(rt)
		w.ex.HiddenVia = roaring.New()
		base := ReferenceBFS(w.adj(), w.reader, w.ex)
		h := uid(rapid.IntRange(0, w.n-1).Draw(rt, "hidden"))
		if h == w.reader {
			return
		}
		w.ex.HiddenVia.Add(uint32(h))
		g := w.build(identity(len(w.edges)))
		for _, lz := range []bool{false, true} {
			g.SetLazy(lz)
			g.SetDeepRelease(0, nil)
			s := run(rt, g, w.reader)
			hd, hok := s.Dist(h)
			if bd, bok := base[h]; hok != bok || hd != bd {
				rt.Fatalf("hidden %d: dist (%d,%v), before (%d,%v)", h, hd, hok, bd, bok)
			}
			for i := range w.n {
				u := uid(i)
				d, ok := s.Dist(u)
				if bd, bok := base[u]; ok && (!bok || d < bd) {
					rt.Fatalf("%d got closer through a hide-via: %d < %d", u, d, bd)
				}
				if ok && d >= 2 {
					for _, p := range s.Path(u)[1 : len(s.Path(u))-1] {
						if p == h {
							rt.Fatalf("path to %d routes through the hidden %d", u, h)
						}
					}
				}
			}
		}
	})
}

// Acceptance: the first hop prefers a friend+ edge over an older plain edge,
// lazily or not.
func TestFirstHopPrefersFriendPlusOverOlderEdge(t *testing.T) {
	for _, lz := range []bool{false, true} {
		g := New(&core.Deps{})
		g.SetLazy(lz)
		g.AddEdge(1, 2, core.LevelFriend, t0)                       // oldest plain edge
		g.AddEdge(1, 3, core.LevelFriendPlus, t0.Add(48*time.Hour)) // newer friend+
		g.AddEdge(2, 9, core.LevelFriend, t0)
		g.AddEdge(3, 9, core.LevelFriend, t0)
		g.AddEdge(9, 10, core.LevelFriend, t0)
		s := run(t, g, 1)
		for _, u := range []core.UserID{9, 10} {
			if s.FirstHop(u) != 3 || !s.FirstHopPlus(u) {
				t.Fatalf("lazy=%v: first hop of %d = %d (plus %v), want the friend+ 3", lz, u, s.FirstHop(u), s.FirstHopPlus(u))
			}
		}
		if !s.FirstHopPlus(3) || s.FirstHopPlus(2) {
			t.Fatal("at distance 1 the flag is the reader's own edge")
		}
	}
}
