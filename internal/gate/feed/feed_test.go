package feed_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/feed"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coreref"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/posts"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/rank"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire/wiretest"
)

type env struct {
	t   *testing.T
	a   *wire.App
	d   *core.Deps
	f   *feed.Service
	w   *wiretest.TestWorld
	met *coreref.MemMetrics
	ctx context.Context
}

// newEnv: alice – bob – carol – dave – gina (a chain), erin – frank in another
// component. Every account is 90 days old (none is "low trust").
func newEnv(t *testing.T) *env {
	t.Helper()
	a := wiretest.NewApp(t)
	d := a.Deps()
	met := &coreref.MemMetrics{}
	d.Metrics = met
	e := &env{t: t, a: a, d: d, f: a.Feed, w: wiretest.World(t, a), met: met, ctx: context.Background()}
	for _, u := range []string{"alice", "bob", "carol", "dave", "gina", "erin", "frank"} {
		e.w.User(u)
	}
	e.w.Friends("alice", "bob")
	e.w.Friends("bob", "carol")
	e.w.Friends("carol", "dave")
	e.w.Friends("dave", "gina")
	e.w.Friends("erin", "frank")
	e.reloadUsers()
	e.advance(90 * 24 * time.Hour)
	return e
}

func (e *env) id(h string) core.UserID { return e.w.ID(h) }

func (e *env) as(h string) context.Context {
	return core.WithViewer(e.ctx, core.Viewer{ID: e.id(h), Kind: core.SessUser, AuthAt: e.d.Clock.Now()})
}

func (e *env) ref(viewer string, p core.PostID) gate.Ref { return e.d.Refs.Post(e.id(viewer), p) }

func (e *env) person(viewer, who string) gate.Ref { return e.d.Refs.User(e.id(viewer), e.id(who)) }

// unfriend removes the edge (fixture edges carry placeholder records, so the
// friends service's signed revocation is out of reach here; the feed only
// sees the graph).
func (e *env) unfriend(a, b string) {
	e.t.Helper()
	e.exec("DELETE FROM edges WHERE (user_id = $1 AND friend_id = $2) OR (user_id = $2 AND friend_id = $1)",
		int64(e.id(a)), int64(e.id(b)))
	e.d.Graph.RemoveEdge(e.id(a), e.id(b))
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

func (e *env) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.d.DB.Q().Exec(e.ctx, sql, args...); err != nil {
		e.t.Fatal(err)
	}
}

// post publishes one minute after the previous post (distinct timestamps).
func (e *env) post(h, body string) core.PostID {
	e.t.Helper()
	return e.postDraft(h, posts.Draft{Body: body, Lang: "en"})
}

func (e *env) postDraft(h string, d posts.Draft) core.PostID {
	e.t.Helper()
	e.advance(time.Minute)
	pv, err := e.a.Posts.Create(e.as(h), d)
	if err != nil {
		e.t.Fatalf("create by %s: %v", h, err)
	}
	id, ok := e.d.Refs.OpenPost(e.id(h), pv.Ref)
	if !ok {
		e.t.Fatal("ref does not open")
	}
	return id
}

func (e *env) thread(h string, bodies ...string) []core.PostID {
	e.t.Helper()
	e.advance(time.Minute)
	parts := make([]posts.Draft, len(bodies))
	for i, b := range bodies {
		parts[i] = posts.Draft{Body: b, Lang: "en"}
	}
	pvs, err := e.a.Posts.CreateThread(e.as(h), parts)
	if err != nil {
		e.t.Fatal(err)
	}
	out := make([]core.PostID, len(pvs))
	for i, pv := range pvs {
		out[i], _ = e.d.Refs.OpenPost(e.id(h), pv.Ref)
	}
	return out
}

func (e *env) like(h string, p core.PostID) {
	e.t.Helper()
	if err := e.a.Engage.Like(e.as(h), e.ref(h, p), true); err != nil {
		e.t.Fatalf("%s likes: %v", h, err)
	}
}

func (e *env) seen(h string, ps ...core.PostID) {
	e.t.Helper()
	refs := make([]gate.Ref, len(ps))
	for i, p := range ps {
		refs[i] = e.ref(h, p)
	}
	if err := e.a.Engage.MarkSeen(e.as(h), refs); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) page(h, preset, cursor string) gate.FeedPage {
	e.t.Helper()
	p, err := e.f.Page(e.as(h), preset, cursor)
	if err != nil {
		e.t.Fatalf("page for %s: %v", h, err)
	}
	return p
}

