package graph

import (
	"context"
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/RoaringBitmap/roaring/v2"
	"pgregory.net/rapid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// world is a random graph plus a reader and the reader's own exclusions.
type world struct {
	n      int
	edges  []edgeSpec
	reader core.UserID
	ex     core.Exclusions
	// blockers: users who blocked the reader (must change nothing, D7).
	blockers []core.UserID
}

type edgeSpec struct {
	a, b  core.UserID
	level core.EdgeLevel
	since time.Time
}

func uid(i int) core.UserID { return core.UserID(i + 1) }

func genWorld(t *rapid.T) world {
	n := rapid.IntRange(1, 40).Draw(t, "n")
	m := rapid.IntRange(0, n*3).Draw(t, "m")
	seen := map[[2]core.UserID]bool{}
	w := world{n: n, reader: uid(rapid.IntRange(0, n-1).Draw(t, "reader"))}
	for range m {
		a := uid(rapid.IntRange(0, n-1).Draw(t, "a"))
		b := uid(rapid.IntRange(0, n-1).Draw(t, "b"))
		if a == b {
			continue
		}
		k := [2]core.UserID{min(a, b), max(a, b)}
		if seen[k] {
			continue
		}
		seen[k] = true
		lvl := core.LevelFriend
		if rapid.IntRange(0, 4).Draw(t, "plus") == 0 {
			lvl = core.LevelFriendPlus
		}
		// Few distinct ages so the ID tie-break is exercised too.
		since := t0.Add(time.Duration(rapid.IntRange(0, 5).Draw(t, "since")) * time.Hour)
		w.edges = append(w.edges, edgeSpec{a, b, lvl, since})
	}
	w.ex = core.Exclusions{Blocked: roaring.New(), HiddenVia: roaring.New()}
	for range rapid.IntRange(0, n/3+1).Draw(t, "nblocks") {
		u := uid(rapid.IntRange(0, n-1).Draw(t, "blocked"))
		if u != w.reader {
			w.ex.Blocked.Add(uint32(u))
		}
	}
	for range rapid.IntRange(0, n/4+1).Draw(t, "nhide") {
		u := uid(rapid.IntRange(0, n-1).Draw(t, "hidden"))
		if u != w.reader {
			w.ex.HiddenVia.Add(uint32(u))
		}
	}
	for range rapid.IntRange(0, 3).Draw(t, "nblockers") {
		u := uid(rapid.IntRange(0, n-1).Draw(t, "blocker"))
		if u != w.reader {
			w.blockers = append(w.blockers, u)
		}
	}
	return w
}

// build adds the edges in the given order and records the reader's blocks,
// hide-vias and the blocks OF the reader.
func (w world) build(order []int) *Graph {
	g := New(&core.Deps{})
	for _, i := range order {
		e := w.edges[i]
		if i%2 == 0 {
			g.AddEdge(e.a, e.b, e.level, e.since)
		} else {
			g.AddEdge(e.b, e.a, e.level, e.since)
		}
	}
	w.ex.Blocked.Iterate(func(x uint32) bool { g.SetBlock(w.reader, core.UserID(x), true); return true })
	w.ex.HiddenVia.Iterate(func(x uint32) bool { g.SetHideVia(w.reader, core.UserID(x), true); return true })
	for _, b := range w.blockers {
		g.SetBlock(b, w.reader, true)
	}
	return g
}

// adj is the oracle's view of the world, built from the specs (not the graph).
func (w world) adj() Adjacency {
	a := Adjacency{}
	for _, e := range w.edges {
		a[e.a] = append(a[e.a], core.Edge{To: e.b, Level: e.level, Since: e.since})
		a[e.b] = append(a[e.b], core.Edge{To: e.a, Level: e.level, Since: e.since})
	}
	return a
}

func identity(n int) []int {
	o := make([]int, n)
	for i := range o {
		o[i] = i
	}
	return o
}

type tb interface {
	Helper()
	Fatal(args ...any)
}

func run(t tb, g *Graph, v core.UserID) core.Search {
	t.Helper()
	s, err := g.Search(context.Background(), v, g.Exclusions(v))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestDistEqualsReferenceBFS(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		w := genWorld(rt)
		g := w.build(identity(len(w.edges)))
		s := run(rt, g, w.reader)
		ref := ReferenceBFS(w.adj(), w.reader, w.ex)
		for i := range w.n + 2 { // +2: users the graph has never seen
			u := uid(i)
			d, ok := s.Dist(u)
			rd, rok := ref[u]
			if ok != rok || d != rd {
				rt.Fatalf("Dist(%d) = (%d,%v), reference (%d,%v)", u, d, ok, rd, rok)
			}
		}
		// Layers are exactly the distance classes, sorted, and bitmaps agree.
		total := 0
		for k := 0; k <= s.MaxLayer(); k++ {
			l := s.Layer(k)
			if len(l) == 0 {
				rt.Fatalf("empty layer %d ≤ MaxLayer %d", k, s.MaxLayer())
			}
			if !slices.IsSorted(l) {
				rt.Fatalf("layer %d not sorted", k)
			}
			bm := s.LayerBitmap(k)
			if int(bm.GetCardinality()) != len(l) {
				rt.Fatalf("layer %d bitmap mismatch", k)
			}
			for _, u := range l {
				if ref[u] != k || !bm.Contains(uint32(u)) {
					rt.Fatalf("user %d in layer %d, reference dist %d", u, k, ref[u])
				}
			}
			total += len(l)
		}
		if total != len(ref) {
			rt.Fatalf("layers hold %d users, reference reaches %d", total, len(ref))
		}
		if s.Layer(s.MaxLayer()+1) != nil || !s.LayerBitmap(-1).IsEmpty() {
			rt.Fatal("out-of-range layer not empty")
		}
	})
}

