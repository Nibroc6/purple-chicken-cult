package engage_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/engage"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coreref"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coretest"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/posts"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/rank"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire/wiretest"
)

type env struct {
	t   *testing.T
	a   *wire.App
	d   *core.Deps
	e   *engage.Service
	w   *wiretest.TestWorld
	met *coreref.MemMetrics
	ctx context.Context
}

// newEnv: alice – bob – carol – dave (a chain), erin – frank in another
// component.
func newEnv(t *testing.T) *env {
	t.Helper()
	a := wiretest.NewApp(t)
	d := a.Deps()
	met := &coreref.MemMetrics{}
	d.Metrics = met
	e := &env{t: t, a: a, d: d, e: a.Engage, w: wiretest.World(t, a), met: met, ctx: context.Background()}
	for _, u := range []string{"alice", "bob", "carol", "dave", "erin", "frank"} {
		e.w.User(u)
	}
	e.w.Friends("alice", "bob")
	e.w.Friends("bob", "carol")
	e.w.Friends("carol", "dave")
	e.w.Friends("erin", "frank")
	return e
}

func (e *env) id(h string) core.UserID { return e.w.ID(h) }

func (e *env) as(h string) context.Context {
	return core.WithViewer(e.ctx, core.Viewer{ID: e.id(h), Kind: core.SessUser, AuthAt: e.d.Clock.Now()})
}

func (e *env) ref(viewer string, p core.PostID) gate.Ref { return e.d.Refs.Post(e.id(viewer), p) }

// post publishes through the posts service and returns the post's ID.
func (e *env) post(h, body string, parents ...posts.ParentIn) core.PostID {
	e.t.Helper()
	pv, err := e.a.Posts.Create(e.as(h), posts.Draft{Body: body, Lang: "en", Parents: parents})
	if err != nil {
		e.t.Fatalf("create by %s: %v", h, err)
	}
	id, ok := e.d.Refs.OpenPost(e.id(h), pv.Ref)
	if !ok {
		e.t.Fatal("ref does not open")
	}
	return id
}

func (e *env) reply(h string, parent core.PostID) core.PostID {
	e.t.Helper()
	return e.post(h, "a reply", posts.ParentIn{Ref: e.ref(h, parent), Kind: "ref"})
}

func (e *env) like(h string, p core.PostID) {
	e.t.Helper()
	if err := e.e.Like(e.as(h), e.ref(h, p), true); err != nil {
		e.t.Fatalf("%s likes: %v", h, err)
	}
}

// checked is p as viewer can see it, with viewer's search.
func (e *env) checked(viewer string, p core.PostID) (core.Search, core.Checked) {
	e.t.Helper()
	sr, err := e.d.Vis.SearchFor(e.ctx, e.id(viewer))
	if err != nil {
		e.t.Fatal(err)
	}
	m, ok := e.d.Posts.Meta(e.ctx, p)
	if !ok {
		e.t.Fatal("no meta")
	}
	c, ok := e.d.Vis.CheckPost(e.ctx, sr, m)
	if !ok {
		e.t.Fatalf("%s cannot see the post", viewer)
	}
	return sr, c
}

func (e *env) n(viewer string, p core.PostID) float64 {
	e.t.Helper()
	sr, c := e.checked(viewer, p)
	return e.e.N(e.ctx, sr, c, core.NewMemo())
}

func (e *env) kids(viewer string, p core.PostID) []float64 {
	e.t.Helper()
	sr, c := e.checked(viewer, p)
	return e.e.KidsN(e.ctx, sr, c, core.NewMemo(), 1)
}

func (e *env) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.d.DB.Q().QueryRow(e.ctx, sql, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.d.DB.Q().Exec(e.ctx, sql, args...); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) advance(d time.Duration) {
	e.t.Helper()
	if err := e.a.Hooks().AdvanceClock(d); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) reloadUsers() {
	e.t.Helper()
	if err := e.a.Users.Reload(e.ctx); err != nil {
		e.t.Fatal(err)
	}
}

func wantNotFound(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("err = %v, want not found", err)
	}
}

