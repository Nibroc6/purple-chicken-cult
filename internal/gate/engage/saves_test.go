package engage_test

import (
	"fmt"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/engage"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coretest"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/posts"
)

func (e *env) save(h string, p core.PostID, on bool) {
	e.t.Helper()
	if err := e.e.Save(e.as(h), e.ref(h, p), on); err != nil {
		e.t.Fatalf("%s saves: %v", h, err)
	}
}

func (e *env) kids2(viewer string, p core.PostID) []float64 {
	e.t.Helper()
	sr, c := e.checked(viewer, p)
	return e.e.KidsN(e.ctx, sr, c, core.NewMemo(), 2)
}

func (e *env) view(viewer string, p core.PostID) gate.PostView {
	e.t.Helper()
	sr, c := e.checked(viewer, p)
	vs, err := e.d.Views.Posts(e.ctx, sr, []core.Checked{c}, core.ViewOpts{})
	if err != nil || len(vs) != 1 {
		e.t.Fatalf("view: %v", err)
	}
	return vs[0]
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// Acceptance: saves are never displayed or counted in any view. Only the saver
// sees saved_by_me; the like summary and reply counts of everyone else stay
// exactly as they were.
func TestSavesArePrivate(t *testing.T) {
	e := newEnv(t)
	p := e.post("carol", "worth keeping")
	e.like("bob", p)
	before := map[string]gate.PostView{}
	for _, h := range []string{"alice", "bob", "carol", "dave"} {
		before[h] = e.view(h, p)
	}

	e.save("bob", p, true)
	e.save("bob", p, true) // idempotent
	e.save("dave", p, true)
	if n := e.count("SELECT count(*) FROM engagements WHERE post_id = $1 AND kind = 2", p); n != 2 {
		t.Fatalf("save rows %d", n)
	}
	if !e.e.SaverSet(e.ctx, p).Contains(uint32(e.id("bob"))) || e.e.LikerSet(e.ctx, p).Contains(uint32(e.id("dave"))) {
		t.Fatal("saves and likes mixed up in memory")
	}
	for _, h := range []string{"alice", "bob", "carol", "dave"} {
		got := e.view(h, p)
		if got.SavedByMe != (h == "bob" || h == "dave") {
			t.Fatalf("%s: saved_by_me %v", h, got.SavedByMe)
		}
		got.SavedByMe = false
		if fmt.Sprintf("%+v", got.Likes) != fmt.Sprintf("%+v", before[h].Likes) || got.ReplyCount != before[h].ReplyCount {
			t.Fatalf("%s sees the saves: %+v, was %+v", h, got.Likes, before[h].Likes)
		}
	}
	ls, err := e.e.Likers(e.as("alice"), e.ref("alice", p))
	if err != nil || ls.InNetwork != 1 {
		t.Fatalf("likers %+v (%v)", ls, err)
	}

	e.save("bob", p, false)
	e.save("bob", p, false)
	if e.view("bob", p).SavedByMe || e.e.SaverSet(e.ctx, p).Contains(uint32(e.id("bob"))) {
		t.Fatal("unsave not honored")
	}
	// The snapshot is a copy.
	e.e.SaverSet(e.ctx, p).Add(42)
	if e.e.SaverSet(e.ctx, p).Contains(42) {
		t.Fatal("SaverSet leaked the cached bitmap")
	}
}

func TestSaveRefsAreNotFound(t *testing.T) {
	e := newEnv(t)
	p := e.post("bob", "hello")
	far := e.post("erin", "far away")
	for _, r := range []gate.Ref{"garbage", e.ref("bob", p), e.ref("alice", far)} {
		wantNotFound(t, e.e.Save(e.as("alice"), r, true))
		wantNotFound(t, e.e.Save(e.as("alice"), r, false))
	}
	if n := e.count("SELECT count(*) FROM engagements"); n != 0 {
		t.Fatalf("%d rows written", n)
	}
	wantNotFound(t, e.e.Save(e.ctx, e.ref("alice", p), true))
	_, err := e.e.Saves(e.ctx, "")
	wantNotFound(t, err)
}

func TestSavesPages(t *testing.T) {
	e := newEnv(t)
	var ids []core.PostID
	for i := range 45 {
		ids = append(ids, e.w.Post("bob", fmt.Sprintf("p%02d", i), fmt.Sprintf("post %d", i)))
	}
	gone := e.post("bob", "deleted later")
	e.save("alice", gone, true)
	for _, id := range ids {
		e.advance(time.Second)
		e.save("alice", id, true)
	}
	if err := e.a.Posts.Delete(e.as("bob"), e.ref("bob", gone)); err != nil {
		t.Fatal(err)
	}
	// A seen cursor never opens the saves list.
	for _, id := range ids {
		if err := e.e.MarkSeen(e.as("alice"), []gate.Ref{e.ref("alice", id)}); err != nil {
			t.Fatal(err)
		}
	}
	seen, err := e.e.RecentlySeen(e.as("alice"), "")
	if err != nil || seen.Next == "" {
		t.Fatalf("seen %v", err)
	}
	_, err = e.e.Saves(e.as("alice"), seen.Next)
	wantNotFound(t, err)

	var got []gate.Ref
	cursor := ""
	for range 5 {
		page, err := e.e.Saves(e.as("alice"), cursor)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) > engage.SeenPageSize {
			t.Fatalf("page of %d", len(page.Items))
		}
		for _, pv := range page.Items {
			if !pv.SavedByMe {
				t.Fatal("a listed save is not saved_by_me")
			}
			got = append(got, pv.Ref)
		}
		if page.Next == "" {
			break
		}
		cursor = page.Next
		_, err = e.e.Saves(e.as("bob"), cursor)
		wantNotFound(t, err)
		_, err = e.e.RecentlySeen(e.as("alice"), cursor)
		wantNotFound(t, err)
	}
	if len(got) != 45 {
		t.Fatalf("listed %d posts", len(got))
	}
	for i, r := range got {
		if want := e.ref("alice", ids[44-i]); r != want {
			t.Fatalf("slot %d out of order", i)
		}
	}
	page, err := e.e.Saves(e.as("carol"), "")
	if err != nil || page.Items == nil || len(page.Items) != 0 || page.Next != "" {
		t.Fatalf("empty page %+v (%v)", page, err)
	}
}