func TestFirstHopEqualsReferenceAndOrderIndependent(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		w := genWorld(rt)
		g := w.build(identity(len(w.edges)))
		s := run(rt, g, w.reader)
		ref := ReferenceFirstHop(w.adj(), w.reader, w.ex)

		perm := rapid.Permutation(identity(len(w.edges))).Draw(rt, "perm")
		g2 := w.build(perm)
		s2 := run(rt, g2, w.reader)

		for i := range w.n {
			u := uid(i)
			d, ok := s.Dist(u)
			fh := s.FirstHop(u)
			if ok && d >= 2 {
				if fh != ref[u] {
					rt.Fatalf("FirstHop(%d) = %d, reference %d", u, fh, ref[u])
				}
			} else if fh != 0 {
				rt.Fatalf("FirstHop(%d) = %d at dist (%d,%v), want 0", u, fh, d, ok)
			}
			if s2.FirstHop(u) != fh {
				rt.Fatalf("FirstHop(%d) depends on adjacency order: %d vs %d", u, fh, s2.FirstHop(u))
			}
			if !slices.Equal(s.Path(u), s2.Path(u)) {
				rt.Fatalf("Path(%d) depends on adjacency order: %v vs %v", u, s.Path(u), s2.Path(u))
			}
			if s.FirstHopPlus(u) != s2.FirstHopPlus(u) {
				rt.Fatalf("FirstHopPlus(%d) depends on order", u)
			}
		}
	})
}

func TestPathProperties(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		w := genWorld(rt)
		g := w.build(identity(len(w.edges)))
		s := run(rt, g, w.reader)
		for i := range w.n + 1 {
			u := uid(i)
			d, ok := s.Dist(u)
			p := s.Path(u)
			if !ok {
				if p != nil {
					rt.Fatalf("Path(%d) = %v for an unreachable user", u, p)
				}
				continue
			}
			if len(p)-1 != d {
				rt.Fatalf("len(Path(%d))-1 = %d, Dist %d", u, len(p)-1, d)
			}
			if p[0] != w.reader || p[len(p)-1] != u {
				rt.Fatalf("Path(%d) = %v has wrong endpoints", u, p)
			}
			for k := 0; k+1 < len(p); k++ {
				if _, adj := g.Level(p[k], p[k+1]); !adj {
					rt.Fatalf("Path(%d) = %v: %d and %d not adjacent", u, p, p[k], p[k+1])
				}
			}
			for k, x := range p {
				if k > 0 && w.ex.Blocked.Contains(uint32(x)) {
					rt.Fatalf("Path(%d) = %v goes through %d, whom the reader blocked", u, p, x)
				}
				if k > 0 && k < len(p)-1 && w.ex.HiddenVia.Contains(uint32(x)) {
					rt.Fatalf("Path(%d) = %v routes through hidden-via %d", u, p, x)
				}
			}
			if d >= 2 && p[1] != s.FirstHop(u) {
				rt.Fatalf("Path(%d) = %v inconsistent with FirstHop %d", u, p, s.FirstHop(u))
			}
			if d >= 1 {
				lvl, _ := g.Level(w.reader, p[1])
				if s.FirstHopPlus(u) != (lvl == core.LevelFriendPlus) {
					rt.Fatalf("FirstHopPlus(%d) wrong", u)
				}
			}
		}
	})
}

