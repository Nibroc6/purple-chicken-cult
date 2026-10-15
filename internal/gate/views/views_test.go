package views

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coretest"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
	"github.com/Nibroc6/purple-chicken-cult/internal/store/storetest"
)

var ctx = context.Background()

type env struct {
	t     *testing.T
	m     *coretest.Mem
	w     *coretest.World
	views *Service
}

func newEnv(t *testing.T, db *store.DB) *env {
	t.Helper()
	m := coretest.NewMem(t)
	m.D.DB = db
	v := New(m.D)
	m.D.Views = v
	return &env{t: t, m: m, w: coretest.NewWorld(t, m.D, db), views: v}
}

func (e *env) search(v core.UserID) core.Search {
	e.t.Helper()
	s, err := e.m.D.Vis.SearchFor(ctx, v)
	if err != nil {
		e.t.Fatal(err)
	}
	return s
}

func (e *env) checked(s core.Search, labels ...string) []core.Checked {
	e.t.Helper()
	var out []core.Checked
	for _, l := range labels {
		pm, ok := e.m.Posts.Meta(ctx, e.w.PostID(l))
		if !ok {
			e.t.Fatalf("no post %s", l)
		}
		c, ok := e.m.D.Vis.CheckPost(ctx, s, pm)
		if !ok {
			e.t.Fatalf("post %s not visible", l)
		}
		out = append(out, c)
	}
	return out
}

func (e *env) view(v core.UserID, o core.ViewOpts, label string) gate.PostView {
	e.t.Helper()
	s := e.search(v)
	pvs, err := e.views.Posts(ctx, s, e.checked(s, label), o)
	if err != nil || len(pvs) != 1 {
		e.t.Fatalf("Posts(%s): %v %v", label, pvs, err)
	}
	return pvs[0]
}

func (e *env) setUser(u core.UserID, fn func(*core.UserMeta)) {
	m, err := e.m.Users.Get(ctx, u)
	if err != nil {
		e.t.Fatal(err)
	}
	fn(&m)
	e.m.Users.Put(m)
}

// Views.Posts accepts only core.Checked, which cannot be built outside core
// (unexported fields) and is minted only by visibility (archtest A10).
func TestPostsAcceptsOnlyChecked(t *testing.T) {
	// Compile-time: the conversion only type-checks while Posts takes []core.Checked.
	_ = (func(*Service, context.Context, core.Search, []core.Checked, core.ViewOpts) ([]gate.PostView, error))((*Service).Posts)
	ct := reflect.TypeFor[core.Checked]()
	for i := range ct.NumField() {
		if ct.Field(i).IsExported() {
			t.Fatalf("core.Checked.%s is exported: a Checked could be forged", ct.Field(i).Name)
		}
	}
	pt := reflect.TypeOf((*Service).Posts).In(3)
	if pt != reflect.TypeFor[[]core.Checked]() {
		t.Fatalf("Posts takes %v", pt)
	}
	// A Checked minted for another viewer is refused.
	e := newEnv(t, nil)
	a, b := e.w.User("a"), e.w.User("b")
	e.w.Friends("a", "b")
	e.w.Post("a", "p", "hi")
	cs := e.checked(e.search(a), "p")
	if _, err := e.views.Posts(ctx, e.search(b), cs, core.ViewOpts{}); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("foreign Checked: %v", err)
	}
	if _, err := e.views.Posts(ctx, e.search(b), []core.Checked{{}}, core.ViewOpts{}); !errors.Is(err, gate.ErrNotFound) {
		t.Fatal("a zero Checked must be refused")
	}
	if pvs, err := e.views.Posts(ctx, e.search(b), nil, core.ViewOpts{}); err != nil || len(pvs) != 0 {
		t.Fatal("no posts ⇒ empty")
	}
}