// §5.2 signals: a like 1, a save 2, one engager at most 4 together; the same
// proximity, author, probation and inviter rules as likes.
func TestSaveSignals(t *testing.T) {
	e := newEnv(t)
	e.w.User("fof")
	e.w.Friends("bob", "fof")
	e.reloadUsers()
	p := e.post("carol", "saved")
	e.save("bob", p, true) // friend: 2
	if n := e.n("alice", p); !near(n, 2) {
		t.Fatalf("n = %v, want 2", n)
	}
	e.like("bob", p) // friend, like + save: 3
	if n := e.n("alice", p); !near(n, 3) {
		t.Fatalf("n = %v, want 3", n)
	}
	e.save("fof", p, true)   // second degree: 0.5 · 2
	e.save("carol", p, true) // the author: 0
	e.save("dave", p, true)  // 3 hops: nothing
	e.save("alice", p, true) // the reader: nothing
	if n := e.n("alice", p); !near(n, 4) {
		t.Fatalf("n = %v, want 4", n)
	}
	if engage.SignalCap != 4 {
		t.Fatalf("signal cap %v", engage.SignalCap)
	}

	// Saves share the inviter cap with likes.
	for i := range 3 {
		h := fmt.Sprintf("inv%d", i)
		e.w.User(h, coretest.InvitedBy(e.id("frank")))
		e.w.Friends("alice", h)
	}
	e.reloadUsers()
	q := e.post("carol", "invitees")
	for i := range 3 {
		e.save(fmt.Sprintf("inv%d", i), q, true)
	}
	if n := e.n("alice", q); !near(n, engage.InviterCap) {
		t.Fatalf("n = %v, want the inviter cap", n)
	}

	// Saves stored by someone else (fixtures) count after Load.
	r := e.post("carol", "seeded")
	e.exec("INSERT INTO engagements (user_id, post_id, kind, at) VALUES ($1, $2, 2, now())", int64(e.id("bob")), r)
	if err := e.e.Load(e.ctx); err != nil {
		t.Fatal(err)
	}
	if n := e.n("alice", r); !near(n, 2) || !e.view("bob", r).SavedByMe {
		t.Fatalf("n = %v after Load", n)
	}

	// An account's deletion drops its saves.
	e.d.Bus.Publish(core.AccountDeleted{ID: e.id("bob")})
	if e.e.SaverSet(e.ctx, p).Contains(uint32(e.id("bob"))) || e.count("SELECT count(*) FROM engagements WHERE user_id = $1", int64(e.id("bob"))) != 0 {
		t.Fatal("deleted account's saves kept")
	}
}

