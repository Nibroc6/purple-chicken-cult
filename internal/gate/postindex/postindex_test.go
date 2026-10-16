package postindex_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coretest"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/postindex"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire/wiretest"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/ids"
)

type env struct {
	t   *testing.T
	a   *wire.App
	d   *core.Deps
	x   *postindex.Index
	w   *wiretest.TestWorld
	ctx context.Context
}

func newEnv(t *testing.T) *env {
	t.Helper()
	a := wiretest.NewApp(t)
	e := &env{t: t, a: a, d: a.Deps(), x: a.PostIndex, w: wiretest.World(t, a), ctx: context.Background()}
	e.w.User("alice")
	e.w.User("bob")
	return e
}

func (e *env) advance(d time.Duration) {
	e.t.Helper()
	if err := e.a.Hooks().AdvanceClock(d); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) fresh() *postindex.Index {
	e.t.Helper()
	x := postindex.New(e.d)
	if err := x.Load(e.ctx); err != nil {
		e.t.Fatal(err)
	}
	return x
}

func labels(e *env, ps []core.PostID, names ...string) bool {
	if len(ps) != len(names) {
		return false
	}
	for i, n := range names {
		if ps[i] != e.w.PostID(n) {
			return false
		}
	}
	return true
}

func TestWindowRecentAndScan(t *testing.T) {
	e := newEnv(t)
	now := e.d.Clock.Now()
	e.w.Post("alice", "a1", "one", coretest.At(now.Add(-3*time.Hour)))
	e.w.Post("bob", "b1", "two", coretest.At(now.Add(-2*time.Hour)))
	e.w.Post("alice", "a2", "three", coretest.At(now.Add(-time.Hour)))
	e.w.Post("alice", "a0", "old", coretest.At(now.Add(-8*24*time.Hour)))
	for _, x := range []*postindex.Index{e.x, e.fresh()} {
		if !labels(e, x.Recent(e.w.ID("alice")), "a2", "a1") || x.RecentLen(e.w.ID("bob")) != 1 || x.WindowSize() != 3 {
			t.Fatalf("recent %v size %d", x.Recent(e.w.ID("alice")), x.WindowSize())
		}
		var scan []core.PostID
		x.WindowScan(func(m core.PostMeta) bool { scan = append(scan, m.ID); return true })
		if !labels(e, scan, "a2", "b1", "a1") {
			t.Fatalf("scan %v", scan)
		}
		var first []core.PostID
		x.WindowScan(func(m core.PostMeta) bool { first = append(first, m.ID); return false })
		if len(first) != 1 {
			t.Fatal("scan did not stop")
		}
		// Older posts come from the LRU / database.
		if m, ok := x.Meta(e.ctx, e.w.PostID("a0")); !ok || m.Author != e.w.ID("alice") || m.Deleted {
			t.Fatalf("old meta %+v %v", m, ok)
		}
		if _, ok := x.Meta(e.ctx, ids.Named("post:none")); ok {
			t.Fatal("unknown post has a meta")
		}
	}
	// Posts age out at read time and leave memory on trim.
	e.advance(7*24*time.Hour - 150*time.Minute)
	if !labels(e, e.x.Recent(e.w.ID("alice")), "a2") || e.x.WindowSize() != 2 {
		t.Fatalf("after aging %v", e.x.Recent(e.w.ID("alice")))
	}
	e.x.Trim()
	if e.x.WindowSize() != 2 {
		t.Fatal("trim changed the window")
	}
	if m, ok := e.x.Meta(e.ctx, e.w.PostID("a1")); !ok || m.ID != e.w.PostID("a1") {
		t.Fatal("trimmed post lost its meta")
	}
	if err := e.a.DrainJobs(e.ctx); err != nil { // postindex.trim is registered
		t.Fatal(err)
	}
}