func TestPostViewFields(t *testing.T) {
	e := newEnv(t, nil)
	v := e.w.User("v")
	f := e.w.User("f")
	a := e.w.User("au", coretest.InvitedBy(f))
	e.w.Friends("v", "f")
	e.w.Friends("f", "au")
	e.setUser(a, func(m *core.UserMeta) { m.DisplayName = "Author Name" })
	edited := coretest.Epoch.Add(time.Minute)
	e.w.Post("au", "p", "# Hello *world*", coretest.Audience(core.AudD2), func(m *core.PostMeta, _ *[]core.LinkMeta) {
		m.AudienceForced, m.RefPolicy, m.Lang, m.EditedAt = false, core.PolTwoHops, "", &edited
	})

	pv := e.view(v, core.ViewOpts{}, "p")
	if pv.Ref != e.m.D.Refs.Post(v, e.w.PostID("p")) || pv.Body != "" || pv.Lang != "und" || pv.Audience != "d2" ||
		pv.RefPolicy != "two_hops" || pv.Mine || !pv.CanReference || pv.Tier != 2 || pv.EditedAt == nil ||
		!pv.CreatedAt.Equal(coretest.Epoch) {
		t.Fatalf("view: %+v", pv)
	}
	if pv.Author.Handle != "au" || pv.Author.DisplayName != "Author Name" || pv.Author.Ref != e.m.D.Refs.User(v, a) ||
		pv.Author.Inviter.Kind != "user" || pv.Author.Inviter.Handle != "f" || pv.Author.Chain.Dist != 2 ||
		pv.Author.Chain.FirstHop == nil || pv.Author.Chain.FirstHop.Handle != "f" {
		t.Fatalf("author: %+v", pv.Author)
	}
	if pv.Thread != nil || pv.Context != nil || pv.Mentions != nil || pv.Previews != nil || pv.ReplyCount != 0 {
		t.Fatalf("empty parts: %+v", pv)
	}
	if pv := e.view(v, core.ViewOpts{WithBody: true}, "p"); pv.Body != "# Hello *world*" {
		t.Fatalf("body %q", pv.Body)
	}
	if mine := e.view(a, core.ViewOpts{}, "p"); !mine.Mine || mine.Tier != 0 || mine.Author.Chain.Dist != 0 {
		t.Fatalf("own view: %+v", mine)
	}
}

func TestAudienceForcedOnlyOnOwnView(t *testing.T) {
	e := newEnv(t, nil)
	v, a := e.w.User("v"), e.w.User("a")
	e.w.Friends("v", "a")
	e.w.Post("a", "p", "x", coretest.Audience(core.AudD1), func(m *core.PostMeta, _ *[]core.LinkMeta) { m.AudienceForced = true })
	if pv := e.view(a, core.ViewOpts{}, "p"); !pv.AudienceForced {
		t.Fatal("the author sees the forced flag")
	}
	if pv := e.view(v, core.ViewOpts{}, "p"); pv.AudienceForced || pv.Audience != "d1" {
		t.Fatal("others never see the forced flag")
	}
}

func TestLikeSummary(t *testing.T) {
	e := newEnv(t, nil)
	v := e.w.User("v")
	e.w.User("au")
	// v's friends f1..f5 with increasing edge age (f5 oldest); d2 at distance 2; d3 at 3.
	for i := 1; i <= 5; i++ {
		h := fmt.Sprintf("f%d", i)
		e.w.User(h)
		e.m.Graph.AddEdge(v, e.w.ID(h), core.LevelFriend, coretest.Epoch.Add(-time.Duration(i)*time.Hour))
	}
	e.w.User("d2")
	e.w.User("d3")
	e.w.Friends("f1", "au")
	e.w.Friends("f1", "d2")
	e.w.Friends("d2", "d3")
	e.w.Post("au", "p", "x")
	e.setUser(e.w.ID("f4"), func(m *core.UserMeta) { m.NamedInLikes = false })
	for _, h := range []string{"f1", "f2", "f3", "f4", "f5", "d2", "d3", "au"} {
		e.w.Like(h, "p")
	}
	pv := e.view(v, core.ViewOpts{}, "p")
	// In network: f1..f5, d2, au (dist 2) = 7; d3 is 3 hops away.
	l := pv.Likes
	if l.InNetwork != 7 || l.ByMe {
		t.Fatalf("likes: %+v", l)
	}
	if len(l.Names) != 3 || l.Names[0].Handle != "f5" || l.Names[1].Handle != "f3" || l.Names[2].Handle != "f2" ||
		l.Names[0].Ref != e.m.D.Refs.User(v, e.w.ID("f5")) {
		t.Fatalf("names (oldest edge first, f4 opted out): %+v", l.Names)
	}
	if l.Others != 4 {
		t.Fatalf("others = %d", l.Others)
	}
	e.w.Like("v", "p")
	l = e.view(v, core.ViewOpts{}, "p").Likes
	if l.InNetwork != 8 || !l.ByMe || l.Others != 4 {
		t.Fatalf("with own like: %+v", l)
	}
	// The author sees the same rule from their own position.
	la := e.view(e.w.ID("au"), core.ViewOpts{}, "p").Likes
	// au: f1 (1), v/d2 (2), self liked; f2..f5 at 3, d3 at 3.
	if la.InNetwork != 4 || !la.ByMe {
		t.Fatalf("author's likes: %+v", la)
	}
}