// describe renders a page as "start:1 b1 end:1 … caught_up" (posts by body,
// threads as "a|b|c", collapsed groups as "collapsed:<tier>x<count>").
func describe(t *testing.T, p gate.FeedPage) string {
	t.Helper()
	var out []string
	for _, it := range p.Items {
		switch it.Type {
		case feed.ItemTierStart:
			out = append(out, fmt.Sprintf("start:%d", it.Tier))
		case feed.ItemTierEnd:
			out = append(out, fmt.Sprintf("end:%d", it.Tier))
		case feed.ItemCaughtUp:
			out = append(out, "caught_up")
		case feed.ItemPost:
			out = append(out, it.Post.Body)
		case feed.ItemThread:
			var parts []string
			for _, pv := range it.Thread {
				parts = append(parts, pv.Body)
			}
			out = append(out, strings.Join(parts, "|"))
		case feed.ItemCollapsed:
			out = append(out, fmt.Sprintf("collapsed:%dx%d", it.Tier, it.Collapsed.Count))
		default:
			t.Fatalf("unknown item %q", it.Type)
		}
	}
	return strings.Join(out, " ")
}

// bodies lists the post bodies of a page in order.
func bodies(p gate.FeedPage) []string {
	var out []string
	for _, it := range p.Items {
		if it.Type == feed.ItemPost {
			out = append(out, it.Post.Body)
		}
	}
	return out
}

func wantNotFound(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("err = %v, want not found", err)
	}
}

func wantProblem(t *testing.T, err error, code string, status int) {
	t.Helper()
	var p *gate.Problem
	if !errors.As(err, &p) || p.Code != code || p.Status != status {
		t.Fatalf("err = %v, want %s (%d)", err, code, status)
	}
}

// Acceptance: the planner picks the author scan for small tiers and the time
// scan when |L_k| + P_k > W (synthetic counts).
func TestPlan(t *testing.T) {
	for _, c := range []struct {
		layer, posts, window int
		want                 feed.Scan
	}{
		{150, 300, 100_000, feed.ScanAuthor},         // tier 1: ~150 friends
		{20_000, 40_000, 7_000_000, feed.ScanAuthor}, // tier 2
		{10, 5, 15, feed.ScanAuthor},                 // tie goes to the author scan
		{10, 6, 15, feed.ScanTime},
		{900_000, 6_200_000, 7_000_000, feed.ScanTime}, // a tier covering most of the network
		{0, 0, 0, feed.ScanAuthor},
	} {
		if got := feed.Plan(c.layer, c.posts, c.window); got != c.want {
			t.Errorf("Plan(%d, %d, %d) = %s, want %s", c.layer, c.posts, c.window, got, c.want)
		}
	}
}

func TestPlannerChoiceIsRecorded(t *testing.T) {
	e := newEnv(t)
	var plans []string
	e.f.SetPlanHook(func(k int, s feed.Scan) { plans = append(plans, fmt.Sprintf("%d:%s", k, s)) })
	e.post("bob", "b1")
	e.post("carol", "c1")
	e.post("dave", "d1")
	e.post("erin", "e1")
	e.page("alice", "", "")
	if want := []string{"1:author", "2:author", "3:author", "4:author"}; !slices.Equal(plans, want) {
		t.Fatalf("plans %v, want %v", plans, want)
	}
	if e.met.Get(feed.MetricPlanner, "author") != 4 || e.met.Get(feed.MetricPlanner, "time") != 0 {
		t.Fatalf("planner metric %v", e.met.Counts)
	}

	// Only bob posts: tier 1 holds the whole window (1 + 3 > 3), so walking the
	// window is cheaper; the result is the same.
	e2 := newEnv(t)
	plans = nil
	e2.f.SetPlanHook(func(k int, s feed.Scan) { plans = append(plans, fmt.Sprintf("%d:%s", k, s)) })
	for _, b := range []string{"b1", "b2", "b3"} {
		e2.post("bob", b)
	}
	p := e2.page("alice", "latest", "")
	if got := describe(t, p); got != "start:1 b3 b2 b1 end:1 caught_up" {
		t.Fatalf("page %q", got)
	}
	if !slices.Contains(plans, "1:time") || e2.met.Get(feed.MetricPlanner, "time") != 1 {
		t.Fatalf("plans %v", plans)
	}
	if len(e2.met.Obs[feed.MetricLatency]) != 1 {
		t.Fatal("latency not observed")
	}
}

