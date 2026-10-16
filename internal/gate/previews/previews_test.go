package previews_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/posts"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/previews"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire/wiretest"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/clock"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/safefetch"
)

// page is what the fake web serves for one URL.
type page struct {
	html  string
	err   error
	delay time.Duration // advances the fake clock while "fetching"
}

// fakeWeb is the fetcher: canned pages by URL, a call log, and a clock it can
// advance to simulate slow sites.
type fakeWeb struct {
	mu    sync.Mutex
	clk   *clock.Fake
	pages map[string]page
	calls []string
}

func (f *fakeWeb) GetHTML(_ context.Context, u *url.URL) (*safefetch.Response, error) {
	f.mu.Lock()
	f.calls = append(f.calls, u.String())
	p, ok := f.pages[u.String()]
	f.mu.Unlock()
	if p.delay > 0 {
		f.clk.Advance(p.delay)
	}
	if !ok {
		return nil, safefetch.ErrStatus
	}
	if p.err != nil {
		return nil, p.err
	}
	return &safefetch.Response{FinalURL: u, ContentType: "text/html", Body: []byte(p.html)}, nil
}

func (f *fakeWeb) set(u string, p page) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pages[u] = p
}

func (f *fakeWeb) called() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

type env struct {
	t   *testing.T
	a   *wire.App
	d   *core.Deps
	clk *clock.Fake
	web *fakeWeb
	ctx context.Context
	ids map[string]core.UserID
}

// newEnv: alice and bob are friends; carol is not connected.
func newEnv(t *testing.T, opts ...wiretest.Option) *env {
	t.Helper()
	a := wiretest.NewApp(t, opts...)
	d := a.Deps()
	clk := d.Clock.(*clock.Fake)
	e := &env{t: t, a: a, d: d, clk: clk, ctx: context.Background(),
		web: &fakeWeb{clk: clk, pages: map[string]page{}}}
	a.Previews.SetFetcher(e.web)
	w := wiretest.World(t, a)
	for _, h := range []string{"alice", "bob", "carol"} {
		e.ids = mapSet(e.ids, h, w.User(h))
	}
	w.Friends("alice", "bob")
	return e
}

func mapSet(m map[string]core.UserID, k string, v core.UserID) map[string]core.UserID {
	if m == nil {
		m = map[string]core.UserID{}
	}
	m[k] = v
	return m
}

func (e *env) as(h string) context.Context {
	return core.WithViewer(e.ctx, core.Viewer{ID: e.ids[h], Kind: core.SessUser, AuthAt: e.clk.Now()})
}

func (e *env) post(h, body string) core.PostID {
	e.t.Helper()
	return e.create(h, posts.Draft{Body: body, Lang: "en"})
}

func (e *env) create(h string, d posts.Draft) core.PostID {
	e.t.Helper()
	pv, err := e.a.Posts.Create(e.as(h), d)
	if err != nil {
		e.t.Fatalf("create: %v", err)
	}
	id, ok := e.d.Refs.OpenPost(e.ids[h], pv.Ref)
	if !ok {
		e.t.Fatal("ref")
	}
	return id
}

func (e *env) drain() {
	e.t.Helper()
	if err := e.a.DrainJobs(e.ctx); err != nil {
		e.t.Fatalf("drain: %v", err)
	}
}

// shown returns the previews viewer sees on post p.
func (e *env) shown(viewer string, p core.PostID) []gate.PreviewView {
	e.t.Helper()
	pv, err := e.a.Posts.Get(e.as(viewer), e.d.Refs.Post(e.ids[viewer], p))
	if err != nil {
		e.t.Fatalf("get: %v", err)
	}
	return pv.Previews
}