func TestLikeAndLikers(t *testing.T) {
	e := newEnv(t)
	p := e.post("carol", "hello")
	var events []core.Liked
	core.Subscribe(e.d.Bus, func(ev core.Liked) { events = append(events, ev) })

	e.like("bob", p)
	e.like("bob", p) // idempotent
	if n := e.count("SELECT count(*) FROM engagements WHERE post_id = $1 AND kind = 1", p); n != 1 {
		t.Fatalf("rows %d", n)
	}
	ls, err := e.e.Likers(e.as("alice"), e.ref("alice", p))
	if err != nil {
		t.Fatal(err)
	}
	if ls.InNetwork != 1 || ls.ByMe || len(ls.Names) != 1 || ls.Names[0].Handle != "bob" || ls.Others != 0 {
		t.Fatalf("alice sees %+v", ls)
	}
	if ls.Names[0].Ref != e.d.Refs.User(e.id("alice"), e.id("bob")) {
		t.Fatal("name carries a foreign ref")
	}

	// dave is 3 hops from alice: his like is not "in alice's network".
	e.like("dave", p)
	if ls, _ = e.e.Likers(e.as("alice"), e.ref("alice", p)); ls.InNetwork != 1 {
		t.Fatalf("a like from 3 hops counted: %+v", ls)
	}
	// carol (the author) counts dave as a friend.
	if ls, _ = e.e.Likers(e.as("carol"), e.ref("carol", p)); ls.InNetwork != 2 {
		t.Fatalf("carol sees %+v", ls)
	}

	e.like("alice", p)
	ls, _ = e.e.Likers(e.as("alice"), e.ref("alice", p))
	if !ls.ByMe || ls.InNetwork != 2 {
		t.Fatalf("after alice's like %+v", ls)
	}
	if !e.e.LikedByMe(e.ctx, e.id("alice"), []core.PostID{p})[p] || !e.e.LikerSet(e.ctx, p).Contains(uint32(e.id("dave"))) {
		t.Fatal("memory does not hold the likes")
	}
	if err := e.e.Like(e.as("alice"), e.ref("alice", p), false); err != nil {
		t.Fatal(err)
	}
	if err := e.e.Like(e.as("alice"), e.ref("alice", p), false); err != nil {
		t.Fatal(err)
	}
	if ls, _ = e.e.Likers(e.as("alice"), e.ref("alice", p)); ls.ByMe || ls.InNetwork != 1 {
		t.Fatalf("after unlike %+v", ls)
	}
	// One event per change, none for the repeats.
	if len(events) != 4 || events[3].On || events[3].User != e.id("alice") || events[3].Post != p {
		t.Fatalf("events %+v", events)
	}
	// The snapshot is a copy.
	e.e.LikerSet(e.ctx, p).Add(42)
	if e.e.LikerSet(e.ctx, p).Contains(42) {
		t.Fatal("LikerSet leaked the cached bitmap")
	}
}

func TestLikersRespectNamingOptOut(t *testing.T) {
	e := newEnv(t)
	p := e.post("bob", "hello")
	e.exec("UPDATE users SET named_in_likes = false WHERE id = $1", int64(e.id("carol")))
	e.reloadUsers()
	e.w.Friends("alice", "carol")
	e.like("carol", p)
	ls, err := e.e.Likers(e.as("alice"), e.ref("alice", p))
	if err != nil {
		t.Fatal(err)
	}
	if ls.InNetwork != 1 || len(ls.Names) != 0 || ls.Others != 1 {
		t.Fatalf("opted-out friend named: %+v", ls)
	}
}

// Every write resolves its ref first: garbage, another viewer's ref, an
// invisible post and a deleted one all answer ErrNotFound and write nothing.
func TestUnknownRefsAreNotFound(t *testing.T) {
	e := newEnv(t)
	p := e.post("bob", "hello")
	far := e.post("erin", "far away")
	gone := e.post("bob", "soon gone")
	if err := e.a.Posts.Delete(e.as("bob"), e.ref("bob", gone)); err != nil {
		t.Fatal(err)
	}
	for _, r := range []gate.Ref{
		"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", // garbage
		e.ref("bob", p),      // another viewer's ref
		e.ref("alice", far),  // another component
		e.ref("alice", gone), // deleted
	} {
		ctx := e.as("alice")
		wantNotFound(t, e.e.Like(ctx, r, true))
		wantNotFound(t, e.e.Like(ctx, r, false))
		_, err := e.e.Likers(ctx, r)
		wantNotFound(t, err)
		wantNotFound(t, e.e.Hide(ctx, r))
		wantNotFound(t, e.e.ShowLess(ctx, r))
	}
	if n := e.count("SELECT (SELECT count(*) FROM engagements) + (SELECT count(*) FROM hides) + (SELECT count(*) FROM show_less)"); n != 0 {
		t.Fatalf("%d rows written", n)
	}
	// Not signed in: the same not-found.
	wantNotFound(t, e.e.Like(e.ctx, e.ref("alice", p), true))
	wantNotFound(t, e.e.MarkSeen(e.ctx, []gate.Ref{e.ref("alice", p)}))
	_, err := e.e.RecentlySeen(e.ctx, "")
	wantNotFound(t, err)
}