func TestTiersMarkersAndCaughtUp(t *testing.T) {
	e := newEnv(t)
	e.post("bob", "b1")
	e.post("carol", "c1")
	e.post("dave", "d1")
	e.post("gina", "g1")
	e.post("alice", "mine") // the reader's own posts are never candidates (D16)
	e.post("erin", "far")   // another component: no path, never shown
	p := e.page("alice", "", "")
	if got := describe(t, p); got != "start:1 b1 end:1 start:2 c1 end:2 start:3 d1 end:3 start:4 g1 end:4 caught_up" {
		t.Fatalf("page %q", got)
	}
	if p.Next != "" || p.Session == "" || p.Preset != "balanced" {
		t.Fatalf("page %+v", p)
	}
	for _, it := range p.Items {
		if it.Type == feed.ItemPost && it.Post.Tier != it.Post.Author.Chain.Dist {
			t.Fatalf("post tier %d, chain %d", it.Post.Tier, it.Post.Author.Chain.Dist)
		}
	}
	if got := bodies(p); got[1] != "c1" || p.Items[4].Post.Author.Chain.FirstHop.Handle != "bob" {
		t.Fatalf("chain of c1: %+v", p.Items[4].Post.Author.Chain)
	}

	// Empty tiers emit nothing.
	e2 := newEnv(t)
	e2.post("carol", "c1")
	if got := describe(t, e2.page("alice", "", "")); got != "start:2 c1 end:2 caught_up" {
		t.Fatalf("page %q", got)
	}
	// Nothing at all: just caught up.
	if got := describe(t, newEnv(t).page("alice", "", "")); got != "caught_up" {
		t.Fatalf("empty feed %q", got)
	}
}

// Acceptance: unseen-first strictness — seen posts never come back, and a
// tier-2 post never precedes an unseen tier-1 one however new or liked it is.
func TestUnseenFirstStrict(t *testing.T) {
	e := newEnv(t)
	e.w.User("fran")
	e.w.Friends("alice", "fran")
	e.reloadUsers()
	old := e.post("bob", "tier-1 old")
	seen := e.post("bob", "tier-1 seen")
	c := e.post("carol", "tier-2 new and liked")
	cSeen := e.post("carol", "tier-2 seen")
	e.like("bob", c)
	e.like("fran", c)
	e.seen("alice", seen, cSeen)
	for _, preset := range []string{"latest", "balanced"} {
		if got := describe(t, e.page("alice", preset, "")); got != "start:1 tier-1 old end:1 start:2 tier-2 new and liked end:2 caught_up" {
			t.Fatalf("%s: %q", preset, got)
		}
	}
	_ = old
}

// Acceptance: every candidate of a tier is in the snapshot (no prefix
// ranking), ranked as a whole: an old post the reader's network liked can
// outrank 24 newer ones and comes first.
func TestSnapshotRanksTheWholeTier(t *testing.T) {
	e := newEnv(t)
	friends := []string{"fa1", "fa2", "fa3", "fa4", "fa5"}
	for _, f := range friends {
		e.w.User(f)
		e.w.Friends("alice", f)
	}
	e.reloadUsers()
	var all []string
	first := e.post("fa1", "p00")
	all = append(all, "p00")
	for i := 1; i < 25; i++ {
		b := fmt.Sprintf("p%02d", i)
		e.post(friends[i%len(friends)], b)
		all = append(all, b)
	}
	for _, liker := range []string{"bob", "fa2", "fa3", "fa4", "fa5"} {
		e.like(liker, first)
	}
	p1 := e.page("alice", "balanced", "")
	got := bodies(p1)
	if len(got) != feed.PageSize || got[0] != "p00" {
		t.Fatalf("page 1 %v", got)
	}
	if p1.Items[0].Type != feed.ItemTierStart || p1.Items[len(p1.Items)-1].Type == feed.ItemTierEnd || p1.Next == "" {
		t.Fatalf("page 1 markers %q", describe(t, p1))
	}
	p2 := e.page("alice", "", p1.Next)
	if d := describe(t, p2); !strings.HasSuffix(d, "end:1 caught_up") || strings.Contains(d, "start:1") {
		t.Fatalf("page 2 %q", d)
	}
	got = append(got, bodies(p2)...)
	slices.Sort(got)
	if !slices.Equal(got, all) {
		t.Fatalf("served %v, want every candidate once", got)
	}

	// Latest is exactly chronological, newest first.
	p := e.page("alice", "latest", "")
	if b := bodies(p); b[0] != "p24" || b[19] != "p05" {
		t.Fatalf("latest %v", b)
	}
	// Under Balanced the author cap holds: no author more than twice in any 10 slots.
	authors := []core.UserID{}
	for _, it := range p1.Items {
		if it.Type == feed.ItemPost {
			id, _ := e.d.Refs.OpenUser(e.id("alice"), it.Post.Author.Ref)
			authors = append(authors, id)
		}
	}
	for i := 0; i+rank.CapWindow <= len(authors); i++ {
		n := map[core.UserID]int{}
		for _, a := range authors[i : i+rank.CapWindow] {
			if n[a]++; n[a] > rank.CapPerAuthor {
				t.Fatalf("author cap broken at %d: %v", i, authors)
			}
		}
	}
}