// D7: a user who blocked the reader is still reachable, at the same distance as
// if no such block existed.
func TestBlockedByOthersChangesNothing(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		w := genWorld(rt)
		with := run(rt, w.build(identity(len(w.edges))), w.reader)
		w2 := w
		w2.blockers = nil
		without := run(rt, w2.build(identity(len(w.edges))), w.reader)
		for i := range w.n {
			u := uid(i)
			d1, ok1 := with.Dist(u)
			d2, ok2 := without.Dist(u)
			if d1 != d2 || ok1 != ok2 || with.FirstHop(u) != without.FirstHop(u) {
				rt.Fatalf("a block of the reader changed the reader's view of %d", u)
			}
		}
	})
}

func TestBlockedUserNeverVisited(t *testing.T) {
	g := New(&core.Deps{})
	// 1–2–3 and 1–4–5–3: blocking 2 forces the long way round.
	for _, e := range [][2]core.UserID{{1, 2}, {2, 3}, {1, 4}, {4, 5}, {5, 3}} {
		g.AddEdge(e[0], e[1], core.LevelFriend, t0)
	}
	s := run(t, g, 1)
	if d, _ := s.Dist(3); d != 2 {
		t.Fatalf("dist(3) = %d, want 2", d)
	}
	g.SetBlock(1, 2, true)
	s = run(t, g, 1)
	if _, ok := s.Dist(2); ok {
		t.Fatal("blocked user is reachable")
	}
	if d, _ := s.Dist(3); d != 3 {
		t.Fatalf("dist(3) = %d, want 3 (around the block)", d)
	}
	if p := s.Path(3); !slices.Equal(p, []core.UserID{1, 4, 5, 3}) {
		t.Fatalf("path = %v", p)
	}
	// One direction only: 2 still reaches 1.
	s2 := run(t, g, 2)
	if d, ok := s2.Dist(1); !ok || d != 1 {
		t.Fatalf("blocked user's own view of the blocker changed: (%d,%v)", d, ok)
	}
	if !g.Blocks(1, 2) || g.Blocks(2, 1) {
		t.Fatal("Blocks is not one-directional")
	}
	g.SetBlock(1, 2, false)
	if g.Blocks(1, 2) || !g.Exclusions(1).Blocked.IsEmpty() {
		t.Fatal("unblock did not clear")
	}
}

func TestHideViaIsTransitOnly(t *testing.T) {
	g := New(&core.Deps{})
	for _, e := range [][2]core.UserID{{1, 2}, {2, 3}} {
		g.AddEdge(e[0], e[1], core.LevelFriend, t0)
	}
	g.SetHideVia(1, 2, true)
	s := run(t, g, 1)
	if d, ok := s.Dist(2); !ok || d != 1 {
		t.Fatal("hidden-via user must stay reachable")
	}
	if _, ok := s.Dist(3); ok {
		t.Fatal("search routed through a hidden-via user")
	}
}

