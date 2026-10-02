package visibility

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coreref"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coretest"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/ids"
)

var ctx = context.Background()

type env struct {
	t   *testing.T
	m   *coretest.Mem
	w   *coretest.World
	vis *Service
}

func newEnv(t *testing.T) *env {
	t.Helper()
	m := coretest.NewMem(t)
	vis := New(m.D)
	m.D.Vis, m.D.Chains = vis, vis
	return &env{t: t, m: m, w: coretest.NewWorld(t, m.D, nil), vis: vis}
}

func (e *env) search(v core.UserID) core.Search {
	e.t.Helper()
	s, err := e.vis.SearchFor(ctx, v)
	if err != nil {
		e.t.Fatal(err)
	}
	return s
}

func (e *env) setStatus(u core.UserID, st uint8) {
	m, err := e.m.Users.Get(ctx, u)
	if err != nil {
		e.t.Fatal(err)
	}
	m.Status = st
	e.m.Users.Put(m)
}

func post(id string, author core.UserID, aud core.Audience) core.PostMeta {
	return core.PostMeta{ID: ids.Named("p:" + id), Author: author, Audience: aud, CreatedAt: coretest.Epoch, Version: 1}
}

// ---- CheckPost truth table ----

// TestCheckPostTruthTable enumerates audience × dist × reader-blocks ×
// author-blocks-reader × probation-forced × deleted × author status (× the
// reader's edge level at dist 1) against an independently written oracle.
func TestCheckPostTruthTable(t *testing.T) {
	e := newEnv(t)
	v := e.w.User("v")
	// A path v – a1 – a2 – a3 – a4, plus x with no path at all.
	authors := map[int]core.UserID{0: v}
	prev := "v"
	for k := 1; k <= 4; k++ {
		h := fmt.Sprintf("a%d", k)
		authors[k] = e.w.User(h)
		e.w.Friends(prev, h)
		prev = h
	}
	const inf = -1
	authors[inf] = e.w.User("x")

	audiences := []core.Audience{core.AudConnected, core.AudD1, core.AudD2, core.AudD3, core.AudFriendPlus, core.Audience(7)}
	statuses := []uint8{core.StatusActive, core.StatusSuspended, core.StatusDeleted, core.StatusPendingEnrollment}
	cases := 0
	for _, dist := range []int{0, 1, 2, 3, 4, inf} {
		author := authors[dist]
		for _, plus := range []bool{false, true} {
			if plus && dist != 1 {
				continue
			}
			for _, readerBlocks := range []bool{false, true} {
				if readerBlocks && dist == 0 {
					continue // nobody blocks themself
				}
				for _, authorBlocks := range []bool{false, true} {
					if authorBlocks && dist == 0 {
						continue
					}
					for _, aud := range audiences {
						for _, forced := range []bool{false, true} {
							for _, deleted := range []bool{false, true} {
								for _, st := range statuses {
									lvl := core.LevelFriend
									if plus {
										lvl = core.LevelFriendPlus
									}
									if dist == 1 {
										e.m.Graph.AddEdge(v, author, lvl, coretest.Epoch)
									}
									e.m.Graph.SetBlock(v, author, readerBlocks)
									e.m.Graph.SetBlock(author, v, authorBlocks)
									e.setStatus(author, st)
									p := post("t", author, aud)
									p.AudienceForced, p.Deleted = forced, deleted

									want := oracleCheck(dist, plus, readerBlocks, aud, forced, deleted, st)
									s := e.search(v)
									c, got := e.vis.CheckPost(ctx, s, p)
									if got != want {
										t.Fatalf("dist=%d plus=%v readerBlocks=%v authorBlocks=%v aud=%d forced=%v deleted=%v status=%d: got %v want %v",
											dist, plus, readerBlocks, authorBlocks, aud, forced, deleted, st, got, want)
									}
									if got && (c.Meta().ID != p.ID || c.Viewer() != v || c.Epoch() != s.Epoch()) {
										t.Fatal("Checked does not carry the post, viewer and epoch")
									}
									if batch := e.vis.CheckPosts(ctx, s, []core.PostMeta{p}); (len(batch) == 1) != want {
										t.Fatal("CheckPosts disagrees with CheckPost")
									}
									cases++
									e.m.Graph.SetBlock(v, author, false)
									e.m.Graph.SetBlock(author, v, false)
									e.setStatus(author, core.StatusActive)
									if dist == 1 {
										e.m.Graph.AddEdge(v, author, core.LevelFriend, coretest.Epoch)
									}
								}
							}
						}
					}
				}
			}
		}
	}
	e.setStatus(authors[1], core.StatusActive)
	if cases < 2000 {
		t.Fatalf("only %d cases", cases)
	}
}

