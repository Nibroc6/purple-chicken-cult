package visibility

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"pgregory.net/rapid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/graph"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coretest"
)

// Phase 1 graph facts (P1-W03): friend+ audiences, hide-via, friend+ first
// hops, the lazy search. Every check runs on the reference graph and on the
// real graph, full and lazy.

// graphs returns the graphs a P1 test runs on, mirrored from the world's
// reference graph: the reference itself, the real full BFS and the real lazy
// search.
func (e *env) graphs(users []core.UserID) map[string]core.Graph {
	out := map[string]core.Graph{"reference": e.m.Graph}
	for _, lazy := range []bool{false, true} {
		g := graph.New(&core.Deps{})
		g.SetLazy(lazy)
		g.SetDeepRelease(0, nil)
		adj := graph.Adjacency{}
		for _, u := range users {
			adj[u] = e.m.Graph.Friends(u)
		}
		g.LoadAdjacency(adj)
		for _, u := range users {
			ex := e.m.Graph.Exclusions(u)
			ex.Blocked.Iterate(func(x uint32) bool { g.SetBlock(u, core.UserID(x), true); return true })
			ex.HiddenVia.Iterate(func(x uint32) bool { g.SetHideVia(u, core.UserID(x), true); return true })
		}
		name := "full"
		if lazy {
			name = "lazy"
		}
		out[name] = g
	}
	return out
}

// each runs fn with every graph of e.graphs installed.
func (e *env) each(users []core.UserID, fn func(name string)) {
	e.t.Helper()
	saved := e.m.D.Graph
	defer func() { e.m.D.Graph = saved }()
	for _, name := range []string{"reference", "full", "lazy"} {
		e.m.D.Graph = e.graphs(users)[name]
		fn(name)
	}
}

func (e *env) visible(v core.UserID, p core.PostMeta) bool {
	e.t.Helper()
	_, ok := e.vis.CheckPost(ctx, e.search(v), p)
	return ok
}

// Acceptance: friend_plus posts are invisible to plain friends — and to
// everyone beyond distance 1, even along friend+ chains — and visible to
// friend+ friends and the author.
func TestFriendPlusPostsInvisibleToPlainFriends(t *testing.T) {
	e := newEnv(t)
	a, p, f, q, x := e.w.User("a"), e.w.User("p"), e.w.User("f"), e.w.User("q"), e.w.User("x")
	e.w.FriendsPlus("a", "p")
	e.w.Friends("a", "f")
	e.w.FriendsPlus("p", "q") // q is two friend+ hops away
	users := []core.UserID{a, p, f, q, x}
	pm := post("fp", a, core.AudFriendPlus)
	e.each(users, func(name string) {
		for _, c := range []struct {
			who  core.UserID
			want bool
		}{{a, true}, {p, true}, {f, false}, {q, false}, {x, false}} {
			if got := e.visible(c.who, pm); got != c.want {
				t.Fatalf("%s: friend_plus post seen by %d = %v, want %v", name, c.who, got, c.want)
			}
		}
	})
	// Upgrading the plain friend makes it visible; a downgrade hides it again.
	e.m.Graph.AddEdge(a, f, core.LevelFriendPlus, coretest.Epoch)
	e.each(users, func(name string) {
		if !e.visible(f, pm) {
			t.Fatalf("%s: an upgraded friend sees friend_plus posts", name)
		}
	})
	e.m.Graph.AddEdge(a, f, core.LevelFriend, coretest.Epoch)
	e.each(users, func(name string) {
		if e.visible(f, pm) {
			t.Fatalf("%s: a downgraded friend no longer sees friend_plus posts", name)
		}
	})
}

// Acceptance: hide-via never removes the hidden friend's own posts, only paths
// through them.
//
//	v – h – x – w      (w only through h and x)
//	v – y – z – x
func TestHideViaKeepsTheHiddenFriendsOwnPosts(t *testing.T) {
	e := newEnv(t)
	v, h, x, w, y, z := e.w.User("v"), e.w.User("h"), e.w.User("x"), e.w.User("w"), e.w.User("y"), e.w.User("z")
	for _, pr := range [][2]string{{"v", "h"}, {"h", "x"}, {"x", "w"}, {"v", "y"}, {"y", "z"}, {"z", "x"}} {
		e.w.Friends(pr[0], pr[1])
	}
	w2 := e.w.User("w2")
	e.w.Friends("h", "w2")
	users := []core.UserID{v, h, x, w, y, z, w2}
	hPosts := []core.PostMeta{post("h-all", h, core.AudConnected), post("h-d1", h, core.AudD1), post("h-d2", h, core.AudD2)}
	xD2, xAll := post("x-d2", x, core.AudD2), post("x-all", x, core.AudConnected)
	w2Post := post("w2", w2, core.AudConnected)
	e.each(users, func(name string) {
		if !e.visible(v, xD2) || !e.visible(v, w2Post) {
			t.Fatalf("%s: before the hide-via x is at 2 and w2 is reachable", name)
		}
	})
	e.m.Graph.SetHideVia(v, h, true)
	e.each(users, func(name string) {
		s := e.search(v)
		for _, p := range hPosts {
			if !e.visible(v, p) {
				t.Fatalf("%s: the hidden friend's own post %v disappeared", name, p.Audience)
			}
		}
		if d, _ := s.Dist(h); d != 1 {
			t.Fatalf("%s: the hidden friend stays at distance 1, got %d", name, d)
		}
		if d, _ := s.Dist(x); d != 3 || s.FirstHop(x) != y {
			t.Fatalf("%s: x is now reached around h: dist %d first hop %d", name, d, s.FirstHop(x))
		}
		if e.visible(v, xD2) || !e.visible(v, xAll) {
			t.Fatalf("%s: x's d2 post needs a path through h; x's open post does not", name)
		}
		if e.vis.CanSeeUser(s, w2) || e.visible(v, w2Post) {
			t.Fatalf("%s: someone reachable only through h is out of reach", name)
		}
		c, err := e.vis.Chain(ctx, s, w)
		if err != nil || c.FirstHop == nil || c.FirstHop.Handle != "y" || c.Dist != 4 {
			t.Fatalf("%s: chain to w avoids h: %+v %v", name, c, err)
		}
		// Hide-via is the reader's own: h's other friends are unaffected.
		if d, _ := e.search(h).Dist(w2); d != 1 {
			t.Fatalf("%s: h still sees w2", name)
		}
	})
	e.m.Graph.SetHideVia(v, h, false)
	e.each(users, func(name string) {
		if !e.visible(v, xD2) || !e.visible(v, w2Post) {
			t.Fatalf("%s: un-hiding restores the paths", name)
		}
	})
}