func TestD22FirstHopRule(t *testing.T) {
	g := New(&core.Deps{})
	// Reader 1 has friends 2 (oldest), 3, 4 (friend+, newest); all reach 9 at dist 2.
	g.AddEdge(1, 3, core.LevelFriend, t0.Add(2*time.Hour))
	g.AddEdge(1, 2, core.LevelFriend, t0.Add(time.Hour))
	for _, f := range []core.UserID{2, 3} {
		g.AddEdge(f, 9, core.LevelFriend, t0)
	}
	if fh := run(t, g, 1).FirstHop(9); fh != 2 {
		t.Fatalf("oldest edge must win: first hop %d", fh)
	}
	g.AddEdge(1, 4, core.LevelFriendPlus, t0.Add(3*time.Hour))
	g.AddEdge(4, 9, core.LevelFriend, t0)
	s := run(t, g, 1)
	if fh := s.FirstHop(9); fh != 4 || !s.FirstHopPlus(9) {
		t.Fatalf("friend+ must win: first hop %d plus %v", fh, s.FirstHopPlus(9))
	}
	// Same age ⇒ lower ID; other people's edge attributes never matter.
	h := New(&core.Deps{})
	h.AddEdge(1, 6, core.LevelFriend, t0)
	h.AddEdge(1, 5, core.LevelFriend, t0)
	h.AddEdge(6, 9, core.LevelFriendPlus, t0.Add(-time.Hour))
	h.AddEdge(5, 9, core.LevelFriend, t0.Add(time.Hour))
	if fh := run(t, h, 1).FirstHop(9); fh != 5 {
		t.Fatalf("ID tie-break: first hop %d", fh)
	}
	// Dist ≤ 1 has no first hop.
	if s.FirstHop(2) != 0 || s.FirstHop(1) != 0 || s.FirstHop(77) != 0 {
		t.Fatal("first hop must be 0 at dist ≤ 1 or unreachable")
	}
}

func TestEpochBumps(t *testing.T) {
	g := New(&core.Deps{})
	e := g.Epoch()
	g.AddEdge(1, 2, core.LevelFriend, t0)
	if g.Epoch() != e {
		t.Fatal("an addition must not bump the epoch")
	}
	g.AddEdge(1, 2, core.LevelFriendPlus, t0)
	if g.Epoch() != e {
		t.Fatal("an upgrade must not bump the epoch")
	}
	steps := []struct {
		name string
		fn   func()
	}{
		{"downgrade", func() { g.AddEdge(1, 2, core.LevelFriend, t0) }},
		{"remove", func() { g.RemoveEdge(1, 2) }},
		{"remove absent", func() { g.RemoveEdge(1, 2) }},
		{"block", func() { g.SetBlock(1, 3, true) }},
		{"block again", func() { g.SetBlock(1, 3, true) }},
		{"hide-via", func() { g.SetHideVia(1, 4, true) }},
		{"reload", func() { g.LoadAdjacency(Adjacency{}) }},
	}
	for _, st := range steps {
		before := g.Epoch()
		st.fn()
		if g.Epoch() <= before {
			t.Fatalf("%s did not bump the epoch", st.name)
		}
	}
	before := g.Epoch()
	g.SetBlock(1, 3, false)
	g.SetHideVia(1, 4, false)
	if g.Epoch() != before {
		t.Fatal("removing a block or hide-via only widens; it must not bump")
	}
	s := run(t, g, 1)
	if s.Epoch() != g.Epoch() {
		t.Fatal("search must carry the graph epoch")
	}
}

func TestFriendsLevelDegree(t *testing.T) {
	g := New(&core.Deps{})
	g.AddEdge(1, 4, core.LevelFriend, t0.Add(time.Hour))
	g.AddEdge(1, 3, core.LevelFriend, t0.Add(time.Hour))
	g.AddEdge(2, 1, core.LevelFriend, t0)
	g.AddEdge(1, 5, core.LevelFriendPlus, t0.Add(5*time.Hour))
	g.AddEdge(1, 1, core.LevelFriend, t0) // ignored
	got := g.Friends(1)
	want := []core.UserID{5, 2, 3, 4}
	if len(got) != len(want) {
		t.Fatalf("friends = %v", got)
	}
	for i, e := range got {
		if e.To != want[i] {
			t.Fatalf("friends order = %v, want %v", got, want)
		}
	}
	// An upgrade keeps the original since.
	g.AddEdge(2, 1, core.LevelFriendPlus, t0.Add(100*time.Hour))
	if f := g.Friends(2); len(f) != 1 || !f[0].Since.Equal(t0) || f[0].Level != core.LevelFriendPlus {
		t.Fatalf("upgrade: %v", f)
	}
	if lvl, ok := g.Level(1, 2); !ok || lvl != core.LevelFriendPlus {
		t.Fatal("level is symmetric")
	}
	if _, ok := g.Level(3, 4); ok {
		t.Fatal("no edge 3–4")
	}
	if g.Degree(1) != 4 || g.Degree(99) != 0 || len(g.Friends(99)) != 0 {
		t.Fatal("degree")
	}
	g.RemoveEdge(4, 1)
	if g.Degree(1) != 3 || g.Degree(4) != 0 {
		t.Fatal("remove is symmetric")
	}
}