// oracleCheck is §4 rules 1, 2, 4, 5 written from the spec, independently of
// the implementation. The author blocking the reader never matters (D7).
func oracleCheck(dist int, plus, readerBlocks bool, aud core.Audience, forced, deleted bool, st uint8) bool {
	if deleted || st != core.StatusActive || dist < 0 || readerBlocks {
		return false
	}
	limit := map[core.Audience]int{core.AudConnected: 1 << 30, core.AudD1: 1, core.AudD2: 2, core.AudD3: 3}
	if aud == core.AudFriendPlus {
		return dist == 0 || (dist == 1 && plus)
	}
	l, ok := limit[aud]
	if !ok {
		return false
	}
	if forced {
		l = min(l, 1)
	}
	return dist <= l
}

func TestCheckPostFailsClosedOnUnknownAuthor(t *testing.T) {
	e := newEnv(t)
	v := e.w.User("v")
	ghost := core.UserID(999)
	e.m.Graph.AddEdge(v, ghost, core.LevelFriend, coretest.Epoch) // in the graph, not in Users
	if _, ok := e.vis.CheckPost(ctx, e.search(v), post("g", ghost, core.AudConnected)); ok {
		t.Fatal("a post by an unknown author must fail closed")
	}
	if _, ok := e.vis.CheckPost(ctx, nil, post("g", v, core.AudConnected)); ok {
		t.Fatal("no search ⇒ nothing visible")
	}
	if _, err := e.vis.SearchFor(ctx, 0); !errors.Is(err, gate.ErrNotFound) {
		t.Fatal("SearchFor(0) must be ErrNotFound")
	}
}

// ---- D7 ----

func TestBlockIsOneDirectional(t *testing.T) {
	e := newEnv(t)
	v, a, b := e.w.User("v"), e.w.User("a"), e.w.User("b")
	e.w.Friends("v", "a")
	e.w.Friends("a", "b")
	e.w.Block("b", "v") // b blocked the reader
	s := e.search(v)
	if !e.vis.CanSeeUser(s, b) {
		t.Fatal("a user who blocked the reader must stay reachable (D7)")
	}
	pb := post("pb", b, core.AudConnected)
	if _, ok := e.vis.CheckPost(ctx, s, pb); !ok {
		t.Fatal("posts of a user who blocked the reader stay visible (D7)")
	}
	if !e.vis.CanReference(ctx, s, pb, core.LinkRef) {
		t.Fatal("the reader can still reference them (shadow link)")
	}
	// b, the blocker, no longer sees v.
	sb := e.search(b)
	if e.vis.CanSeeUser(sb, v) {
		t.Fatal("the blocker must not reach the blocked user")
	}
	if _, ok := e.vis.CheckPost(ctx, sb, post("pv", v, core.AudConnected)); ok {
		t.Fatal("the blocker must not see the blocked user's posts")
	}
	if !e.vis.CanSeeUser(sb, a) {
		t.Fatal("the blocker still sees everyone else")
	}
}

// ---- §10.1.3 CanSeeLink ----