func TestReplyCountAndContextVisibleOnly(t *testing.T) {
	e := newEnv(t, nil)
	v := e.w.User("v")
	e.w.User("a")
	e.w.User("stranger")
	e.w.Friends("v", "a")
	base := coretest.Epoch.Add(-time.Hour)
	e.w.Post("a", "root", "**Bold** start of a [linked](https://example.com) thought", coretest.At(base))
	e.w.Post("v", "r1", "reply", e.w.ReplyTo("root"), coretest.At(base.Add(time.Minute)))
	e.w.Post("a", "r2", "reply", e.w.ReplyTo("root"), coretest.At(base.Add(2*time.Minute)))
	e.w.Post("stranger", "r3", "hidden", e.w.ReplyTo("root"), coretest.At(base.Add(3*time.Minute)))
	// A cont child is not a reply.
	root := e.w.PostID("root")
	e.w.Post("a", "c1", "more", func(m *core.PostMeta, ls *[]core.LinkMeta) {
		m.ContParent = root
		*ls = append(*ls, core.LinkMeta{Parent: root, Kind: core.LinkCont, ParentAuthor: e.w.ID("a")})
	}, coretest.At(base.Add(4*time.Minute)))

	if pv := e.view(v, core.ViewOpts{}, "root"); pv.ReplyCount != 2 {
		t.Fatalf("reply count %d, want 2 (invisible and cont children excluded)", pv.ReplyCount)
	}
	pv := e.view(v, core.ViewOpts{WithContext: true}, "r1")
	if len(pv.Context) != 1 {
		t.Fatalf("context: %+v", pv.Context)
	}
	row := pv.Context[0]
	if row.Ref != e.m.D.Refs.Post(v, root) || row.Kind != "ref" || row.AuthorHandle != "a" ||
		row.Excerpt != "Bold start of a linked thought" {
		t.Fatalf("row: %+v", row)
	}
	if pv := e.view(v, core.ViewOpts{}, "r1"); pv.Context != nil {
		t.Fatal("context only WithContext")
	}
}

func TestThreadPositionOverVisibleParts(t *testing.T) {
	e := newEnv(t, nil)
	v := e.w.User("v")
	a := e.w.User("a")
	e.w.User("w")
	e.w.Friends("v", "a")
	e.w.Friends("v", "w")
	base := coretest.Epoch.Add(-time.Hour)
	labels := []string{"t1", "t2", "t3", "t4"}
	var prev core.PostID
	for i, l := range labels {
		p := prev
		opts := []coretest.PostOpt{coretest.At(base.Add(time.Duration(i) * time.Second))}
		if p != uuid.Nil {
			opts = append(opts, func(m *core.PostMeta, ls *[]core.LinkMeta) {
				m.ContParent = p
				*ls = append(*ls, core.LinkMeta{Parent: p, Kind: core.LinkCont, ParentAuthor: a})
			})
		}
		if i == 3 {
			opts = append(opts, coretest.Audience(core.AudD1)) // part 4 is friends-only
		}
		prev = e.w.Post("a", l, "part", opts...)
	}
	// Maintain ContChild as the posts module would.
	for i := 0; i+1 < len(labels); i++ {
		pm, _ := e.m.Posts.Meta(ctx, e.w.PostID(labels[i]))
		pm.ContChild = e.w.PostID(labels[i+1])
		e.m.Posts.Put(pm)
	}
	for i, l := range labels {
		th := e.view(v, core.ViewOpts{}, l).Thread
		if th == nil || th.Index != i+1 || th.Total != 4 {
			t.Fatalf("%s: %+v", l, th)
		}
	}
	// w (distance 2) cannot see part 4: the thread has three visible parts.
	for i, l := range labels[:3] {
		th := e.view(e.w.ID("w"), core.ViewOpts{}, l).Thread
		if th == nil || th.Index != i+1 || th.Total != 3 {
			t.Fatalf("w %s: %+v", l, th)
		}
	}
	// A deleted middle part splits the visible thread.
	e.m.Posts.Delete(e.w.PostID("t2"))
	if th := e.view(v, core.ViewOpts{}, "t1").Thread; th != nil {
		t.Fatalf("t1 alone: %+v", th)
	}
	if th := e.view(v, core.ViewOpts{}, "t4").Thread; th == nil || th.Index != 2 || th.Total != 2 {
		t.Fatalf("t4: %+v", th)
	}
}