func TestSearchSnapshotIsStable(t *testing.T) {
	g := New(&core.Deps{})
	g.AddEdge(1, 2, core.LevelFriend, t0)
	g.AddEdge(2, 3, core.LevelFriend, t0)
	s := run(t, g, 1)
	g.RemoveEdge(2, 3)
	g.AddEdge(1, 7, core.LevelFriend, t0) // interns a new user after the search
	if d, ok := s.Dist(3); !ok || d != 2 {
		t.Fatal("a later mutation changed an existing search")
	}
	if _, ok := s.Dist(7); ok {
		t.Fatal("a later addition leaked into an existing search")
	}
	if p := s.Path(3); !slices.Equal(p, []core.UserID{1, 2, 3}) {
		t.Fatalf("path = %v", p)
	}
}

func TestLonelyReader(t *testing.T) {
	g := New(&core.Deps{})
	s := run(t, g, 42)
	if d, ok := s.Dist(42); !ok || d != 0 {
		t.Fatal("self is at distance 0")
	}
	if s.MaxLayer() != 0 || !slices.Equal(s.Layer(0), []core.UserID{42}) || !slices.Equal(s.Path(42), []core.UserID{42}) {
		t.Fatal("a reader without edges sees only themself")
	}
	if s.Viewer() != 42 {
		t.Fatal("viewer")
	}
}

