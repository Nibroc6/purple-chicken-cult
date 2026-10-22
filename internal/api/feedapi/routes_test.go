package feedapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/api/apitest"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/apix"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/feedapi"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire/wiretest"
)

type fixture struct {
	h *apitest.Harness
	w *wiretest.TestWorld
}

// newFixture: alice – bob – carol – dave, zoe (young) friend of carol, xavier
// in another component. bob posts 21 times, carol once, dave once, zoe twice,
// xavier once.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	h := apitest.Service(t, "feed", feedapi.Register)
	w := wiretest.World(t, h.App)
	for _, u := range []string{"alice", "bob", "carol", "dave", "zoe", "xavier"} {
		w.User(u)
	}
	w.Friends("alice", "bob")
	w.Friends("bob", "carol")
	w.Friends("carol", "dave")
	w.Friends("carol", "zoe")
	ctx := context.Background()
	if err := h.App.Hooks().AdvanceClock(90 * 24 * time.Hour); err != nil {
		t.Fatal(err)
	}
	now := h.App.Deps().Clock.Now()
	if _, err := h.App.Deps().DB.Q().Exec(ctx, "UPDATE users SET created_at = $1 WHERE handle = 'zoe'", now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := h.App.Users.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 21; i++ {
		w.Post("bob", fmt.Sprintf("b%02d", i), fmt.Sprintf("bob %d", i))
	}
	w.Post("carol", "c1", "hello from carol")
	w.Post("dave", "d1", "dave far")
	w.Post("zoe", "z1", "zoe one")
	w.Post("zoe", "z2", "zoe two")
	w.Post("xavier", "x1", "far away")
	return &fixture{h: h, w: w}
}

// ref is viewer's ref for the post labelled label.
func (f *fixture) ref(viewer, label string) string {
	return string(f.h.App.Deps().Refs.Post(f.w.ID(viewer), f.w.PostID(label)))
}

func (f *fixture) do(t *testing.T, method, path string, want int) []byte {
	t.Helper()
	rec := f.h.Do(method, path, nil)
	if rec.Code != want {
		t.Fatalf("%s %s = %d %s, want %d", method, path, rec.Code, rec.Body, want)
	}
	return rec.Body.Bytes()
}

func (f *fixture) rateCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.h.App.Deps().DB.Q().QueryRow(context.Background(), "SELECT coalesce(sum(count), 0) FROM rate_counters").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func decode[T any](t *testing.T, b []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("%s: %v", b, err)
	}
	return v
}

type item struct {
	Type string `json:"type"`
	Tier int    `json:"tier"`
	Post *struct {
		Ref  string `json:"ref"`
		Body string `json:"body"`
	} `json:"post"`
	Collapsed *struct {
		Count int    `json:"count"`
		Token string `json:"token"`
	} `json:"collapsed"`
}

type page struct {
	Session string `json:"session"`
	Preset  string `json:"preset"`
	Items   []item `json:"items"`
	Next    string `json:"next"`
}

const notFound = `{"error":"not_found"}` + "\n"

func q(path string, kv ...string) string {
	v := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		v.Set(kv[i], kv[i+1])
	}
	return path + "?" + v.Encode()
}

func TestAnonymousGetsUniformNotFound(t *testing.T) {
	f := newFixture(t)
	for _, path := range []string{
		"/api/feed", q("/api/feed/expand", "token", "x"), q("/api/feed/new", "session", "x"),
		"/api/posts/" + f.ref("alice", "c1") + "/why",
	} {
		if rec := f.h.Do(http.MethodGet, path, nil); rec.Code != http.StatusNotFound || rec.Body.String() != notFound {
			t.Errorf("GET %s = %d %q", path, rec.Code, rec.Body)
		}
	}
}