func TestCanSeeLinkTable(t *testing.T) {
	e := newEnv(t)
	v := e.w.User("v")
	f := e.w.User("f")   // v's friend
	pa := e.w.User("pa") // parent author, v's friend
	d2 := e.w.User("d2") // at distance 2 from v
	far := e.w.User("far")
	e.w.Friends("v", "f")
	e.w.Friends("v", "pa")
	e.w.Friends("f", "d2")
	e.w.Friends("pa", "far")

	type parentKind struct {
		name string
		mod  func(*core.PostMeta)
		open bool
	}
	parents := []parentKind{
		{"open", func(*core.PostMeta) {}, true},
		{"d3", func(p *core.PostMeta) { p.Audience = core.AudD3 }, false},
		{"forced", func(p *core.PostMeta) { p.Audience, p.AudienceForced = core.AudD1, true }, false},
		{"two_hops", func(p *core.PostMeta) { p.RefPolicy = core.PolTwoHops }, false},
		{"friends", func(p *core.PostMeta) { p.RefPolicy = core.PolFriends }, false},
		{"nobody", func(p *core.PostMeta) { p.RefPolicy = core.PolNobody }, false},
	}
	childAuthors := []struct {
		name   string
		id     core.UserID
		exempt bool // V, P's author or V's friend
	}{{"viewer", v, true}, {"parent-author", pa, true}, {"friend", f, true}, {"dist2", d2, false}, {"far", far, false}}

	n := 0
	for _, pk := range parents {
		for _, ca := range childAuthors {
			for _, childVisible := range []bool{true, false} {
				for _, parentVisible := range []bool{true, false} {
					for _, detached := range []bool{false, true} {
						for _, shadow := range []bool{false, true} {
							n++
							parent := post(fmt.Sprintf("P%d", n), pa, core.AudConnected)
							pk.mod(&parent)
							parent.Deleted = !parentVisible
							child := post(fmt.Sprintf("C%d", n), ca.id, core.AudConnected)
							child.Deleted = !childVisible
							l := core.LinkMeta{Child: child.ID, Parent: parent.ID, Kind: core.LinkRef, ChildAuthor: ca.id,
								ParentAuthor: pa, Detached: detached, Shadow: shadow}
							want := childVisible && parentVisible && !detached && (!shadow || ca.id == v) && (pk.open || ca.exempt)
							got := e.vis.CanSeeLink(ctx, e.search(v), l, child, parent)
							if got != want {
								t.Fatalf("parent=%s child=%s childVisible=%v parentVisible=%v detached=%v shadow=%v: got %v want %v",
									pk.name, ca.name, childVisible, parentVisible, detached, shadow, got, want)
							}
						}
					}
				}
			}
		}
	}
	// A link whose IDs do not match the posts given is never shown.
	p, c := post("x1", pa, core.AudConnected), post("x2", f, core.AudConnected)
	if e.vis.CanSeeLink(ctx, e.search(v), core.LinkMeta{Child: c.ID, Parent: ids.Named("other")}, c, p) {
		t.Fatal("mismatched link")
	}
}

// ---- VisibleChildren / VisibleParents ----