// Acceptance: pages never reshuffle while new posts arrive; new posts in the
// tiers already reached are counted for the "new posts above" banner.
func TestPagesNeverReshuffle(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 25; i++ {
		e.post("bob", fmt.Sprintf("old%02d", i))
	}
	e.post("carol", "c-old")
	p1 := e.page("alice", "latest", "")
	if n, k, err := e.f.NewAbove(e.as("alice"), p1.Session); err != nil || n != 0 || k != 0 {
		t.Fatalf("new above %d %d %v", n, k, err)
	}
	e.post("bob", "new1")
	e.post("bob", "new2")
	e.post("carol", "c-new") // tier 2 is not reached yet
	n, k, err := e.f.NewAbove(e.as("alice"), p1.Session)
	if err != nil || n != 2 || k != 1 {
		t.Fatalf("new above %d %d %v", n, k, err)
	}
	p2 := e.page("alice", "", p1.Next)
	if got := describe(t, p2); got != "old04 old03 old02 old01 old00 end:1 start:2 c-new c-old end:2 caught_up" {
		t.Fatalf("page 2 %q", got)
	}
	// Tier 2 was snapshotted when it was reached, c-new included; only posts
	// newer than a tier's snapshot are "new above".
	e.post("carol", "c-newer")
	if n, k, _ = e.f.NewAbove(e.as("alice"), p1.Session); n != 3 || k != 1 {
		t.Fatalf("after reaching tier 2: %d %d", n, k)
	}
	// Starting over shows them.
	if b := bodies(e.page("alice", "latest", "")); b[0] != "new2" || b[1] != "new1" {
		t.Fatalf("fresh session %v", b)
	}
}

// Acceptance: after an unfriend, block or deletion mid-session (and a mute,
// hide or audience-limited part), the next page omits the affected posts.
func TestNextPageRechecks(t *testing.T) {
	setup := func(t *testing.T) (*env, gate.FeedPage, []core.PostID) {
		e := newEnv(t)
		var ids []core.PostID
		for i := 0; i < 22; i++ {
			ids = append(ids, e.post("bob", fmt.Sprintf("b%02d", i)))
		}
		e.post("carol", "c1")
		p1 := e.page("alice", "latest", "")
		if got := bodies(p1); len(got) != 20 || got[0] != "b21" {
			t.Fatalf("page 1 %v", got)
		}
		return e, p1, ids
	}
	for _, c := range []struct {
		name   string
		mutate func(e *env, ids []core.PostID)
		want   string
	}{
		{"unchanged", func(*env, []core.PostID) {}, "b01 b00 end:1 start:2 c1 end:2 caught_up"},
		{"unfriend", func(e *env, _ []core.PostID) {
			e.unfriend("alice", "bob")
		}, "caught_up"},
		{"block", func(e *env, _ []core.PostID) {
			if err := e.a.Friends.Block(e.as("alice"), e.person("alice", "bob")); err != nil {
				e.t.Fatal(err)
			}
		}, "caught_up"},
		{"mute", func(e *env, _ []core.PostID) {
			if err := e.a.Friends.Mute(e.as("alice"), e.person("alice", "bob"), true); err != nil {
				e.t.Fatal(err)
			}
		}, "end:1 start:2 c1 end:2 caught_up"},
		{"delete", func(e *env, ids []core.PostID) {
			if err := e.a.Posts.Delete(e.as("bob"), e.ref("bob", ids[0])); err != nil {
				e.t.Fatal(err)
			}
		}, "b01 end:1 start:2 c1 end:2 caught_up"},
		{"hide", func(e *env, ids []core.PostID) {
			if err := e.a.Engage.Hide(e.as("alice"), e.ref("alice", ids[1])); err != nil {
				e.t.Fatal(err)
			}
		}, "b00 end:1 start:2 c1 end:2 caught_up"},
		{"carol unfriends bob", func(e *env, _ []core.PostID) {
			e.unfriend("carol", "bob")
		}, "b01 b00 end:1 caught_up"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e, p1, ids := setup(t)
			e.t = t
			c.mutate(e, ids)
			if got := describe(t, e.page("alice", "", p1.Next)); got != c.want {
				t.Fatalf("page 2 %q, want %q", got, c.want)
			}
		})
	}
}

