package engageapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Nibroc6/purple-chicken-cult/internal/api/apitest"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/apix"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/engageapi"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire/wiretest"
)

type fixture struct {
	h *apitest.Harness
	w *wiretest.TestWorld
}

// newFixture: alice – bob – carol, xavier in another component; carol and
// xavier each posted once.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	h := apitest.Service(t, "engage", engageapi.Register)
	w := wiretest.World(t, h.App)
	for _, u := range []string{"alice", "bob", "carol", "xavier"} {
		w.User(u)
	}
	w.Friends("alice", "bob")
	w.Friends("bob", "carol")
	w.Post("carol", "c1", "hello from carol")
	w.Post("xavier", "x1", "far away")
	if err := h.App.Engage.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	return &fixture{h: h, w: w}
}

// ref is viewer's ref for the post labelled label.
func (f *fixture) ref(viewer, label string) string {
	return string(f.h.App.Deps().Refs.Post(f.w.ID(viewer), f.w.PostID(label)))
}

func (f *fixture) do(t *testing.T, method, path string, body any, want int) []byte {
	t.Helper()
	rec := f.h.Do(method, path, body)
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

func (f *fixture) rows(t *testing.T, table string) int {
	t.Helper()
	var n int
	if err := f.h.App.Deps().DB.Q().QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
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

type likes struct {
	InNetwork int `json:"in_network"`
	Names     []struct {
		Ref    string `json:"ref"`
		Handle string `json:"handle"`
	} `json:"names"`
	Others int  `json:"others"`
	ByMe   bool `json:"by_me"`
}

type page struct {
	Items []struct {
		Ref  string `json:"ref"`
		Body string `json:"body"`
	} `json:"items"`
	Next string `json:"next"`
}

const notFound = `{"error":"not_found"}` + "\n"

func TestAnonymousGetsUniformNotFound(t *testing.T) {
	f := newFixture(t)
	r := f.ref("alice", "c1")
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPut, "/api/posts/" + r + "/like", nil},
		{http.MethodDelete, "/api/posts/" + r + "/like", nil},
		{http.MethodGet, "/api/posts/" + r + "/likes", nil},
		{http.MethodPost, "/api/seen", map[string]any{"posts": []string{r}}},
		{http.MethodGet, "/api/seen/recent", nil},
		{http.MethodPost, "/api/posts/" + r + "/hide", nil},
		{http.MethodPost, "/api/posts/" + r + "/show-less", nil},
		{http.MethodPut, "/api/posts/" + r + "/save", nil},
		{http.MethodDelete, "/api/posts/" + r + "/save", nil},
		{http.MethodGet, "/api/saves", nil},
	} {
		if rec := f.h.Do(c.method, c.path, c.body); rec.Code != http.StatusNotFound || rec.Body.String() != notFound {
			t.Errorf("%s %s = %d %q", c.method, c.path, rec.Code, rec.Body)
		}
	}
}

func TestLikeLifecycle(t *testing.T) {
	f := newFixture(t)
	f.h.Login("bob")
	out := decode[struct {
		Likes likes `json:"likes"`
	}](t, f.do(t, http.MethodPut, "/api/posts/"+f.ref("bob", "c1")+"/like", nil, http.StatusOK))
	if !out.Likes.ByMe || out.Likes.InNetwork != 1 {
		t.Fatalf("after like %+v", out.Likes)
	}
	f.do(t, http.MethodPut, "/api/posts/"+f.ref("bob", "c1")+"/like", nil, http.StatusOK)

	f.h.Login("alice")
	ls := decode[likes](t, f.do(t, http.MethodGet, "/api/posts/"+f.ref("alice", "c1")+"/likes", nil, http.StatusOK))
	if ls.ByMe || ls.InNetwork != 1 || len(ls.Names) != 1 || ls.Names[0].Handle != "bob" || ls.Others != 0 {
		t.Fatalf("alice sees %+v", ls)
	}
	out = decode[struct {
		Likes likes `json:"likes"`
	}](t, f.do(t, http.MethodPut, "/api/posts/"+f.ref("alice", "c1")+"/like", nil, http.StatusOK))
	if !out.Likes.ByMe || out.Likes.InNetwork != 2 {
		t.Fatalf("after alice's like %+v", out.Likes)
	}
	out = decode[struct {
		Likes likes `json:"likes"`
	}](t, f.do(t, http.MethodDelete, "/api/posts/"+f.ref("alice", "c1")+"/like", nil, http.StatusOK))
	if out.Likes.ByMe || out.Likes.InNetwork != 1 {
		t.Fatalf("after unlike %+v", out.Likes)
	}
}

// Acceptance: POST /seen with garbage refs answers 204 and writes nothing.
func TestSeenAndRecentlySeen(t *testing.T) {
	f := newFixture(t)
	f.h.Login("alice")
	for _, body := range []any{
		map[string]any{"posts": []string{}},
		map[string]any{"posts": []string{"garbage", "", "%%%", strings.Repeat("A", 200)}},
		map[string]any{"posts": []string{f.ref("bob", "c1"), f.ref("alice", "x1")}}, // foreign ref, invisible post
	} {
		f.do(t, http.MethodPost, "/api/seen", body, http.StatusNoContent)
	}
	if n := f.rows(t, "seen"); n != 0 {
		t.Fatalf("garbage wrote %d rows", n)
	}
	p := decode[page](t, f.do(t, http.MethodGet, "/api/seen/recent", nil, http.StatusOK))
	if len(p.Items) != 0 || p.Next != "" {
		t.Fatalf("recent %+v", p)
	}
	// More than 50 refs: still 204 (the rest are ignored).
	many := make([]string, 60)
	for i := range many {
		many[i] = f.ref("alice", "c1")
	}
	f.do(t, http.MethodPost, "/api/seen", map[string]any{"posts": many}, http.StatusNoContent)
	p = decode[page](t, f.do(t, http.MethodGet, "/api/seen/recent", nil, http.StatusOK))
	if len(p.Items) != 1 || p.Items[0].Ref != f.ref("alice", "c1") || p.Items[0].Body != "hello from carol" {
		t.Fatalf("recent %+v", p)
	}
	f.do(t, http.MethodGet, "/api/seen/recent?cursor=nope", nil, http.StatusNotFound)
	f.do(t, http.MethodPost, "/api/seen", map[string]any{"posts": []string{}, "extra": 1}, http.StatusBadRequest)
}