func TestMentionsRefOnlyWhenVisible(t *testing.T) {
	e := newEnv(t, nil)
	v := e.w.User("v")
	e.w.User("a")
	far := e.w.User("far")
	friend := e.w.User("friend")
	e.w.Friends("v", "a")
	e.w.Friends("v", "friend")
	e.w.Post("a", "p", "hi @friend and @far")
	e.m.Posts.SetMentions(e.w.PostID("p"), map[int]core.UserID{1: far, 0: friend})
	m := e.view(v, core.ViewOpts{}, "p").Mentions
	if len(m) != 2 || m[0].Ord != 0 || m[0].Ref != e.m.D.Refs.User(v, friend) || m[1].Ord != 1 || m[1].Ref != "" {
		t.Fatalf("mentions: %+v", m)
	}
	// d.Mentions is optional (contract request p0-w02-1): nil renders no mentions.
	e.m.D.Mentions = nil
	if m := e.view(v, core.ViewOpts{}, "p").Mentions; len(m) != 0 {
		t.Fatalf("mentions without index: %+v", m)
	}
}

func TestPersonView(t *testing.T) {
	e := newEnv(t, nil)
	v := e.w.User("v")
	f := e.w.User("f")
	fp := e.w.User("fp")
	c := e.w.User("c")
	x := e.w.User("x")
	e.w.Friends("v", "f")
	e.m.Graph.AddEdge(v, fp, core.LevelFriendPlus, coretest.Epoch)
	e.w.Friends("f", "c")
	e.m.Mutes.SetMute(v, f, true)
	e.m.Requests.Set(v, c, "outgoing")
	e.m.Requests.Set(v, f, "incoming") // ignored: already friends
	s := e.search(v)

	self, err := e.views.Person(ctx, s, v)
	if err != nil || self.Relation != "self" || self.Inviter.Kind != "genesis" || self.Chain.Dist != 0 {
		t.Fatalf("self: %+v %v", self, err)
	}
	pf, _ := e.views.Person(ctx, s, f)
	if pf.Relation != "friend" || !pf.Muted || pf.Blocked || pf.Request != "" || pf.Handle != "f" || pf.Ref != e.m.D.Refs.User(v, f) {
		t.Fatalf("friend: %+v", pf)
	}
	if p, _ := e.views.Person(ctx, s, fp); p.Relation != "friend_plus" || p.Muted {
		t.Fatalf("friend+: %+v", p)
	}
	pc, _ := e.views.Person(ctx, s, c)
	if pc.Relation != "connected" || pc.Request != "outgoing" || pc.Chain.Dist != 2 || pc.Chain.FirstHop.Handle != "f" {
		t.Fatalf("connected: %+v", pc)
	}
	if _, err := e.views.Person(ctx, s, x); !errors.Is(err, gate.ErrNotFound) {
		t.Fatal("an invisible person must be ErrNotFound")
	}
	if _, err := e.views.Author(ctx, s, x); !errors.Is(err, gate.ErrNotFound) {
		t.Fatal("an invisible author must be ErrNotFound")
	}
	// Blocking hides the person from the blocker entirely.
	e.w.Block("v", "c")
	if _, err := e.views.Person(ctx, e.search(v), c); !errors.Is(err, gate.ErrNotFound) {
		t.Fatal("blocked person stays out of the horizon")
	}
	// c blocked by v still sees v unchanged (D7).
	if p, err := e.views.Person(ctx, e.search(c), v); err != nil || p.Blocked || p.Chain.Dist != 2 {
		t.Fatalf("blocked user's view of the blocker: %+v %v", p, err)
	}
}