// Acceptance: another viewer's cursor, session or token ⇒ not found (as is
// garbage); the viewer's own expired session ⇒ feed_session_expired.
func TestForeignAndExpiredSessions(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 21; i++ {
		e.post("bob", fmt.Sprintf("b%02d", i))
	}
	p := e.page("alice", "", "")
	if p.Next == "" {
		t.Fatal("no next page")
	}
	// bob is a signed-in user too; carol sees the same posts as alice could.
	for _, h := range []string{"bob", "carol"} {
		_, err := e.f.Page(e.as(h), "", p.Next)
		wantNotFound(t, err)
		_, _, err = e.f.NewAbove(e.as(h), p.Session)
		wantNotFound(t, err)
	}
	for _, garbage := range []string{"x", p.Session, p.Next[:len(p.Next)-2] + "AA", strings.Repeat("A", 40)} {
		_, err := e.f.Page(e.as("alice"), "", garbage)
		wantNotFound(t, err)
	}
	_, _, err := e.f.NewAbove(e.as("alice"), p.Next) // a cursor is not a session
	wantNotFound(t, err)
	_, err = e.f.Expand(e.as("alice"), p.Session)
	wantNotFound(t, err)
	_, err = e.f.Page(e.ctx, "", "") // signed out
	wantNotFound(t, err)

	// 29 minutes later the session is still alive (and refreshed by the use)…
	e.advance(29 * time.Minute)
	if _, _, err := e.f.NewAbove(e.as("alice"), p.Session); err != nil {
		t.Fatal(err)
	}
	// … and 30 unused minutes later it expired.
	e.advance(30 * time.Minute)
	_, err = e.f.Page(e.as("alice"), "", p.Next)
	wantProblem(t, err, feed.CodeSessionExpired, gate.StatusConflict)
	_, _, err = e.f.NewAbove(e.as("alice"), p.Session)
	wantProblem(t, err, feed.CodeSessionExpired, gate.StatusConflict)
	// Another viewer still learns nothing (not "expired").
	_, err = e.f.Page(e.as("bob"), "", p.Next)
	wantNotFound(t, err)
	if e.met.Get(feed.MetricTierReached, "1") != 1 {
		t.Fatalf("tier reached %v", e.met.Counts)
	}
}