func TestHideAndShowLess(t *testing.T) {
	f := newFixture(t)
	f.h.Login("alice")
	f.do(t, http.MethodPost, "/api/posts/"+f.ref("alice", "c1")+"/hide", nil, http.StatusNoContent)
	f.do(t, http.MethodPost, "/api/posts/"+f.ref("alice", "c1")+"/show-less", nil, http.StatusNoContent)
	if f.rows(t, "hides") != 1 {
		t.Fatal("hide not stored")
	}
}

// Saves are private: the saver's list and saved_by_me; nothing anyone else
// reads changes.
func TestSaveLifecycle(t *testing.T) {
	f := newFixture(t)
	f.h.Login("alice")
	before := string(f.do(t, http.MethodGet, "/api/posts/"+f.ref("alice", "c1")+"/likes", nil, http.StatusOK))
	f.h.Login("bob")
	f.do(t, http.MethodPut, "/api/posts/"+f.ref("bob", "c1")+"/save", nil, http.StatusNoContent)
	f.do(t, http.MethodPut, "/api/posts/"+f.ref("bob", "c1")+"/save", nil, http.StatusNoContent)
	saved := decode[struct {
		Items []struct {
			Ref       string `json:"ref"`
			SavedByMe bool   `json:"saved_by_me"`
		} `json:"items"`
		Next string `json:"next"`
	}](t, f.do(t, http.MethodGet, "/api/saves", nil, http.StatusOK))
	if len(saved.Items) != 1 || saved.Items[0].Ref != f.ref("bob", "c1") || !saved.Items[0].SavedByMe || saved.Next != "" {
		t.Fatalf("saves %+v", saved)
	}
	f.do(t, http.MethodGet, "/api/saves?cursor=nope", nil, http.StatusNotFound)

	f.h.Login("alice")
	if after := string(f.do(t, http.MethodGet, "/api/posts/"+f.ref("alice", "c1")+"/likes", nil, http.StatusOK)); after != before {
		t.Fatalf("a save shows in the like summary: %s, was %s", after, before)
	}
	if p := decode[page](t, f.do(t, http.MethodGet, "/api/saves", nil, http.StatusOK)); len(p.Items) != 0 {
		t.Fatalf("alice sees bob's saves: %+v", p)
	}

	f.h.Login("bob")
	f.do(t, http.MethodDelete, "/api/posts/"+f.ref("bob", "c1")+"/save", nil, http.StatusNoContent)
	if p := decode[page](t, f.do(t, http.MethodGet, "/api/saves", nil, http.StatusOK)); len(p.Items) != 0 {
		t.Fatalf("after unsave %+v", p)
	}
}

// Acceptance (charging uniformity): a like, hide or show-less on an invisible,
// foreign or garbage ref answers the uniform 404 and moves the rate counters
// exactly like a valid one.
func TestInvisibleRefsAreUniform(t *testing.T) {
	f := newFixture(t)
	f.h.Login("alice")
	for _, c := range []struct{ method, suffix string }{
		{http.MethodPut, "/like"}, {http.MethodDelete, "/like"}, {http.MethodGet, "/likes"},
		{http.MethodPost, "/hide"}, {http.MethodPost, "/show-less"},
		{http.MethodPut, "/save"}, {http.MethodDelete, "/save"},
	} {
		call := func(ref string) (int, string, int) {
			before := f.rateCount(t)
			rec := f.h.Do(c.method, "/api/posts/"+ref+c.suffix, nil)
			return rec.Code, rec.Body.String(), f.rateCount(t) - before
		}
		okCode, _, okDelta := call(f.ref("alice", "c1"))
		if okCode != http.StatusOK && okCode != http.StatusNoContent {
			t.Fatalf("%s %s valid: %d", c.method, c.suffix, okCode)
		}
		for name, r := range map[string]string{
			"other component": f.ref("alice", "x1"),
			"another viewer":  f.ref("bob", "c1"),
			"garbage":         "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		} {
			code, body, delta := call(r)
			if code != http.StatusNotFound || body != notFound || delta != okDelta {
				t.Errorf("%s %s %s: %d %q delta %d (valid: %d)", c.method, c.suffix, name, code, body, delta, okDelta)
			}
		}
	}
}

func TestRouteClasses(t *testing.T) {
	newFixture(t)
	want := map[string]string{
		"PUT /posts/{ref}/like": "w:like", "DELETE /posts/{ref}/like": "w:like", "GET /posts/{ref}/likes": "r:post_read",
		"POST /seen": "-:free", "GET /seen/recent": "r:feed_page", "POST /posts/{ref}/hide": "w:social",
		"POST /posts/{ref}/show-less": "w:social",
		"PUT /posts/{ref}/save":       "w:save", "DELETE /posts/{ref}/save": "w:save", "GET /saves": "r:feed_page",
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