func TestVisibleChildrenLimitAfterFiltering(t *testing.T) {
	e := newEnv(t)
	v := e.w.User("v")
	e.w.User("a")
	e.w.User("stranger") // no path to v
	e.w.User("probie")
	e.w.Friends("v", "a")
	e.w.Friends("a", "probie")
	base := coretest.Epoch.Add(-time.Hour)
	e.w.Post("a", "parent", "root", coretest.At(base))
	// 30 visible children (older), then 50 newer invisible ones: 25 by a stranger,
	// 25 forced-d1 posts by someone at distance 2.
	for i := range 30 {
		e.w.Post("a", fmt.Sprintf("vis%02d", i), "ok", e.w.ReplyTo("parent"), coretest.At(base.Add(time.Duration(i+1)*time.Second)))
	}
	for i := range 50 {
		author, opts := "stranger", []coretest.PostOpt{}
		if i%2 == 1 {
			author = "probie"
			opts = append(opts, func(m *core.PostMeta, _ *[]core.LinkMeta) { m.Audience, m.AudienceForced = core.AudD1, true })
		}
		opts = append(opts, e.w.ReplyTo("parent"), coretest.At(base.Add(time.Duration(100+i)*time.Second)))
		e.w.Post(author, fmt.Sprintf("hid%02d", i), "no", opts...)
	}
	s := e.search(v)
	parent, _ := e.m.Posts.Meta(ctx, e.w.PostID("parent"))
	got := e.vis.VisibleChildren(ctx, s, parent, core.ChildFilter{Limit: 20})
	if len(got) != 20 {
		t.Fatalf("got %d children, want 20", len(got))
	}
	for i, l := range got {
		want := e.w.PostID(fmt.Sprintf("vis%02d", 29-i))
		if l.Link.Child != want || l.Other.Meta().ID != want || l.Other.Viewer() != v {
			t.Fatalf("child %d = %v, want the %d-th newest visible child", i, l.Link.Child, i)
		}
	}
	if all := e.vis.VisibleChildren(ctx, s, parent, core.ChildFilter{}); len(all) != 30 {
		t.Fatalf("no limit: %d children, want 30", len(all))
	}
	// NotSelf drops a's own children; RefOnly drops cont links.
	if ns := e.vis.VisibleChildren(ctx, s, parent, core.ChildFilter{NotSelf: true}); len(ns) != 0 {
		t.Fatalf("NotSelf kept %d", len(ns))
	}
	// An invisible parent has no visible children at all.
	hidden := parent
	hidden.Deleted = true
	if got := e.vis.VisibleChildren(ctx, s, hidden, core.ChildFilter{}); got != nil {
		t.Fatal("children of an invisible parent")
	}
	// Parents: vis00 shows its parent; hid00 is invisible so shows nothing.
	c, _ := e.m.Posts.Meta(ctx, e.w.PostID("vis00"))
	ps := e.vis.VisibleParents(ctx, s, c)
	if len(ps) != 1 || ps[0].Other.Meta().ID != parent.ID || ps[0].Link.Kind != core.LinkRef {
		t.Fatalf("parents = %+v", ps)
	}
	h, _ := e.m.Posts.Meta(ctx, e.w.PostID("hid00"))
	if e.vis.VisibleParents(ctx, s, h) != nil {
		t.Fatal("parents of an invisible child")
	}
}

func TestVisibleChildrenFilters(t *testing.T) {
	e := newEnv(t)
	v, a := e.w.User("v"), e.w.User("a")
	e.w.Friends("v", "a")
	e.w.Post("a", "root", "r", coretest.At(coretest.Epoch.Add(-time.Hour)))
	root := e.w.PostID("root")
	cont := func(m *core.PostMeta, ls *[]core.LinkMeta) {
		m.ContParent = root
		*ls = append(*ls, core.LinkMeta{Parent: root, Kind: core.LinkCont, ParentAuthor: a})
	}
	e.w.Post("a", "part2", "c", cont, coretest.At(coretest.Epoch.Add(-time.Minute)))
	e.w.Post("v", "reply", "r", e.w.ReplyTo("root"), coretest.At(coretest.Epoch.Add(-30*time.Second)))
	s := e.search(v)
	pm, _ := e.m.Posts.Meta(ctx, root)
	if got := e.vis.VisibleChildren(ctx, s, pm, core.ChildFilter{}); len(got) != 2 {
		t.Fatalf("all: %d", len(got))
	}
	got := e.vis.VisibleChildren(ctx, s, pm, core.ChildFilter{RefOnly: true})
	if len(got) != 1 || got[0].Link.Child != e.w.PostID("reply") {
		t.Fatalf("RefOnly: %+v", got)
	}
	got = e.vis.VisibleChildren(ctx, s, pm, core.ChildFilter{NotSelf: true})
	if len(got) != 1 || got[0].Link.Child != e.w.PostID("reply") {
		t.Fatalf("NotSelf: %+v", got)
	}
}

// ---- CanReference ----