// Acceptance: MarkSeen with garbage refs succeeds and writes nothing; invisible
// posts are ignored; at most 50 count; first sightings feed the impressions metric.
func TestMarkSeen(t *testing.T) {
	e := newEnv(t)
	friend := e.post("bob", "tier one")
	fof := e.post("carol", "tier two")
	far := e.post("erin", "invisible")

	if err := e.e.MarkSeen(e.as("alice"), []gate.Ref{"garbage", "", e.ref("bob", friend), e.ref("alice", far)}); err != nil {
		t.Fatal(err)
	}
	if n := e.count("SELECT count(*) FROM seen"); n != 0 {
		t.Fatalf("garbage wrote %d rows", n)
	}
	if err := e.e.MarkSeen(e.as("alice"), []gate.Ref{e.ref("alice", friend), e.ref("alice", fof), e.ref("alice", fof),
		e.ref("alice", far)}); err != nil {
		t.Fatal(err)
	}
	seen := e.e.SeenSet(e.ctx, e.id("alice"), e.d.Clock.Now().Add(-engage.SeenRetention))
	if len(seen) != 2 {
		t.Fatalf("seen %v", seen)
	}
	if _, ok := seen[far]; ok {
		t.Fatal("an invisible post was marked seen")
	}
	if e.met.Get(engage.MetricImpressions, "1") != 1 || e.met.Get(engage.MetricImpressions, "2") != 1 {
		t.Fatalf("metrics %v", e.met.Counts)
	}
	// Seen again later: the time moves, the impression is not counted twice.
	e.advance(time.Hour)
	if err := e.e.MarkSeen(e.as("alice"), []gate.Ref{e.ref("alice", friend)}); err != nil {
		t.Fatal(err)
	}
	if e.met.Get(engage.MetricImpressions, "1") != 1 {
		t.Fatal("impression counted twice")
	}
	var at time.Time
	if err := e.d.DB.Q().QueryRow(e.ctx, "SELECT at FROM seen WHERE user_id = $1 AND post_id = $2", int64(e.id("alice")),
		friend).Scan(&at); err != nil || !at.Equal(e.d.Clock.Now().Truncate(time.Microsecond)) {
		t.Fatalf("seen at %v (%v)", at, err)
	}

	// Only the first 50 refs of a batch count.
	var refs []gate.Ref
	for i := range engage.SeenBatch + 5 {
		refs = append(refs, e.ref("alice", e.w.Post("bob", fmt.Sprintf("b%d", i), "bulk")))
	}
	if err := e.e.MarkSeen(e.as("alice"), refs); err != nil {
		t.Fatal(err)
	}
	if n := e.count("SELECT count(*) FROM seen WHERE user_id = $1", int64(e.id("alice"))); n != 2+engage.SeenBatch {
		t.Fatalf("seen rows %d", n)
	}

	// Seen state is kept 14 days.
	e.advance(engage.SeenRetention + time.Hour)
	if since := e.d.Clock.Now().Add(-engage.SeenRetention); len(e.e.SeenSet(e.ctx, e.id("alice"), since)) != 0 {
		t.Fatal("seen state outlived 14 days")
	}
	n, err := e.e.PurgeSeen(e.ctx)
	if err != nil || n != 2+engage.SeenBatch {
		t.Fatalf("purged %d (%v)", n, err)
	}
}