// Acceptance: grandchildren credit (gens = 2) adds β·Σ n of each visible
// child's own visible children; grandchildren behind invisible links never
// count and adding them never changes KidsN; a slippery parent still gets no
// credit, and a slippery child passes none up from its replies.
func TestPreferGrandchildren(t *testing.T) {
	e := newEnv(t)
	e.w.User("gina")
	e.w.Friends("carol", "gina")
	parent := e.post("bob", "parent")
	c1 := e.reply("carol", parent)
	c2 := e.reply("gina", parent)
	g1 := e.reply("gina", c1)
	g2 := e.reply("bob", c1)
	e.like("bob", c1)   // n(c1) = 1
	e.like("carol", c2) // n(c2) = 0.5
	e.like("bob", g1)   // n(g1) = 1
	e.save("carol", g2, true)
	// n(g2) = 0.5 · 2 = 1 (carol is second degree for alice)
	want := []float64{0.5, 1 + engage.Beta*2}
	got := e.kids2("alice", parent)
	slices.Sort(got)
	if len(got) != 2 || !near(got[0], want[0]) || !near(got[1], want[1]) {
		t.Fatalf("kids %v, want %v", got, want)
	}
	if one := e.kids("alice", parent); !slices.Equal(slices.Sorted(slices.Values(one)), []float64{0.5, 1}) {
		t.Fatalf("gens 1 counts grandchildren: %v", one)
	}

	// An invisible grandchild (dave's friends-only reply, 3 hops from alice),
	// a detached one and a self-reply never change KidsN, liked or not.
	{
		h, err := e.a.Posts.Create(e.as("dave"), posts.Draft{Body: "only friends", Lang: "en", Audience: "d1",
			Parents: []posts.ParentIn{{Ref: e.ref("dave", c1), Kind: "ref"}}})
		if err != nil {
			t.Fatal(err)
		}
		hid, _ := e.d.Refs.OpenPost(e.id("dave"), h.Ref)
		e.like("carol", hid)
		e.save("carol", hid, true)
	}
	self := e.reply("carol", c1)
	e.like("bob", self)
	det := e.reply("bob", c1)
	e.like("bob", det)
	if err := e.a.Posts.Detach(e.as("carol"), e.ref("carol", c1), e.ref("carol", det)); err != nil {
		t.Fatal(err)
	}
	after := e.kids2("alice", parent)
	slices.Sort(after)
	if !slices.Equal(after, got) {
		t.Fatalf("invisible grandchildren changed KidsN: %v, was %v", after, got)
	}

	// A slippery child passes no grandchild credit up; a slippery parent gets none.
	if _, err := e.a.Posts.SetRefPolicy(e.as("carol"), e.ref("carol", c1), "nobody"); err != nil {
		t.Fatal(err)
	}
	slippery := e.kids2("alice", parent)
	slices.Sort(slippery)
	if !slices.Equal(slippery, []float64{0.5, 1}) {
		t.Fatalf("slippery child passed credit: %v", slippery)
	}
	if _, err := e.a.Posts.SetRefPolicy(e.as("bob"), e.ref("bob", parent), "nobody"); err != nil {
		t.Fatal(err)
	}
	if got := e.kids2("alice", parent); got != nil {
		t.Fatalf("slippery parent got %v", got)
	}
}

// Each level is limited to K visible children after the filters, so at most
// K² grandchildren count in total.
func TestPreferGrandchildrenCap(t *testing.T) {
	e := newEnv(t)
	e.w.Post("bob", "root", "root")
	for i := range engage.KidsK + 2 {
		k := fmt.Sprintf("k%d", i)
		e.w.Post("carol", k, "child", e.w.ReplyTo("root"))
		for j := range engage.KidsK + 2 {
			g := fmt.Sprintf("g%d-%d", i, j)
			e.w.Post("dave", g, "grandchild", e.w.ReplyTo(k))
			e.w.Like("bob", g) // n(g) = 1 for alice
		}
	}
	if err := e.e.Load(e.ctx); err != nil {
		t.Fatal(err)
	}
	got := e.kids2("alice", e.w.PostID("root"))
	if len(got) != engage.KidsK {
		t.Fatalf("%d kids, want %d", len(got), engage.KidsK)
	}
	// Each kid sums the n of at most K grandchildren: n_kid = β·K.
	for _, k := range got {
		if !near(k, engage.Beta*engage.KidsK) {
			t.Fatalf("kids %v, want β·K each", got)
		}
	}
}