func TestSearchHonoursContext(t *testing.T) {
	g := New(&core.Deps{})
	g.AddEdge(1, 2, core.LevelFriend, t0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := g.Search(ctx, 1, core.Exclusions{}); err == nil {
		t.Fatal("cancelled context must fail the search")
	}
}

func TestForgetOnAccountDeleted(t *testing.T) {
	d := &core.Deps{Bus: &core.Bus{}}
	g := New(d)
	if err := g.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	g.AddEdge(1, 2, core.LevelFriend, t0)
	g.AddEdge(2, 3, core.LevelFriend, t0)
	g.SetBlock(1, 4, true)
	g.SetBlock(2, 1, true)
	before := g.Epoch()
	d.Bus.Publish(core.AccountDeleted{ID: 1})
	if g.Degree(1) != 0 || g.Degree(2) != 1 || g.Blocks(1, 4) || g.Blocks(2, 1) || g.Epoch() <= before {
		t.Fatal("account deletion must drop the user's edges and blocks and bump the epoch")
	}
}

func TestLoadAdjacencyRoundTrip(t *testing.T) {
	adj := SynthAdjacency(Synth(300, "ws", 7), 1000, t0)
	g := New(&core.Deps{})
	g.LoadAdjacency(adj)
	got := g.Adjacency()
	for u, es := range adj {
		if len(got[u]) != len(es) {
			t.Fatalf("user %d: %d edges, want %d", u, len(got[u]), len(es))
		}
		for _, e := range es {
			if lvl, ok := g.Level(u, e.To); !ok || lvl != e.Level {
				t.Fatalf("edge %d–%d missing", u, e.To)
			}
		}
	}
}

func TestSynth(t *testing.T) {
	for _, model := range []string{"ba", "ws"} {
		a, b := Synth(2000, model, 11), Synth(2000, model, 11)
		if len(a) != 2000 {
			t.Fatalf("%s: %d nodes", model, len(a))
		}
		total := 0
		for i := range a {
			if !slices.Equal(a[i], b[i]) {
				t.Fatalf("%s: not deterministic", model)
			}
			if !slices.IsSorted(a[i]) {
				t.Fatalf("%s: unsorted adjacency", model)
			}
			for _, j := range a[i] {
				if int(j) == i {
					t.Fatalf("%s: self loop", model)
				}
				if _, ok := slices.BinarySearch(a[j], uint32(i)); !ok {
					t.Fatalf("%s: asymmetric %d–%d", model, i, j)
				}
			}
			total += len(a[i])
		}
		mean := float64(total) / 2000
		if mean < 5 || mean > 7 {
			t.Fatalf("%s: mean degree %.2f, want ≈ 6", model, mean)
		}
		if c := Synth(2000, model, 12); slices.Equal(c[1999], a[1999]) && slices.Equal(c[1000], a[1000]) && slices.Equal(c[5], a[5]) {
			t.Fatalf("%s: seed has no effect", model)
		}
	}
	if Synth(10, "nope", 1) != nil || Synth(0, "ba", 1) != nil {
		t.Fatal("unknown model / empty graph must be nil")
	}
	for _, n := range []int{1, 2, 3, 5, 7} {
		for _, model := range []string{"ba", "ws"} {
			if len(Synth(n, model, 1)) != n {
				t.Fatalf("%s n=%d", model, n)
			}
		}
	}
}

// The search on a synthetic BA graph agrees with the oracles for random readers.
func TestSynthAgreesWithReference(t *testing.T) {
	adj := SynthAdjacency(Synth(1500, "ba", 3), 1, t0)
	g := New(&core.Deps{})
	g.LoadAdjacency(adj)
	rng := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // test data
	for range 20 {
		v := core.UserID(1 + rng.IntN(1500))
		ex := core.Exclusions{Blocked: roaring.New(), HiddenVia: roaring.New()}
		for range 10 {
			if b := core.UserID(1 + rng.IntN(1500)); b != v {
				ex.Blocked.Add(uint32(b))
			}
		}
		s, err := g.Search(context.Background(), v, ex)
		if err != nil {
			t.Fatal(err)
		}
		dist := ReferenceBFS(adj, v, ex)
		first := ReferenceFirstHop(adj, v, ex)
		for u := core.UserID(1); u <= 1500; u++ {
			d, ok := s.Dist(u)
			rd, rok := dist[u]
			if ok != rok || d != rd {
				t.Fatalf("reader %d: Dist(%d) = (%d,%v), reference (%d,%v)", v, u, d, ok, rd, rok)
			}
			if ok && d >= 2 && s.FirstHop(u) != first[u] {
				t.Fatalf("reader %d: FirstHop(%d) = %d, reference %d", v, u, s.FirstHop(u), first[u])
			}
		}
	}
}

// Layers stay ID-sorted when bulk loads and single interns are mixed.
func TestLayersSortedAcrossInterns(t *testing.T) {
	g := New(&core.Deps{})
	g.LoadAdjacency(Adjacency{50: {{To: 40, Level: core.LevelFriend, Since: t0}}, 40: {{To: 60, Level: core.LevelFriend, Since: t0}}})
	g.AddEdge(50, 10, core.LevelFriend, t0)
	g.AddEdge(10, 5, core.LevelFriend, t0)
	g.AddEdge(40, 99, core.LevelFriend, t0)
	g.AddEdge(40, 7, core.LevelFriend, t0)
	s := run(t, g, 50)
	want := [][]core.UserID{{50}, {10, 40}, {5, 7, 60, 99}}
	if s.MaxLayer() != len(want)-1 {
		t.Fatalf("max layer %d", s.MaxLayer())
	}
	for k, l := range want {
		if got := s.Layer(k); !slices.Equal(got, l) {
			t.Fatalf("layer %d = %v, want %v", k, got, l)
		}
		bm := s.LayerBitmap(k)
		bm.Add(12345) // callers may modify their copy
		if s.LayerBitmap(k).Contains(12345) {
			t.Fatal("LayerBitmap must return a copy")
		}
	}
}