// Random worlds with the P1 graph facts — friend+ edges, the reader's blocks
// and hide-vias, other people's blocks — and posts of every audience: the
// real graph, full and lazy, gives the same CheckPost answers, chains (first
// hop and its friend+ flag) and CanSeeUser as the reference implementation.
func TestP1FactsAgreeWithReference(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		e := newEnv(t)
		n := rapid.IntRange(2, 12).Draw(rt, "n")
		users := make([]core.UserID, n)
		for i := range n {
			var opts []coretest.UserOpt
			if rapid.Bool().Draw(rt, "name") {
				opts = append(opts, coretest.NamedInChains())
			}
			users[i] = e.w.User(fmt.Sprintf("u%d", i), opts...)
		}
		seen := map[[2]int]bool{}
		for range rapid.IntRange(0, 3*n).Draw(rt, "m") {
			i, j := rapid.IntRange(0, n-1).Draw(rt, "a"), rapid.IntRange(0, n-1).Draw(rt, "b")
			if i == j || seen[[2]int{min(i, j), max(i, j)}] {
				continue
			}
			seen[[2]int{min(i, j), max(i, j)}] = true
			if rapid.IntRange(0, 2).Draw(rt, "plus") == 0 {
				e.w.FriendsPlus(fmt.Sprintf("u%d", i), fmt.Sprintf("u%d", j))
			} else {
				e.w.Friends(fmt.Sprintf("u%d", i), fmt.Sprintf("u%d", j))
			}
		}
		for range rapid.IntRange(0, n/2).Draw(rt, "facts") {
			i, j := rapid.IntRange(0, n-1).Draw(rt, "x"), rapid.IntRange(0, n-1).Draw(rt, "y")
			if i == j {
				continue
			}
			if rapid.Bool().Draw(rt, "hide") {
				e.m.Graph.SetHideVia(users[i], users[j], true)
			} else {
				e.m.Graph.SetBlock(users[i], users[j], true)
			}
		}
		auds := []core.Audience{core.AudConnected, core.AudD1, core.AudD2, core.AudD3, core.AudFriendPlus}
		var posts []core.PostMeta
		for i, u := range users {
			for _, a := range auds {
				posts = append(posts, post(fmt.Sprintf("%d-%d", i, a), u, a))
			}
		}
		type answer struct {
			seen    []bool
			users   []bool
			chains  []string
			firsts  []core.UserID
			layers  [][]core.UserID
			maxDist int
		}
		answers := map[string][]answer{}
		e.each(users, func(name string) {
			for _, v := range users {
				s := e.search(v)
				var a answer
				for _, p := range posts {
					_, ok := e.vis.CheckPost(ctx, s, p)
					a.seen = append(a.seen, ok)
				}
				for _, u := range users {
					a.users = append(a.users, e.vis.CanSeeUser(s, u))
					c, err := e.vis.Chain(ctx, s, u)
					b, _ := json.Marshal(c)
					a.chains = append(a.chains, fmt.Sprintf("%s %v", b, err))
					a.firsts = append(a.firsts, s.FirstHop(u))
				}
				a.maxDist = s.MaxLayer()
				for k := 0; k <= a.maxDist; k++ {
					a.layers = append(a.layers, s.Layer(k))
				}
				answers[name] = append(answers[name], a)
			}
		})
		for _, name := range []string{"full", "lazy"} {
			for i := range users {
				got, want := answers[name][i], answers["reference"][i]
				if !slices.Equal(got.seen, want.seen) || !slices.Equal(got.users, want.users) ||
					!slices.Equal(got.chains, want.chains) || !slices.Equal(got.firsts, want.firsts) ||
					got.maxDist != want.maxDist {
					rt.Fatalf("%s disagrees with the reference for reader u%d:\n got %+v\nwant %+v", name, i, got, want)
				}
				for k := range got.layers {
					if !slices.Equal(got.layers[k], want.layers[k]) {
						rt.Fatalf("%s layer %d of u%d: %v vs %v", name, k, i, got.layers[k], want.layers[k])
					}
				}
			}
		}
	})
}