// Ties on created_at order by id (newest first = id ascending), like coreref.
func TestTiesAndPutReplace(t *testing.T) {
	e := newEnv(t)
	at := coretest.At(e.d.Clock.Now().Add(-time.Minute))
	e.w.Post("alice", "x", "x", at)
	e.w.Post("alice", "y", "y", at)
	got := e.x.Recent(e.w.ID("alice"))
	want := []core.PostID{e.w.PostID("x"), e.w.PostID("y")}
	slices.SortFunc(want, func(a, b core.PostID) int { return slices.Compare(a[:], b[:]) })
	if !slices.Equal(got, want) || !slices.Equal(e.fresh().Recent(e.w.ID("alice")), want) {
		t.Fatalf("ties %v want %v", got, want)
	}
	m, _ := e.x.Meta(e.ctx, e.w.PostID("x"))
	m.Version, m.RefPolicy = 2, core.PolNobody
	e.x.Put(m)
	if got, _ := e.x.Meta(e.ctx, m.ID); got.Version != 2 || got.RefPolicy != core.PolNobody || e.x.WindowSize() != 2 {
		t.Fatalf("replace %+v", got)
	}
	e.x.Delete(m.ID)
	if got, ok := e.x.Meta(e.ctx, m.ID); !ok || !got.Deleted || e.x.RecentLen(e.w.ID("alice")) != 1 || e.x.WindowSize() != 1 {
		t.Fatalf("delete %+v", got)
	}
}

func TestLinksBodiesAndMetaFlags(t *testing.T) {
	e := newEnv(t)
	now := e.d.Clock.Now()
	e.w.Post("alice", "p", "parent body", coretest.At(now.Add(-10*24*time.Hour)))
	e.w.Post("bob", "c1", "first child", coretest.At(now.Add(-2*time.Hour)), e.w.ReplyTo("p"))
	e.w.Post("bob", "c2", "second child", coretest.At(now.Add(-time.Hour)), e.w.ReplyTo("p"))
	for _, x := range []*postindex.Index{e.x, e.fresh()} {
		var kids []core.PostID
		x.Children(e.ctx, e.w.PostID("p"), func(l core.LinkMeta) bool { kids = append(kids, l.Child); return true })
		if !labels(e, kids, "c2", "c1") {
			t.Fatalf("children %v", kids)
		}
		ps := x.Parents(e.ctx, e.w.PostID("c1"))
		if len(ps) != 1 || ps[0].Parent != e.w.PostID("p") || ps[0].ParentAuthor != e.w.ID("alice") || ps[0].Kind != core.LinkRef {
			t.Fatalf("parents %+v", ps)
		}
		if m, _ := x.Meta(e.ctx, e.w.PostID("c1")); !m.IsReply {
			t.Fatalf("c1 meta %+v", m)
		}
		b := x.Bodies(e.ctx, []core.PostID{e.w.PostID("p"), e.w.PostID("c2"), ids.Named("post:none")})
		if len(b) != 2 || b[e.w.PostID("p")] != "parent body" {
			t.Fatalf("bodies %v", b)
		}
	}
	// PutLink is idempotent; UpdateLink replaces the cached link.
	ps := e.x.Parents(e.ctx, e.w.PostID("c1"))
	l := ps[0]
	e.x.PutLink(l)
	n := 0
	e.x.Children(e.ctx, l.Parent, func(core.LinkMeta) bool { n++; return true })
	if n != 2 {
		t.Fatalf("%d children after a repeated PutLink", n)
	}
	l.Detached = true
	e.x.UpdateLink(l)
	if got := e.x.Parents(e.ctx, l.Child); len(got) != 1 || !got[0].Detached {
		t.Fatalf("update %+v", got)
	}
	e.x.Children(e.ctx, l.Parent, func(c core.LinkMeta) bool {
		if c.Child == l.Child && !c.Detached {
			t.Fatal("children cache not updated")
		}
		return true
	})
}

func TestMentionTargets(t *testing.T) {
	e := newEnv(t)
	e.w.Post("alice", "m", "hi @bob")
	id := e.w.PostID("m")
	if got := e.x.MentionTargets(e.ctx, id); got != nil {
		t.Fatalf("no mentions yet: %v", got)
	}
	if _, err := e.d.DB.Q().Exec(e.ctx, "INSERT INTO mentions (post_id, ord, target_id) VALUES ($1, 0, $2)", id,
		int64(e.w.ID("bob"))); err != nil {
		t.Fatal(err)
	}
	// The empty answer was cached; a new version (an edit) drops it.
	m, _ := e.x.Meta(e.ctx, id)
	m.Version++
	e.x.Put(m)
	if got := e.x.MentionTargets(e.ctx, id); len(got) != 1 || got[0] != e.w.ID("bob") {
		t.Fatalf("mentions %v", got)
	}
	got := e.x.MentionTargets(e.ctx, id)
	got[0] = 0 // callers get copies
	if again := e.x.MentionTargets(e.ctx, id); again[0] != e.w.ID("bob") {
		t.Fatal("cache shared with callers")
	}
	e.x.Delete(id)
	if _, err := e.d.DB.Q().Exec(e.ctx, "DELETE FROM mentions WHERE post_id = $1", id); err != nil {
		t.Fatal(err)
	}
	if got := e.x.MentionTargets(e.ctx, id); got != nil {
		t.Fatalf("after delete %v", got)
	}
}