func (e *env) rows(p core.PostID) []string {
	e.t.Helper()
	rs, err := e.d.DB.Q().Query(e.ctx, `SELECT ord, url_canon, status, ready_at FROM link_previews WHERE post_id = $1 ORDER BY ord`, p)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rs.Close()
	var out []string
	for rs.Next() {
		var (
			ord, st int16
			u       string
			ready   time.Time
		)
		if err := rs.Scan(&ord, &u, &st, &ready); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("%d %s %d", ord, u, st))
	}
	return out
}

const article = `<!doctype html><html><head>
<title>Fallback title</title>
<meta property="og:title" content="  The &amp; real
  title ` + "\u202E" + `evil` + "\u200B" + `  ">
<meta name="description" content="Plain description">
<meta property="og:description" content="OG description wins">
</head><body><meta property="og:title" content="late"></body></html>`

// TestPreviewAppearsAtTheFirstBucket: never before created_at + PreviewDelay,
// tracking parameters stripped, the title cleaned, <url> and images skipped.
func TestPreviewAppearsAtTheFirstBucket(t *testing.T) {
	e := newEnv(t)
	e.web.set("https://news.example/a?id=1", page{html: article})
	created := e.clk.Now()
	p := e.post("alice", "see https://News.Example/a?utm_source=x&id=1&fbclid=y#top and <https://quiet.example/> "+
		"and ![cat](https://img.example/cat.png)")
	if got := e.shown("bob", p); len(got) != 0 {
		t.Fatalf("before the job: %+v", got)
	}
	e.drain()
	e.clk.Set(created.Add(30*time.Second - time.Millisecond))
	if got := e.shown("bob", p); len(got) != 0 {
		t.Fatalf("before the bucket: %+v", got)
	}
	e.clk.Set(created.Add(30 * time.Second))
	got := e.shown("bob", p)
	if len(got) != 1 {
		t.Fatalf("at the bucket: %+v", got)
	}
	pv := got[0]
	if pv.URL != "https://news.example/a?id=1" || pv.Host != "news.example" || pv.HostUnicode != "news.example" || pv.Lookalike {
		t.Fatalf("%+v", pv)
	}
	if pv.Title != "The & real title evil" || pv.Description != "OG description wins" {
		t.Fatalf("title %q description %q", pv.Title, pv.Description)
	}
	if c := e.web.called(); len(c) != 1 || c[0] != "https://news.example/a?id=1" {
		t.Fatalf("fetched %v", c)
	}
	// The author and other viewers see the same copy; carol cannot see the post.
	if got := e.shown("alice", p); len(got) != 1 || got[0] != pv {
		t.Fatalf("alice: %+v", got)
	}
	if _, err := e.a.Posts.Get(e.as("carol"), e.d.Refs.Post(e.ids["carol"], p)); !errors.Is(err, gate.ErrNotFound) {
		t.Fatal(err)
	}
}

// TestSlowFetchBuckets: a fetch slower than one bucket shows at exactly bucket
// 2; a fetch slower than two buckets never shows.
func TestSlowFetchBuckets(t *testing.T) {
	e := newEnv(t)
	e.web.set("https://slow.example/", page{html: "<title>Slow</title>", delay: 31 * time.Second})
	created := e.clk.Now()
	slow := e.post("alice", "https://slow.example/")
	e.drain()
	for _, at := range []time.Duration{31 * time.Second, 59 * time.Second, 60*time.Second - time.Millisecond} {
		e.clk.Set(created.Add(at))
		if got := e.shown("bob", slow); len(got) != 0 {
			t.Fatalf("at +%v: %+v", at, got)
		}
	}
	e.clk.Set(created.Add(60 * time.Second))
	if got := e.shown("bob", slow); len(got) != 1 || got[0].Title != "Slow" {
		t.Fatalf("at bucket 2: %+v", got)
	}

	e.web.set("https://slower.example/", page{html: "<title>Slower</title>", delay: 61 * time.Second})
	created = e.clk.Now()
	slower := e.post("alice", "https://slower.example/")
	e.drain()
	for _, at := range []time.Duration{61 * time.Second, 2 * time.Minute, 24 * time.Hour} {
		e.clk.Set(created.Add(at))
		if got := e.shown("bob", slower); len(got) != 0 {
			t.Fatalf("slower at +%v: %+v", at, got)
		}
	}
	if r := e.rows(slower); len(r) != 1 || !strings.HasSuffix(r[0], " 2") {
		t.Fatalf("rows %v", r)
	}
}

