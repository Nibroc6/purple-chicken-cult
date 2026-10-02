package coretest_test

import (
	"context"
	"testing"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coretest"
)

// The reference implementations must agree with §4 / §4.5 / §10.1.3 on a small world:
// a – b – c – d and a separate component x – y.
func TestReferenceWorld(t *testing.T) {
	m := coretest.NewMem(t)
	w := coretest.NewWorld(t, m.D, nil)
	for _, h := range []string{"a", "b", "c", "d", "x", "y"} {
		w.User(h)
	}
	w.Friends("a", "b")
	w.Friends("b", "c")
	w.Friends("c", "d")
	w.Friends("x", "y")
	w.Post("d", "d1", "hello from d")
	w.Post("x", "x1", "other component")
	w.Post("c", "c1", "friends of friends only", coretest.Audience(core.AudD2))
	w.Post("b", "reply", "a reply", w.ReplyTo("d1"))
	w.Like("b", "d1")

	ctx := context.Background()
	s, err := m.D.Vis.SearchFor(ctx, w.ID("a"))
	if err != nil {
		t.Fatal(err)
	}
	if d, ok := s.Dist(w.ID("d")); !ok || d != 3 {
		t.Fatalf("dist(a,d) = %d,%v; want 3", d, ok)
	}
	if _, ok := s.Dist(w.ID("x")); ok {
		t.Fatal("x is in another component and must be invisible")
	}
	if s.FirstHop(w.ID("d")) != w.ID("b") {
		t.Fatalf("first hop to d = %d, want b", s.FirstHop(w.ID("d")))
	}
	meta := func(label string) core.PostMeta {
		pm, ok := m.D.Posts.Meta(ctx, w.PostID(label))
		if !ok {
			t.Fatalf("no post %s", label)
		}
		return pm
	}
	if _, ok := m.D.Vis.CheckPost(ctx, s, meta("d1")); !ok {
		t.Error("a must see d's post at distance 3 (no maximum distance)")
	}
	if _, ok := m.D.Vis.CheckPost(ctx, s, meta("x1")); ok {
		t.Error("a must not see a post from another component")
	}
	if _, ok := m.D.Vis.CheckPost(ctx, s, meta("c1")); !ok {
		t.Error("a is at distance 2 from c and must see a d2 post")
	}
	sd, _ := m.D.Vis.SearchFor(ctx, w.ID("d"))
	if _, ok := m.D.Vis.CheckPost(ctx, sd, meta("c1")); !ok {
		t.Error("d is c's friend")
	}
	chain, err := m.D.Chains.Chain(ctx, s, w.ID("d"))
	if err != nil || chain.Dist != 3 || chain.FirstHop == nil || chain.FirstHop.Handle != "b" || chain.HiddenMiddle != 1 {
		t.Fatalf("chain a→d = %+v, %v", chain, err)
	}
	c, _ := m.D.Vis.CheckPost(ctx, s, meta("d1"))
	views, err := m.D.Views.Posts(ctx, s, []core.Checked{c}, core.ViewOpts{WithBody: true, WithContext: true})
	if err != nil || len(views) != 1 {
		t.Fatalf("views: %v", err)
	}
	pv := views[0]
	if pv.Tier != 3 || pv.Body != "hello from d" || pv.Likes.InNetwork != 1 || pv.ReplyCount != 1 || pv.Author.Inviter.Kind != "genesis" {
		t.Fatalf("post view %+v", pv)
	}
	// Blocks are one-directional (D7): after a blocks b, a's paths avoid b.
	w.Block("a", "b")
	s2, _ := m.D.Vis.SearchFor(ctx, w.ID("a"))
	if _, ok := s2.Dist(w.ID("d")); ok {
		t.Error("a blocked b, the only bridge: d must be unreachable for a")
	}
	sb, _ := m.D.Vis.SearchFor(ctx, w.ID("b"))
	if _, ok := sb.Dist(w.ID("a")); !ok {
		t.Error("being blocked hides nothing from b (D7)")
	}
}