// The test-hook reset reloads the index (contract request p0-w05-1).
func TestResetReloads(t *testing.T) {
	e := newEnv(t)
	e.w.Post("alice", "gone", "x")
	if err := e.a.Hooks().Reset(e.ctx); err != nil {
		t.Fatal(err)
	}
	if e.x.WindowSize() != 0 {
		t.Fatal("reset kept posts in memory")
	}
	if _, ok := e.x.Meta(e.ctx, e.w.PostID("gone")); ok {
		t.Fatal("reset kept a meta")
	}
}

// INT-0 (plan §7 perf: the feed reads only memory): after Load, the links and
// mentions of window posts come from memory, new links of new posts join
// them, and a post that ages out keeps its lists (moved to the LRUs).
func TestWindowLinksInMemory(t *testing.T) {
	e := newEnv(t)
	now := e.d.Clock.Now()
	old := e.w.Post("alice", "old", "old", coretest.At(now.Add(-9*24*time.Hour)))
	p := e.w.Post("alice", "p", "parent", coretest.At(now.Add(-3*time.Hour)))
	c := e.w.Post("bob", "c", "child", coretest.At(now.Add(-2*time.Hour)), e.w.ReplyTo("p"), e.w.ReplyTo("old"))
	if _, err := e.d.DB.Q().Exec(e.ctx, "INSERT INTO mentions (post_id, ord, target_id) VALUES ($1, 0, $2)",
		c, int64(e.w.ID("alice"))); err != nil {
		t.Fatal(err)
	}
	x := e.fresh()
	// The database no longer has them: memory must answer.
	if _, err := e.d.DB.Q().Exec(e.ctx, "DELETE FROM mentions; DELETE FROM post_parents"); err != nil {
		t.Fatal(err)
	}
	var kids []core.PostID
	x.Children(e.ctx, p, func(l core.LinkMeta) bool { kids = append(kids, l.Child); return true })
	if !slices.Equal(kids, []core.PostID{c}) {
		t.Fatalf("children of p from memory: %v", kids)
	}
	if ps := x.Parents(e.ctx, c); len(ps) != 2 || ps[0].Parent != p || ps[1].Parent != old {
		t.Fatalf("parents of c from memory: %+v", ps)
	}
	if m := x.MentionTargets(e.ctx, c); m[0] != e.w.ID("alice") {
		t.Fatalf("mentions of c from memory: %v", m)
	}
	// A new post and its link (posts module order: Put, then PutLink).
	n := ids.Named("post:n")
	x.Put(core.PostMeta{ID: n, Author: e.w.ID("bob"), CreatedAt: now, Version: 1, Lang: "en", IsReply: true})
	x.PutLink(core.LinkMeta{Child: n, Parent: p, Kind: core.LinkRef, ChildAuthor: e.w.ID("bob"),
		ParentAuthor: e.w.ID("alice"), CreatedAt: now})
	kids = nil
	x.Children(e.ctx, p, func(l core.LinkMeta) bool { kids = append(kids, l.Child); return true })
	if !slices.Equal(kids, []core.PostID{n, c}) {
		t.Fatalf("children after a new reply: %v", kids)
	}
	if ps := x.Parents(e.ctx, n); len(ps) != 1 || ps[0].Parent != p {
		t.Fatalf("parents of the new post: %+v", ps)
	}
	// Aging out keeps the lists.
	e.advance(8 * 24 * time.Hour)
	x.Trim()
	kids = nil
	x.Children(e.ctx, p, func(l core.LinkMeta) bool { kids = append(kids, l.Child); return true })
	if !slices.Equal(kids, []core.PostID{n, c}) || x.WindowSize() != 0 {
		t.Fatalf("children after trim: %v (window %d)", kids, x.WindowSize())
	}
}