func TestFeedPages(t *testing.T) {
	f := newFixture(t)
	f.h.Login("alice")
	p1 := decode[page](t, f.do(t, http.MethodGet, q("/api/feed", "preset", "latest"), http.StatusOK))
	if p1.Preset != "latest" || p1.Next == "" || p1.Items[0].Type != "tier_start" || p1.Items[0].Tier != 1 ||
		p1.Items[1].Post == nil || !strings.HasPrefix(p1.Items[1].Post.Body, "bob ") || len(p1.Items) != 21 {
		t.Fatalf("page 1 %+v", p1)
	}
	p2 := decode[page](t, f.do(t, http.MethodGet, q("/api/feed", "cursor", p1.Next), http.StatusOK))
	var types []string
	for _, it := range p2.Items {
		types = append(types, it.Type)
	}
	if got := strings.Join(types, " "); got != "post tier_end tier_start post tier_end tier_start post collapsed tier_end caught_up" || p2.Next != "" {
		t.Fatalf("page 2 %s next %q", got, p2.Next)
	}
	var token string
	for _, it := range p2.Items {
		if it.Type == "collapsed" {
			token = it.Collapsed.Token
			if it.Collapsed.Count != 2 || it.Tier != 3 {
				t.Fatalf("collapsed %+v", it)
			}
		}
	}
	ex := decode[struct {
		Items []item `json:"items"`
	}](t, f.do(t, http.MethodGet, q("/api/feed/expand", "token", token), http.StatusOK))
	if len(ex.Items) != 2 || ex.Items[0].Post.Body != "zoe two" {
		t.Fatalf("expand %+v", ex)
	}
	n := decode[feedapi.NewOut](t, f.do(t, http.MethodGet, q("/api/feed/new", "session", p1.Session), http.StatusOK))
	if n.Count != 0 || n.Tier != 0 {
		t.Fatalf("new %+v", n)
	}

	// Problems about the caller's own input.
	for _, p := range []string{"nope", "Top"} {
		rec := f.h.Do(http.MethodGet, q("/api/feed", "preset", p), nil)
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `"preset_unavailable"`) {
			t.Fatalf("preset %s: %d %s", p, rec.Code, rec.Body)
		}
	}
	// Another viewer's cursor, session or token is the uniform 404.
	f.h.Login("bob")
	for _, path := range []string{
		q("/api/feed", "cursor", p1.Next), q("/api/feed/new", "session", p1.Session), q("/api/feed/expand", "token", token),
		q("/api/feed", "cursor", "garbage"),
	} {
		if rec := f.h.Do(http.MethodGet, path, nil); rec.Code != http.StatusNotFound || rec.Body.String() != notFound {
			t.Fatalf("GET %s = %d %q", path, rec.Code, rec.Body)
		}
	}
	// The caller's own session after 30 unused minutes: 409, start over.
	f.h.Login("alice")
	if err := f.h.App.Hooks().AdvanceClock(31 * time.Minute); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{q("/api/feed", "cursor", p1.Next), q("/api/feed/new", "session", p1.Session)} {
		rec := f.h.Do(http.MethodGet, path, nil)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"feed_session_expired"`) {
			t.Fatalf("GET %s = %d %s", path, rec.Code, rec.Body)
		}
	}
}

func TestWhy(t *testing.T) {
	f := newFixture(t)
	f.h.Login("alice")
	why := decode[struct {
		Tier    int      `json:"tier"`
		Preset  string   `json:"preset"`
		Reasons []string `json:"reasons"`
		Via     struct {
			Dist     int `json:"dist"`
			FirstHop *struct {
				Handle string `json:"handle"`
			} `json:"first_hop"`
		} `json:"via"`
	}](t, f.do(t, http.MethodGet, "/api/posts/"+f.ref("alice", "c1")+"/why", http.StatusOK))
	if why.Tier != 2 || why.Via.Dist != 2 || why.Via.FirstHop == nil || why.Via.FirstHop.Handle != "bob" || why.Preset != "balanced" {
		t.Fatalf("why %+v", why)
	}
}

// Acceptance (charging uniformity): why on an invisible, foreign or garbage ref
// answers the uniform 404 and moves the rate counters exactly like a valid one.
func TestInvisibleRefsAreUniform(t *testing.T) {
	f := newFixture(t)
	f.h.Login("alice")
	call := func(ref string) (int, string, int) {
		before := f.rateCount(t)
		rec := f.h.Do(http.MethodGet, "/api/posts/"+ref+"/why", nil)
		return rec.Code, rec.Body.String(), f.rateCount(t) - before
	}
	okCode, _, okDelta := call(f.ref("alice", "c1"))
	if okCode != http.StatusOK {
		t.Fatalf("valid: %d", okCode)
	}
	for name, r := range map[string]string{
		"other component": f.ref("alice", "x1"),
		"another viewer":  f.ref("bob", "c1"),
		"garbage":         "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
	} {
		code, body, delta := call(r)
		if code != http.StatusNotFound || body != notFound || delta != okDelta {
			t.Errorf("%s: %d %q delta %d (valid: %d)", name, code, body, delta, okDelta)
		}
	}
}

func TestRouteClasses(t *testing.T) {
	newFixture(t)
	want := map[string]string{
		"GET /feed": "r:feed_page", "GET /feed/expand": "r:feed_page", "GET /feed/new": "r:feed_page",
		"GET /posts/{ref}/why": "r:post_read",
	}
	found := 0
	for _, ri := range apix.Routes() {
		k := ri.Method + " " + strings.TrimPrefix(ri.Pattern, "/api")
		if w, ok := want[k]; ok {
			found++
			if got := string(ri.Class.Kind) + ":" + ri.Class.Name; got != w {
				t.Errorf("%s: %s, want %s", k, got, w)
			}
		}
	}
	if found != len(want) {
		t.Fatalf("found %d of %d routes", found, len(want))
	}
}