// TestFastFetchStillWaits: a cache hit or an instant fetch never shows early,
// and a late job run (past bucket 2) fails without fetching.
func TestFastFetchStillWaitsAndLateRunFails(t *testing.T) {
	e := newEnv(t)
	e.web.set("https://fast.example/", page{html: "<title>Fast</title>"})
	created := e.clk.Now()
	p := e.post("alice", "https://fast.example/")
	e.drain()
	e.clk.Set(created.Add(29 * time.Second))
	if got := e.shown("bob", p); len(got) != 0 {
		t.Fatal(got)
	}
	created = e.clk.Now()
	late := e.post("alice", "https://late.example/")
	e.clk.Set(created.Add(61 * time.Second))
	e.drain()
	if c := e.web.called(); len(c) != 1 {
		t.Fatalf("fetched %v", c)
	}
	if r := e.rows(late); len(r) != 1 || !strings.HasSuffix(r[0], " 2") {
		t.Fatalf("rows %v", r)
	}
}

// TestAtMostThreeDistinctLinks: canonical duplicates count once; links past
// the third get no preview.
func TestAtMostThreeDistinctLinks(t *testing.T) {
	e := newEnv(t)
	for _, u := range []string{"https://a.example/", "https://b.example/", "https://c.example/", "https://d.example/"} {
		e.web.set(u, page{html: "<title>" + u + "</title>"})
	}
	p := e.post("alice", "[one](https://a.example) https://A.example/?utm_campaign=z https://b.example/#x "+
		"`https://code.example/` https://c.example/ https://d.example/")
	e.drain()
	want := []string{"0 https://a.example/ 1", "1 https://b.example/ 1", "2 https://c.example/ 1"}
	if r := e.rows(p); strings.Join(r, "|") != strings.Join(want, "|") {
		t.Fatalf("rows %v", r)
	}
	e.clk.Advance(time.Minute)
	if got := e.shown("bob", p); len(got) != 3 || got[2].URL != "https://c.example/" {
		t.Fatalf("%+v", got)
	}
}

// TestCacheAcrossPosts: a second post with the same link uses the cache (still
// its own row and its own bucket); failures are cached too; the cache expires.
func TestCacheAcrossPosts(t *testing.T) {
	e := newEnv(t)
	e.web.set("https://viral.example/", page{html: "<title>Viral</title>"})
	p1 := e.post("alice", "https://viral.example/")
	e.drain()
	e.clk.Advance(10 * time.Second)
	created := e.clk.Now()
	p2 := e.post("bob", "again https://viral.example/?utm_source=share")
	e.drain()
	if c := e.web.called(); len(c) != 1 {
		t.Fatalf("fetched %v", c)
	}
	e.clk.Set(created.Add(29 * time.Second))
	if got := e.shown("alice", p2); len(got) != 0 {
		t.Fatalf("p2 early: %+v", got)
	}
	if got := e.shown("alice", p1); len(got) != 1 {
		t.Fatalf("p1: %+v", got)
	}
	e.clk.Set(created.Add(30 * time.Second))
	if got := e.shown("alice", p2); len(got) != 1 || got[0].Title != "Viral" {
		t.Fatalf("p2: %+v", got)
	}
	// A failure is cached: the broken link is fetched once per TTL.
	_ = e.post("alice", "https://broken.example/")
	_ = e.post("bob", "https://broken.example/")
	e.drain()
	if c := e.web.called(); len(c) != 2 || c[1] != "https://broken.example/" {
		t.Fatalf("fetched %v", c)
	}
	e.clk.Advance(previews.CacheTTL + time.Second)
	_ = e.post("alice", "https://viral.example/")
	e.drain()
	if c := e.web.called(); len(c) != 3 || c[2] != "https://viral.example/" {
		t.Fatalf("after expiry %v", c)
	}
}