func TestRecentlySeenPages(t *testing.T) {
	e := newEnv(t)
	var ids []core.PostID
	for i := range 45 {
		ids = append(ids, e.w.Post("bob", fmt.Sprintf("p%02d", i), fmt.Sprintf("post %d", i)))
	}
	gone := e.post("bob", "deleted later")
	// Seen one by one: p44 was seen last.
	if err := e.e.MarkSeen(e.as("alice"), []gate.Ref{e.ref("alice", gone)}); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		e.advance(time.Second)
		if err := e.e.MarkSeen(e.as("alice"), []gate.Ref{e.ref("alice", id)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.a.Posts.Delete(e.as("bob"), e.ref("bob", gone)); err != nil {
		t.Fatal(err)
	}

	var got []gate.Ref
	cursor := ""
	for range 5 {
		page, err := e.e.RecentlySeen(e.as("alice"), cursor)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) > engage.SeenPageSize {
			t.Fatalf("page of %d", len(page.Items))
		}
		for _, pv := range page.Items {
			got = append(got, pv.Ref)
		}
		if page.Next == "" {
			break
		}
		cursor = page.Next
		// Another viewer's cursor is not found; so is a tampered one.
		_, err = e.e.RecentlySeen(e.as("bob"), cursor)
		wantNotFound(t, err)
		_, err = e.e.RecentlySeen(e.as("alice"), cursor+"x")
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
	// Nothing seen: an empty list, not null.
	page, err := e.e.RecentlySeen(e.as("carol"), "")
	if err != nil || page.Items == nil || len(page.Items) != 0 || page.Next != "" {
		t.Fatalf("empty page %+v (%v)", page, err)
	}
}

func TestHideAndShowLess(t *testing.T) {
	e := newEnv(t)
	p1 := e.post("bob", "one")
	p2 := e.post("carol", "two")
	p3 := e.post("carol", "three")
	own := e.post("alice", "mine")
	ctx := e.as("alice")
	if err := e.e.Hide(ctx, e.ref("alice", p1)); err != nil {
		t.Fatal(err)
	}
	if err := e.e.Hide(ctx, e.ref("alice", p1)); err != nil {
		t.Fatal(err)
	}
	for _, p := range []core.PostID{p2, p2, p3, own} { // the repeat on p2 counts once
		if err := e.e.ShowLess(ctx, e.ref("alice", p)); err != nil {
			t.Fatal(err)
		}
	}
	hidden := e.e.HiddenSet(e.ctx, e.id("alice"))
	for _, p := range []core.PostID{p1, p2, p3, own} {
		if _, ok := hidden[p]; !ok {
			t.Fatalf("post not hidden: %v", hidden)
		}
	}
	sl := e.e.ShowLessCounts(e.ctx, e.id("alice"))
	if len(sl) != 1 || sl[e.id("carol")] != 2 {
		t.Fatalf("show less %v", sl)
	}
	// Personal: nobody else's state changes.
	if len(e.e.HiddenSet(e.ctx, e.id("bob"))) != 0 || len(e.e.ShowLessCounts(e.ctx, e.id("bob"))) != 0 {
		t.Fatal("hides leaked to another reader")
	}
	// A month-old hide is kept (retention is one year, plan §10).
	e.advance(31 * 24 * time.Hour)
	if n, err := e.e.PurgeHides(e.ctx); err != nil || n != 0 {
		t.Fatalf("purged %d month-old hides (%v)", n, err)
	}
	if engage.HideRetention != 365*24*time.Hour {
		t.Fatalf("hide retention %v, want one year", engage.HideRetention)
	}
	e.advance(engage.HideRetention + time.Hour)
	if n, err := e.e.PurgeHides(e.ctx); err != nil || n != 4 {
		t.Fatalf("purged %d (%v)", n, err)
	}
	if len(e.e.ShowLessCounts(e.ctx, e.id("alice"))) != 1 {
		t.Fatal("the purge dropped show-less counts")
	}
}

func TestUnhideAndResetShowLess(t *testing.T) {
	e := newEnv(t)
	p1 := e.post("bob", "one")
	p2 := e.post("carol", "two")
	p3 := e.post("carol", "three")
	ctx := e.as("alice")
	if err := e.e.Hide(ctx, e.ref("alice", p1)); err != nil {
		t.Fatal(err)
	}
	for _, p := range []core.PostID{p2, p3} {
		if err := e.e.ShowLess(ctx, e.ref("alice", p)); err != nil {
			t.Fatal(err)
		}
	}
	page, err := e.e.Hidden(ctx, "")
	if err != nil || len(page.Items) != 3 {
		t.Fatalf("hidden page %+v (%v)", page, err)
	}
	// Undoing a show-less gives its step back; undoing twice changes nothing.
	for range 2 {
		if err := e.e.Unhide(ctx, e.ref("alice", p2)); err != nil {
			t.Fatal(err)
		}
	}
	if sl := e.e.ShowLessCounts(e.ctx, e.id("alice")); sl[e.id("carol")] != 1 {
		t.Fatalf("show less after undo %v", sl)
	}
	if _, ok := e.e.HiddenSet(e.ctx, e.id("alice"))[p2]; ok {
		t.Fatal("p2 still hidden")
	}
	// A plain hide leaves show-less alone.
	if err := e.e.Unhide(ctx, e.ref("alice", p1)); err != nil {
		t.Fatal(err)
	}
	list, err := e.e.ShowLessList(ctx)
	if err != nil || len(list) != 1 || list[0].Handle != "carol" || list[0].Count != 1 {
		t.Fatalf("show-less list %+v (%v)", list, err)
	}
	if err := e.e.ResetShowLess(ctx, list[0].Ref); err != nil {
		t.Fatal(err)
	}
	if sl := e.e.ShowLessCounts(e.ctx, e.id("alice")); len(sl) != 0 {
		t.Fatalf("show less after reset %v", sl)
	}
	// The reset keeps the post hidden, and undoing it later does not go below zero.
	if _, ok := e.e.HiddenSet(e.ctx, e.id("alice"))[p3]; !ok {
		t.Fatal("reset unhid p3")
	}
	if err := e.e.Unhide(ctx, e.ref("alice", p3)); err != nil {
		t.Fatal(err)
	}
	if n := e.count("SELECT count(*) FROM show_less"); n != 0 {
		t.Fatalf("%d show_less rows", n)
	}
	// Another viewer's ref does not open.
	if err := e.e.ResetShowLess(e.as("bob"), list[0].Ref); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("foreign ref: %v", err)
	}
}

// §5.5 n: friends 1.0, second degree 0.5, the author and probation accounts 0,
// one inviter's invitees together at most 4 units, nothing from 3 hops or beyond.
func TestPreferN(t *testing.T) {
	e := newEnv(t)
	for i := range 6 {
		h := fmt.Sprintf("inv%d", i)
		e.w.User(h, coretest.InvitedBy(e.id("frank")))
		e.w.Friends("alice", h)
	}
	e.w.User("probie", coretest.InvitedBy(e.id("frank")))
	e.w.Friends("alice", "probie")
	e.exec("UPDATE users SET probation_until = $2 WHERE id = $1", int64(e.id("probie")), e.d.Clock.Now().Add(30*24*time.Hour))
	e.w.User("fof")
	e.w.Friends("bob", "fof")
	e.reloadUsers()

	p := e.post("carol", "liked")
	if n := e.n("alice", p); n != 0 {
		t.Fatalf("n = %v with no likes", n)
	}
	e.like("bob", p) // friend: 1
	e.like("fof", p) // second degree: 0.5
	if n := e.n("alice", p); n != 1.5 {
		t.Fatalf("n = %v, want 1.5", n)
	}
	e.like("carol", p)  // the author: 0
	e.like("dave", p)   // 3 hops: nothing
	e.like("probie", p) // probation: 0
	e.like("alice", p)  // the reader herself is not her network
	if n := e.n("alice", p); n != 1.5 {
		t.Fatalf("n = %v after author/far/probation likes", n)
	}
	for i := range 6 {
		e.like(fmt.Sprintf("inv%d", i), p)
	}
	if n := e.n("alice", p); n != 1.5+engage.InviterCap {
		t.Fatalf("n = %v, want the inviter cap", n)
	}
	// Probation ends: the account counts (and shares frank's cap).
	e.exec("UPDATE users SET probation_cleared_at = $2 WHERE id = $1", int64(e.id("probie")), e.d.Clock.Now())
	e.a.Users.Invalidate(e.id("probie"))
	if n := e.n("alice", p); n != 1.5+engage.InviterCap {
		t.Fatalf("n = %v after probation", n)
	}
	// Memoized per snapshot: a memo keeps the first answer.
	sr, c := e.checked("alice", p)
	memo := core.NewMemo()
	first := e.e.N(e.ctx, sr, c, memo)
	if err := e.e.Like(e.as("bob"), e.ref("bob", p), false); err != nil {
		t.Fatal(err)
	}
	if e.e.N(e.ctx, sr, c, memo) != first || e.n("alice", p) != first-1 {
		t.Fatal("memo or unlike not honored")
	}
}

// Acceptance: invisible, detached and self children never change KidsN; a
// slippery parent gets no credit; the 20-child limit applies after the filters.
func TestPreferKidsN(t *testing.T) {
	e := newEnv(t)
	e.w.User("gina")
	e.w.Friends("carol", "gina")
	parent := e.post("bob", "parent")
	r1 := e.reply("carol", parent)
	r2 := e.reply("gina", parent)
	e.like("bob", r1)   // n(r1) for alice = 1
	e.like("carol", r2) // n(r2) for alice = 0.5
	base := e.kids("alice", parent)
	if !slices.Equal(slices.Sorted(slices.Values(base)), []float64{0.5, 1}) {
		t.Fatalf("kids %v", base)
	}

	// An invisible child: dave's friends-only reply (alice is 3 hops from dave).
	hidden, err := e.a.Posts.Create(e.as("dave"), posts.Draft{Body: "only friends", Lang: "en", Audience: "d1",
		Parents: []posts.ParentIn{{Ref: e.ref("dave", parent), Kind: "ref"}}})
	if err != nil {
		t.Fatal(err)
	}
	hid, _ := e.d.Refs.OpenPost(e.id("dave"), hidden.Ref)
	e.like("carol", hid)
	// A self-reply and a continuation by the parent's author.
	self := e.reply("bob", parent)
	e.like("alice", self)
	cont := e.post("bob", "part two", posts.ParentIn{Ref: e.ref("bob", parent), Kind: "cont"})
	e.like("carol", cont)
	// A detached reply.
	det := e.reply("carol", parent)
	e.like("bob", det)
	if err := e.a.Posts.Detach(e.as("bob"), e.ref("bob", parent), e.ref("bob", det)); err != nil {
		t.Fatal(err)
	}
	if got := e.kids("alice", parent); !slices.Equal(got, base) {
		t.Fatalf("kids changed to %v", got)
	}
	// Adding likes to invisible children never moves anything either.
	e.like("dave", hid)
	if err := e.e.Like(e.as("carol"), e.ref("carol", hid), false); err != nil {
		t.Fatal(err)
	}
	if got := e.kids("alice", parent); !slices.Equal(got, base) {
		t.Fatalf("kids changed to %v", got)
	}

	// Slippery ⇒ no credit at all.
	if _, err := e.a.Posts.SetRefPolicy(e.as("bob"), e.ref("bob", parent), "nobody"); err != nil {
		t.Fatal(err)
	}
	if got := e.kids("alice", parent); got != nil {
		t.Fatalf("slippery parent got %v", got)
	}

	// The limit: 25 visible children, then 5 newer invisible ones ⇒ the 20 newest visible.
	p2 := e.w.Post("bob", "popular", "popular")
	for i := range 25 {
		e.w.Post("carol", fmt.Sprintf("k%d", i), "reply", e.w.ReplyTo("popular"))
	}
	later := coretest.At(e.d.Clock.Now().Add(time.Minute))
	for i := range 5 {
		e.w.Post("dave", fmt.Sprintf("h%d", i), "hidden reply", e.w.ReplyTo("popular"), coretest.Audience(core.AudD1), later)
	}
	if got := e.kids("alice", p2); len(got) != engage.KidsK {
		t.Fatalf("%d kids, want %d", len(got), engage.KidsK)
	}
}

// Acceptance: likes from users 3+ hops away never change the ranked order.
func TestFarLikesNeverChangeOrder(t *testing.T) {
	e := newEnv(t)
	e.w.User("fof")
	e.w.Friends("bob", "fof")
	var ps []core.PostID
	for i := range 8 {
		ps = append(ps, e.post([]string{"bob", "carol", "fof"}[i%3], fmt.Sprintf("post %d", i)))
		e.advance(17 * time.Minute)
	}
	e.like("fof", ps[0])
	e.like("bob", ps[3])
	e.like("carol", ps[5])
	p := rank.ParamsFor(core.PresetBalanced)
	order := func() [][16]byte {
		sr, err := e.d.Vis.SearchFor(e.ctx, e.id("alice"))
		if err != nil {
			t.Fatal(err)
		}
		memo := core.NewMemo()
		var cs []rank.Candidate
		for _, id := range ps {
			_, c := e.checked("alice", id)
			m := c.Meta()
			cs = append(cs, rank.Candidate{Key: m.ID, Author: uint32(m.Author), CreatedAt: m.CreatedAt,
				N: e.e.N(e.ctx, sr, c, memo), KidsN: e.e.KidsN(e.ctx, sr, c, memo, 1)})
		}
		var out [][16]byte
		for _, s := range rank.RankTier(p, e.d.Clock.Now(), cs) {
			out = append(out, s.Key)
		}
		return out
	}
	before := order()
	for _, id := range ps {
		e.like("dave", id) // 3 hops from alice
	}
	if after := order(); !slices.Equal(before, after) {
		t.Fatal("likes from 3 hops changed the order")
	}
	// The in-network likes did move posts (the check above is not vacuous).
	var chrono [][16]byte
	for i := len(ps) - 1; i >= 0; i-- {
		chrono = append(chrono, ps[i])
	}
	if slices.Equal(before, chrono) {
		t.Fatal("balanced order is chronological despite network likes")
	}
}

// AccountDeleted drops everything the account engaged with; PostDeleted drops
// the post's likes.
func TestDeletionCleanUp(t *testing.T) {
	e := newEnv(t)
	p := e.post("carol", "liked")
	q := e.post("alice", "also liked")
	e.like("bob", p)
	e.like("bob", q)
	e.like("alice", p)
	if err := e.e.MarkSeen(e.as("bob"), []gate.Ref{e.ref("bob", p)}); err != nil {
		t.Fatal(err)
	}
	if err := e.e.ShowLess(e.as("bob"), e.ref("bob", p)); err != nil {
		t.Fatal(err)
	}
	e.d.Bus.Publish(core.AccountDeleted{ID: e.id("bob")})
	bob := int64(e.id("bob"))
	if n := e.count(`SELECT (SELECT count(*) FROM engagements WHERE user_id = $1) + (SELECT count(*) FROM seen WHERE user_id = $1)
		+ (SELECT count(*) FROM hides WHERE user_id = $1) + (SELECT count(*) FROM show_less WHERE user_id = $1)`, bob); n != 0 {
		t.Fatalf("%d rows left", n)
	}
	if e.e.LikerSet(e.ctx, p).Contains(uint32(bob)) || e.e.LikerSet(e.ctx, q).Contains(uint32(bob)) {
		t.Fatal("memory still holds the deleted account's likes")
	}
	if err := e.a.Posts.Delete(e.as("carol"), e.ref("carol", p)); err != nil {
		t.Fatal(err)
	}
	if !e.e.LikerSet(e.ctx, p).IsEmpty() || e.count("SELECT count(*) FROM engagements WHERE post_id = $1", p) != 0 {
		t.Fatal("a deleted post kept its likes")
	}
}

// Likes written straight to the database (fixtures) show up after Load.
func TestLoadReadsTheDatabase(t *testing.T) {
	e := newEnv(t)
	e.w.Post("carol", "c1", "seeded")
	p := e.w.PostID("c1")
	if !e.e.LikerSet(e.ctx, p).IsEmpty() {
		t.Fatal("likes before any like")
	}
	e.w.Like("bob", "c1")
	if err := e.e.Load(e.ctx); err != nil {
		t.Fatal(err)
	}
	if !e.e.LikerSet(e.ctx, p).Contains(uint32(e.id("bob"))) {
		t.Fatal("Load missed a stored like")
	}
	if n := e.n("alice", p); n != 1 {
		t.Fatalf("n = %v", n)
	}
	// Uncached posts load on demand.
	e.w.Post("carol", "c2", "older")
	e.w.Like("bob", "c2")
	if !e.e.LikedByMe(e.ctx, e.id("bob"), []core.PostID{e.w.PostID("c2")})[e.w.PostID("c2")] {
		t.Fatal("on-demand load missed a like")
	}
}