func TestCanReferenceTable(t *testing.T) {
	e := newEnv(t)
	v := e.w.User("v")
	e.w.User("f")
	e.w.User("d2")
	e.w.User("d3")
	e.w.User("x")
	e.w.Friends("v", "f")
	e.w.Friends("f", "d2")
	e.w.Friends("d2", "d3")
	id := map[string]core.UserID{"self": v, "f": e.w.ID("f"), "d2": e.w.ID("d2"), "d3": e.w.ID("d3"), "x": e.w.ID("x")}
	s := e.search(v)
	type row struct {
		author string
		pol    core.RefPolicy
		ref    bool // want for kind ref
		cont   bool // want for kind cont
	}
	rows := []row{
		{"self", core.PolAnyone, true, true},
		{"self", core.PolNobody, true, true}, // the author's own ref on a slippery post
		{"self", core.PolFriends, true, true},
		{"f", core.PolAnyone, true, false}, // cont on another's post ⇒ false
		{"f", core.PolTwoHops, true, false},
		{"f", core.PolFriends, true, false},
		{"f", core.PolNobody, false, false},
		{"d2", core.PolAnyone, true, false},
		{"d2", core.PolTwoHops, true, false},
		{"d2", core.PolFriends, false, false},
		{"d2", core.PolNobody, false, false},
		{"d3", core.PolAnyone, true, false},
		{"d3", core.PolTwoHops, false, false},
		{"d3", core.PolFriends, false, false},
		{"x", core.PolAnyone, false, false}, // invisible parent
		{"f", core.RefPolicy(9), false, false},
	}
	for i, r := range rows {
		p := post(fmt.Sprintf("r%d", i), id[r.author], core.AudConnected)
		p.RefPolicy = r.pol
		if got := e.vis.CanReference(ctx, s, p, core.LinkRef); got != r.ref {
			t.Fatalf("%s/%d ref: got %v want %v", r.author, r.pol, got, r.ref)
		}
		if got := e.vis.CanReference(ctx, s, p, core.LinkCont); got != r.cont {
			t.Fatalf("%s/%d cont: got %v want %v", r.author, r.pol, got, r.cont)
		}
		if e.vis.CanReference(ctx, s, p, core.LinkKind(7)) {
			t.Fatal("unknown link kind")
		}
	}
	// A deleted or audience-hidden parent cannot be referenced.
	p := post("gone", id["f"], core.AudConnected)
	p.Deleted = true
	if e.vis.CanReference(ctx, s, p, core.LinkRef) {
		t.Fatal("deleted parent")
	}
	if e.vis.CanReference(ctx, s, post("lim", id["d3"], core.AudD2), core.LinkRef) {
		t.Fatal("parent outside its audience")
	}
}

// ---- chains ----

func TestChainNaming(t *testing.T) {
	e := newEnv(t)
	v := e.w.User("v", coretest.NamedInChains())
	e.w.User("h1", coretest.NamedInChains())
	e.w.User("h2", coretest.NamedInChains())
	e.w.User("h3")
	e.w.User("au", coretest.NamedInChains())
	e.w.Friends("v", "h1")
	e.w.Friends("h1", "h2")
	e.w.Friends("h2", "h3")
	e.w.Friends("h3", "au")
	s := e.search(v)
	h1, h2, h3, au := e.w.ID("h1"), e.w.ID("h2"), e.w.ID("h3"), e.w.ID("au")

	self, err := e.vis.Chain(ctx, s, v)
	if err != nil || self.Dist != 0 || self.FirstHop != nil {
		t.Fatalf("self chain: %+v %v", self, err)
	}
	friend, _ := e.vis.Chain(ctx, s, h1)
	if friend.Dist != 1 || friend.FirstHop != nil || friend.HiddenMiddle != 0 {
		t.Fatalf("friend chain: %+v", friend)
	}
	two, _ := e.vis.Chain(ctx, s, h2)
	if two.Dist != 2 || two.FirstHop == nil || two.FirstHop.Handle != "h1" || two.FirstHop.Ref != e.m.D.Refs.User(v, h1) ||
		len(two.Middle) != 0 || two.HiddenMiddle != 0 {
		t.Fatalf("dist-2 chain: %+v", two)
	}
	// dist 3 to h3: h3 did not opt in ⇒ the middle (h2) collapses.
	three, _ := e.vis.Chain(ctx, s, h3)
	if three.Dist != 3 || three.FirstHop.Handle != "h1" || len(three.Middle) != 0 || three.HiddenMiddle != 1 {
		t.Fatalf("dist-3 chain: %+v", three)
	}
	// dist 4 to au: h3 (a middle endpoint) did not opt in ⇒ 2 hidden.
	four, _ := e.vis.Chain(ctx, s, au)
	if four.HiddenMiddle != 2 || len(four.Middle) != 0 {
		t.Fatalf("dist-4 chain: %+v", four)
	}
	// Everyone opts in ⇒ the middle is named, in path order.
	m, _ := e.m.Users.Get(ctx, h3)
	m.NameInChains = true
	e.m.Users.Put(m)
	four, _ = e.vis.Chain(ctx, s, au)
	if four.HiddenMiddle != 0 || len(four.Middle) != 2 || four.Middle[0].Handle != "h2" || four.Middle[1].Handle != "h3" ||
		four.Middle[1].Ref != e.m.D.Refs.User(v, h3) {
		t.Fatalf("named chain: %+v", four)
	}
	// The author not opting in hides the middle too.
	m, _ = e.m.Users.Get(ctx, au)
	m.NameInChains = false
	e.m.Users.Put(m)
	four, _ = e.vis.Chain(ctx, s, au)
	if four.HiddenMiddle != 2 || four.FirstHop.Handle != "h1" {
		t.Fatalf("author opted out: %+v", four)
	}
	// Invisible ⇒ ErrNotFound.
	x := e.w.User("x")
	if _, err := e.vis.Chain(ctx, s, x); !errors.Is(err, gate.ErrNotFound) {
		t.Fatal("chain to an invisible user")
	}
}