// TestPerHostLimiter: three fetches per registrable domain at once, then one
// per two seconds; a refusal is not cached.
func TestPerHostLimiter(t *testing.T) {
	e := newEnv(t)
	for i := range 5 {
		e.web.set(fmt.Sprintf("https://s%d.host.example/", i), page{html: "<title>ok</title>"})
	}
	p1 := e.post("alice", "https://s0.host.example/ https://s1.host.example/ https://s2.host.example/")
	p2 := e.post("alice", "https://s3.host.example/")
	e.drain()
	if c := e.web.called(); len(c) != 3 {
		t.Fatalf("fetched %v", c)
	}
	if r := e.rows(p2); len(r) != 1 || !strings.HasSuffix(r[0], " 2") {
		t.Fatalf("limited row %v", r)
	}
	if r := e.rows(p1); len(r) != 3 || !strings.HasSuffix(r[2], " 1") {
		t.Fatalf("rows %v", r)
	}
	e.clk.Advance(2 * time.Second)
	p3 := e.post("alice", "https://s3.host.example/ https://s4.host.example/")
	e.drain()
	if c := e.web.called(); len(c) != 4 {
		t.Fatalf("fetched %v", c)
	}
	if r := e.rows(p3); len(r) != 2 || !strings.HasSuffix(r[0], " 1") || !strings.HasSuffix(r[1], " 2") {
		t.Fatalf("rows %v", r)
	}
	// Reset (test hooks) forgets the limiter.
	e.a.Previews.Reset()
	_ = e.post("alice", "https://s4.host.example/")
	e.drain()
	if c := e.web.called(); len(c) != 5 {
		t.Fatalf("after reset %v", c)
	}
}

// TestFailuresNeverShowAndNeverRetry: errors, pages without a title and
// non-HTML answers leave a failed row; the job itself succeeds once.
func TestFailuresNeverShowAndNeverRetry(t *testing.T) {
	e := newEnv(t)
	e.web.set("https://type.example/", page{err: safefetch.ErrType})
	e.web.set("https://blocked.example/", page{err: safefetch.ErrBlocked})
	e.web.set("https://empty.example/", page{html: "<html><body>no title</body></html>"})
	p := e.post("alice", "https://type.example/ https://blocked.example/ https://empty.example/")
	e.drain()
	e.clk.Advance(time.Hour)
	if got := e.shown("bob", p); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	var attempts, done int
	if err := e.d.DB.Q().QueryRow(e.ctx, `SELECT max(attempts), count(*) FILTER (WHERE done_at IS NOT NULL)
		FROM jobs WHERE kind = 'preview.fetch'`).Scan(&attempts, &done); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || done != 1 {
		t.Fatalf("attempts %d done %d", attempts, done)
	}
}