func TestPreviewsReadyAndOkOnly(t *testing.T) {
	db := storetest.NewDB(t)
	e := newEnv(t, db)
	v := e.w.User("viewer")
	e.w.User("author")
	e.w.Friends("viewer", "author")
	e.w.Post("author", "p", "see https://example.com and https://two.example and https://three.example")
	pid := e.w.PostID("p")
	now := e.m.Clock.Now()
	ins := func(ord int, host string, status int, ready time.Time) {
		_, err := db.Q().Exec(ctx, `INSERT INTO link_previews (post_id, ord, url_canon, site_host, title, description, status, ready_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, pid, ord, "https://"+host+"/x", host, "T"+host, "D"+host, status, ready)
		if err != nil {
			t.Fatal(err)
		}
	}
	ins(0, "example.com", 1, now.Add(-time.Second))
	ins(1, "two.example", 1, now.Add(time.Second))  // not ready yet
	ins(2, "three.example", 2, now.Add(-time.Hour)) // failed
	pv := e.view(v, core.ViewOpts{}, "p")
	if len(pv.Previews) != 1 {
		t.Fatalf("previews: %+v", pv.Previews)
	}
	p := pv.Previews[0]
	if p.URL != "https://example.com/x" || p.Host != "example.com" || p.Title != "Texample.com" || p.Description != "Dexample.com" ||
		p.HostUnicode == "" {
		t.Fatalf("preview: %+v", p)
	}
	e.m.Clock.Advance(2 * time.Second)
	if pv := e.view(v, core.ViewOpts{}, "p"); len(pv.Previews) != 2 || pv.Previews[1].Host != "two.example" {
		t.Fatalf("after ready_at: %+v", pv.Previews)
	}
}

func TestExcerpt(t *testing.T) {
	cases := map[string]string{
		"":                                    "",
		"plain":                               "plain",
		"# Title\n\nSome *em* and __strong__": "Title Some em and strong",
		"> quoted\n- item one\n2. item two":   "quoted item one item two",
		"![alt text](https://x/y.png) [a](b)": "alt text a",
		"```go\ncode()\n```\nafter":           "code() after",
		"`inline` ~~gone~~ <https://e.com>":   "inline gone https://e.com",
		"line one\r\n\r\n   line   two":       "line one line two",
	}
	for in, want := range cases {
		if got := Excerpt(in, 80); got != want {
			t.Errorf("Excerpt(%q) = %q, want %q", in, got, want)
		}
	}
	long := ""
	for range 30 {
		long += "abcd "
	}
	if got := Excerpt(long, 80); len([]rune(got)) > 80 || got == "" {
		t.Fatalf("truncation: %q", got)
	}
	if got := Excerpt("ééééé", 3); got != "ééé" {
		t.Fatalf("runes: %q", got)
	}
}

// A Checked minted under an older search for the same viewer is refused: the
// graph changed (here an edge removal narrowed a d2 audience) since the check.
func TestPostsRefusesStaleChecked(t *testing.T) {
	e := newEnv(t, nil)
	v, f := e.w.User("v"), e.w.User("f")
	e.w.User("a")
	e.w.User("g")
	e.w.User("h")
	e.w.Friends("v", "f")
	e.w.Friends("f", "a")
	e.w.Friends("v", "g")
	e.w.Friends("g", "h")
	e.w.Friends("h", "a") // a stays reachable at dist 3 after v–f goes
	e.w.Post("a", "p", "x", coretest.Audience(core.AudD2))
	old := e.search(v)
	cs := e.checked(old, "p")
	e.m.D.Graph.RemoveEdge(v, f)
	fresh := e.search(v)
	if fresh.Epoch() == old.Epoch() {
		t.Fatal("edge removal must bump the epoch")
	}
	if _, ok := e.m.D.Vis.CheckPost(ctx, fresh, cs[0].Meta()); ok {
		t.Fatal("the d2 post must be out of reach after the removal")
	}
	if _, err := e.views.Posts(ctx, fresh, cs, core.ViewOpts{}); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("stale Checked accepted: %v", err)
	}
	if _, err := e.views.Posts(ctx, old, cs, core.ViewOpts{}); err != nil {
		t.Fatalf("matching search refused: %v", err)
	}
}

// A forced post stored wider than d1 still reads "d1" to others (D6), exactly
// like a voluntary d1; the author keeps the stored audience and the flag.
func TestForcedPostReadsD1WhateverIsStored(t *testing.T) {
	e := newEnv(t, nil)
	e.w.User("v")
	a := e.w.User("a")
	e.w.Friends("v", "a")
	e.w.Post("a", "p", "x", coretest.Audience(core.AudConnected), func(m *core.PostMeta, _ *[]core.LinkMeta) { m.AudienceForced = true })
	e.w.Post("a", "q", "y", coretest.Audience(core.AudD1))
	pv, vol := e.view(e.w.ID("v"), core.ViewOpts{}, "p"), e.view(e.w.ID("v"), core.ViewOpts{}, "q")
	if pv.Audience != "d1" || pv.AudienceForced || pv.Audience != vol.Audience {
		t.Fatalf("forced post leaks to others: %+v", pv)
	}
	if own := e.view(a, core.ViewOpts{}, "p"); !own.AudienceForced {
		t.Fatalf("the author sees the forced flag: %+v", own)
	}
}