func TestSessionsPerViewer(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 21; i++ {
		e.post("bob", fmt.Sprintf("b%02d", i))
	}
	var pages []gate.FeedPage
	for i := 0; i < feed.SessionsPerUser+1; i++ {
		pages = append(pages, e.page("alice", "", ""))
	}
	e.page("bob", "", "")
	if n := e.f.Sessions(); n != feed.SessionsPerUser+1 {
		t.Fatalf("%d sessions kept", n)
	}
	_, err := e.f.Page(e.as("alice"), "", pages[0].Next)
	wantProblem(t, err, feed.CodeSessionExpired, gate.StatusConflict)
	for _, p := range pages[1:] {
		if _, err := e.f.Page(e.as("alice"), "", p.Next); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPresets(t *testing.T) {
	e := newEnv(t)
	e.post("bob", "b1")
	for _, p := range []string{"top", "chronological", "Latest"} {
		_, err := e.f.Page(e.as("alice"), p, "")
		wantProblem(t, err, feed.CodePresetUnavailable, gate.StatusUnprocessable)
	}
	if p := e.page("alice", "", ""); p.Preset != "balanced" {
		t.Fatalf("default preset %s", p.Preset)
	}
	e.exec("UPDATE users SET rank_preset = 0 WHERE id = $1", int64(e.id("alice")))
	e.reloadUsers()
	if p := e.page("alice", "", ""); p.Preset != "latest" {
		t.Fatalf("saved preset %s", p.Preset)
	}
	if e.met.Get(feed.MetricPresetSwitch, "latest") != 1 {
		t.Fatalf("preset switch %v", e.met.Counts)
	}
	p := e.page("alice", "balanced", "")
	if p.Preset != "balanced" || e.met.Get(feed.MetricPresetSwitch, "balanced") != 1 {
		t.Fatalf("explicit preset %s %v", p.Preset, e.met.Counts)
	}
	// A cursor keeps its session's preset.
	for i := 0; i < 21; i++ {
		e.post("bob", fmt.Sprintf("b%02d", i))
	}
	p = e.page("alice", "latest", "")
	if q := e.page("alice", "balanced", p.Next); q.Preset != "latest" {
		t.Fatalf("continued preset %s", q.Preset)
	}
}

// A continuation thread is one feed item ranked as its newest unseen visible
// part, earlier parts as context; a thread whose parts are all seen is gone.
func TestThreadIsOneItem(t *testing.T) {
	e := newEnv(t)
	parts := e.thread("bob", "part one", "part two", "part three")
	e.post("bob", "single")
	if got := describe(t, e.page("alice", "latest", "")); got != "start:1 single part one|part two|part three end:1 caught_up" {
		t.Fatalf("page %q", got)
	}
	p := e.page("alice", "latest", "")
	th := p.Items[2].Thread
	if th[0].Thread == nil || th[0].Thread.Index != 1 || th[2].Thread.Total != 3 {
		t.Fatalf("thread positions %+v", th[0].Thread)
	}
	e.seen("alice", parts[2])
	if got := describe(t, e.page("alice", "latest", "")); got != "start:1 single part one|part two end:1 caught_up" {
		t.Fatalf("after seeing part three %q", got)
	}
	e.seen("alice", parts[0], parts[1])
	if got := describe(t, e.page("alice", "latest", "")); got != "start:1 single end:1 caught_up" {
		t.Fatalf("all parts seen %q", got)
	}
	// Continuations are not replies: no reply penalty.
	why, err := e.f.Why(e.as("alice"), e.ref("alice", parts[2]))
	if err != nil || slices.Contains(why.Reasons, rank.ReasonReplyPenalty) {
		t.Fatalf("why %+v %v", why, err)
	}
}

// §4.4: from tier 3 on, posts of accounts younger than 60 days fold into one
// collapsed item per tier; closer tiers never collapse.
func TestLowTrustCollapse(t *testing.T) {
	e := newEnv(t)
	e.w.User("yuri") // young, friend of bob: tier 2
	e.w.User("zoe")  // young, friend of carol: tier 3
	e.w.Friends("bob", "yuri")
	e.w.Friends("carol", "zoe")
	e.exec("UPDATE users SET created_at = $1 WHERE handle IN ('yuri', 'zoe')", e.d.Clock.Now().Add(-10*24*time.Hour))
	e.reloadUsers()
	e.post("dave", "d1")
	e.post("zoe", "z1")
	e.post("zoe", "z2")
	e.post("yuri", "y1")
	e.post("dave", "d2")
	p := e.page("alice", "latest", "")
	if got := describe(t, p); got != "start:2 y1 end:2 start:3 d2 collapsed:3x2 d1 end:3 caught_up" {
		t.Fatalf("page %q", got)
	}
	var token string
	for _, it := range p.Items {
		if it.Type == feed.ItemCollapsed {
			token = it.Collapsed.Token
			if it.Collapsed.Reason != feed.ReasonLowTrust {
				t.Fatalf("reason %q", it.Collapsed.Reason)
			}
		}
	}
	items, err := e.f.Expand(e.as("alice"), token)
	if err != nil || len(items) != 2 || items[0].Post.Body != "z2" || items[1].Post.Body != "z1" {
		t.Fatalf("expand %+v %v", items, err)
	}
	_, err = e.f.Expand(e.as("bob"), token)
	wantNotFound(t, err)
	// A deleted group member disappears from the count and the expansion.
	z2, _ := e.d.Refs.OpenPost(e.id("alice"), items[0].Post.Ref)
	if err := e.a.Posts.Delete(e.as("zoe"), e.ref("zoe", z2)); err != nil {
		t.Fatal(err)
	}
	if items, _ = e.f.Expand(e.as("alice"), token); len(items) != 1 {
		t.Fatalf("after delete %d", len(items))
	}
}

func TestWhy(t *testing.T) {
	e := newEnv(t)
	e.w.User("fran")
	e.w.Friends("alice", "fran")
	e.reloadUsers()
	c := e.post("carol", "c1")
	b := e.postDraft("bob", posts.Draft{Body: "reply", Lang: "en", Parents: []posts.ParentIn{{Ref: e.ref("bob", c), Kind: "ref"}}})
	far := e.post("erin", "far")
	e.like("fran", c)
	e.like("bob", c)
	e.advance(3 * time.Hour)

	why, err := e.f.Why(e.as("alice"), e.ref("alice", c))
	if err != nil {
		t.Fatal(err)
	}
	if why.Tier != 2 || why.Via.FirstHop == nil || why.Via.FirstHop.Handle != "bob" || why.Preset != "balanced" ||
		why.ShiftMinutes <= 0 || !slices.Contains(why.Reasons, rank.ReasonNetworkLikes) {
		t.Fatalf("why %+v", why)
	}
	why, err = e.f.Why(e.as("alice"), e.ref("alice", b))
	if err != nil || why.Tier != 1 || why.ShiftMinutes != -6*60 || !slices.Contains(why.Reasons, rank.ReasonReplyPenalty) {
		t.Fatalf("why reply %+v %v", why, err)
	}
	e.exec("UPDATE users SET rank_preset = 0 WHERE id = $1", int64(e.id("alice")))
	e.reloadUsers()
	why, _ = e.f.Why(e.as("alice"), e.ref("alice", c))
	if why.Preset != "latest" || why.ShiftMinutes != 0 || !slices.Equal(why.Reasons, []string{rank.ReasonChronological}) {
		t.Fatalf("why latest %+v", why)
	}
	for _, r := range []gate.Ref{e.ref("alice", far), e.ref("bob", c), "garbage"} {
		_, err := e.f.Why(e.as("alice"), r)
		wantNotFound(t, err)
	}
}

func TestAccountDeletionDropsSessions(t *testing.T) {
	e := newEnv(t)
	e.post("bob", "b1")
	e.page("alice", "", "")
	e.d.Bus.Publish(core.AccountDeleted{ID: e.id("alice")})
	if n := e.f.Sessions(); n != 0 {
		t.Fatalf("%d sessions left", n)
	}
	e.page("alice", "", "")
	e.f.Reset()
	if n := e.f.Sessions(); n != 0 {
		t.Fatalf("%d sessions after reset", n)
	}
}

// INT-0 conformance finding (§10.1.3): a post whose only parent the reader
// cannot see ranks exactly like a root (no reply penalty); a reply to a post
// the reader sees keeps it.
func TestHiddenParentIsNoReplyForRanking(t *testing.T) {
	e := newEnv(t)
	e.w.Post("frank", "far", "in another component")
	e.w.Post("bob", "near", "visible root")
	e.w.Post("bob", "r-hidden", "replies to far", e.w.ReplyTo("far"))
	e.w.Post("carol", "r-visible", "replies to near", e.w.ReplyTo("near"))
	reasons := func(label string) []string {
		why, err := e.f.Why(e.as("alice"), e.ref("alice", e.w.PostID(label)))
		if err != nil {
			t.Fatalf("why %s: %v", label, err)
		}
		return why.Reasons
	}
	if slices.Contains(reasons("r-hidden"), rank.ReasonReplyPenalty) {
		t.Fatalf("a reply to an invisible post is penalized: %v", reasons("r-hidden"))
	}
	if !slices.Contains(reasons("r-visible"), rank.ReasonReplyPenalty) {
		t.Fatalf("a reply to a visible post is not penalized: %v", reasons("r-visible"))
	}
}