// TestOptOutDeleteAndEdit: no_preview posts get no job; deleting drops the
// rows; an edit rebuilds the previews on the edit's own buckets.
func TestOptOutDeleteAndEdit(t *testing.T) {
	e := newEnv(t)
	e.web.set("https://one.example/", page{html: "<title>One</title>"})
	e.web.set("https://two.example/", page{html: "<title>Two</title>"})
	off := e.create("alice", posts.Draft{Body: "https://one.example/", Lang: "en", NoPreview: true})
	e.drain()
	if r := e.rows(off); len(r) != 0 || len(e.web.called()) != 0 {
		t.Fatalf("opt-out: %v %v", r, e.web.called())
	}

	p := e.post("alice", "https://one.example/")
	e.drain()
	e.clk.Advance(time.Minute)
	if got := e.shown("bob", p); len(got) != 1 || got[0].Title != "One" {
		t.Fatalf("%+v", got)
	}
	edited := e.clk.Now()
	if _, err := e.a.Posts.Edit(e.as("alice"), e.d.Refs.Post(e.ids["alice"], p), "now https://two.example/", ""); err != nil {
		t.Fatal(err)
	}
	e.drain()
	e.clk.Set(edited.Add(29 * time.Second))
	if got := e.shown("bob", p); len(got) != 0 {
		t.Fatalf("after edit, early: %+v", got)
	}
	e.clk.Set(edited.Add(30 * time.Second))
	if got := e.shown("bob", p); len(got) != 1 || got[0].Title != "Two" {
		t.Fatalf("after edit: %+v", got)
	}

	if err := e.a.Posts.Delete(e.as("alice"), e.d.Refs.Post(e.ids["alice"], p)); err != nil {
		t.Fatal(err)
	}
	if r := e.rows(p); len(r) != 0 {
		t.Fatalf("after delete %v", r)
	}
	// A job for a deleted post does nothing.
	if err := e.a.Previews.Build(e.ctx, p); err != nil || len(e.rows(p)) != 0 {
		t.Fatal(err, e.rows(p))
	}
}

// TestRealFetcherUnderTestHooks: the app's default fetcher is safefetch; with
// test hooks on it may reach a loopback page (e2e), with them off it may not.
func TestRealFetcherUnderTestHooks(t *testing.T) {
	var hits atomic.Int32
	var ua atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		ua.Store(r.Header.Get("User-Agent"))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<title>Local page</title>"))
	}))
	defer srv.Close()

	on := wiretest.NewApp(t)
	wo := wiretest.World(t, on)
	alice := wo.User("alice")
	ctx := core.WithViewer(context.Background(), core.Viewer{ID: alice, Kind: core.SessUser, AuthAt: on.Deps().Clock.Now()})
	pv, err := on.Posts.Create(ctx, posts.Draft{Body: "local " + srv.URL + "/page", Lang: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if err := on.DrainJobs(context.Background()); err != nil {
		t.Fatal(err)
	}
	on.Deps().Clock.(*clock.Fake).Advance(time.Minute)
	got, err := on.Posts.Get(ctx, pv.Ref)
	if err != nil || len(got.Previews) != 1 || got.Previews[0].Title != "Local page" || got.Previews[0].Host != "127.0.0.1" {
		t.Fatalf("%+v %v", got.Previews, err)
	}
	if hits.Load() != 1 || ua.Load() != safefetch.UserAgent {
		t.Fatal(hits.Load(), ua.Load())
	}

	off := wiretest.NewApp(t, wiretest.WithEnv("PCC_TEST_HOOKS", "0"))
	wf := wiretest.World(t, off)
	bob := wf.User("bob")
	ctx = core.WithViewer(context.Background(), core.Viewer{ID: bob, Kind: core.SessUser, AuthAt: off.Deps().Clock.Now()})
	pv, err = off.Posts.Create(ctx, posts.Draft{Body: srv.URL + "/page http://169.254.169.254/latest/ http://localhost/", Lang: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if err := off.DrainJobs(context.Background()); err != nil {
		t.Fatal(err)
	}
	off.Deps().Clock.(*clock.Fake).Advance(time.Minute)
	if got, _ := off.Posts.Get(ctx, pv.Ref); len(got.Previews) != 0 {
		t.Fatalf("%+v", got.Previews)
	}
	if hits.Load() != 1 {
		t.Fatal("the production fetcher reached a loopback server")
	}
}

func TestTargets(t *testing.T) {
	got := previews.Targets("x <https://no.example/> ![i](https://img.example/i.png) [a](javascript:alert(1)) " +
		"http://user@host.example/ https://ok.example/?utm_medium=a&k=v https://OK.example/?k=v ftp://f.example/")
	if len(got) != 1 || got[0].String() != "https://ok.example/?k=v" {
		t.Fatalf("%v", got)
	}
}