func TestChainFirstHopPlus(t *testing.T) {
	e := newEnv(t)
	v, a := e.w.User("v"), e.w.User("a")
	e.w.User("b")
	e.m.Graph.AddEdge(v, a, core.LevelFriendPlus, coretest.Epoch)
	e.w.Friends("a", "b")
	c, err := e.vis.Chain(ctx, e.search(v), e.w.ID("b"))
	if err != nil || !c.FirstHopPlus || c.FirstHop.Handle != "a" {
		t.Fatalf("chain: %+v %v", c, err)
	}
}

// The real visibility agrees with the reference implementation on random-ish
// worlds (a cheap cross-check on top of the spec tables).
func TestAgreesWithReference(t *testing.T) {
	e := newEnv(t)
	hs := []string{"u0", "u1", "u2", "u3", "u4", "u5", "u6"}
	for _, h := range hs {
		e.w.User(h)
	}
	for _, p := range [][2]string{{"u0", "u1"}, {"u1", "u2"}, {"u2", "u3"}, {"u0", "u4"}, {"u4", "u3"}, {"u3", "u5"}} {
		e.w.Friends(p[0], p[1])
	}
	e.w.Block("u0", "u4")
	e.w.Block("u5", "u0")
	for i, h := range hs {
		for _, aud := range []core.Audience{core.AudConnected, core.AudD1, core.AudD2, core.AudD3} {
			e.w.Post(h, fmt.Sprintf("%s-%d", h, aud), "b", coretest.Audience(aud), coretest.At(coretest.Epoch.Add(time.Duration(i)*time.Minute)))
		}
	}
	ref := coretestRef(e)
	for _, h := range hs {
		v := e.w.ID(h)
		s := e.search(v)
		for _, h2 := range hs {
			for _, aud := range []core.Audience{core.AudConnected, core.AudD1, core.AudD2, core.AudD3} {
				pm, _ := e.m.Posts.Meta(ctx, e.w.PostID(fmt.Sprintf("%s-%d", h2, aud)))
				_, a := e.vis.CheckPost(ctx, s, pm)
				_, b := ref.CheckPost(ctx, s, pm)
				if a != b {
					t.Fatalf("viewer %s post %s-%d: %v vs reference %v", h, h2, aud, a, b)
				}
			}
		}
	}
}

func coretestRef(e *env) core.Visibility { return &coreref.MemVisibility{D: e.m.D} }
