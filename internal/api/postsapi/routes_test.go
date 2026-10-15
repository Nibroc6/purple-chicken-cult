package postsapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Nibroc6/purple-chicken-cult/internal/api/apitest"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/apix"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/postsapi"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire/wiretest"
)

type fixture struct {
	h *apitest.Harness
	w *wiretest.TestWorld
}

// newFixture: alice – bob, xavier in another component, each with a post.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	h := apitest.Service(t, "posts", postsapi.Register)
	w := wiretest.World(t, h.App)
	for _, u := range []string{"alice", "bob", "xavier"} {
		w.User(u)
	}
	w.Friends("alice", "bob")
	w.Post("bob", "bob1", "hello from bob")
	w.Post("xavier", "x1", "far away")
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

func decode[T any](t *testing.T, b []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("%s: %v", b, err)
	}
	return v
}

type post struct {
	Ref      string `json:"ref"`
	Body     string `json:"body"`
	Lang     string `json:"lang"`
	Tier     int    `json:"tier"`
	Mine     bool   `json:"mine"`
	Audience string `json:"audience"`
	Context  []struct {
		Ref          string `json:"ref"`
		AuthorHandle string `json:"author_handle"`
	} `json:"context"`
	Thread *struct {
		Index int `json:"index"`
		Total int `json:"total"`
	} `json:"thread"`
	EditedAt   *string `json:"edited_at"`
	ReplyCount int     `json:"reply_count"`
}

type errBody struct {
	Error string `json:"error"`
	Field string `json:"field"`
}

func gateRef(s string) gate.Ref { return gate.Ref(s) }

const notFound = `{"error":"not_found"}` + "\n"

func TestAnonymousGetsUniformNotFound(t *testing.T) {
	f := newFixture(t)
	r := f.ref("alice", "bob1")
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, "/api/posts", map[string]any{"body": "x", "lang": "en"}},
		{http.MethodPost, "/api/posts/thread", map[string]any{"parts": []any{map[string]any{"body": "x", "lang": "en"}}}},
		{http.MethodGet, "/api/posts/" + r, nil},
		{http.MethodPatch, "/api/posts/" + r, map[string]any{"body": "x"}},
		{http.MethodDelete, "/api/posts/" + r, nil},
		{http.MethodPut, "/api/posts/" + r + "/ref-policy", map[string]any{"ref_policy": "nobody"}},
		{http.MethodPost, "/api/posts/" + r + "/detach", map[string]any{"child": r}},
		{http.MethodPost, "/api/posts/" + r + "/detach-all", nil},
	} {
		if rec := f.h.Do(c.method, c.path, c.body); rec.Code != http.StatusNotFound || rec.Body.String() != notFound {
			t.Errorf("%s %s = %d %q", c.method, c.path, rec.Code, rec.Body)
		}
	}
}

func TestPostLifecycle(t *testing.T) {
	f := newFixture(t)
	f.h.Login("alice")
	p := decode[post](t, f.do(t, http.MethodPost, "/api/posts", map[string]any{
		"body": "replying", "lang": "en", "audience": "d2", "parents": []any{map[string]any{"ref": f.ref("alice", "bob1"), "kind": "ref"}},
	}, http.StatusCreated))
	if !p.Mine || p.Audience != "d2" || len(p.Context) != 1 || p.Context[0].AuthorHandle != "bob" {
		t.Fatalf("created %+v", p)
	}
	got := decode[post](t, f.do(t, http.MethodGet, "/api/posts/"+p.Ref, nil, http.StatusOK))
	if got.Body != "replying" {
		t.Fatalf("get %+v", got)
	}
	ed := decode[post](t, f.do(t, http.MethodPatch, "/api/posts/"+p.Ref, map[string]any{"body": "replying, edited"}, http.StatusOK))
	if ed.Ref != p.Ref || ed.Body != "replying, edited" || ed.EditedAt == nil || ed.Lang != "en" {
		t.Fatalf("edited %+v", ed)
	}

	th := decode[struct {
		Items []post `json:"items"`
	}](t, f.do(t, http.MethodPost, "/api/posts/thread", map[string]any{
		"parts": []any{map[string]any{"body": "one", "lang": "en"}, map[string]any{"body": "two", "lang": "en"}},
	}, http.StatusCreated))
	if len(th.Items) != 2 || th.Items[1].Thread == nil || th.Items[1].Thread.Total != 2 {
		t.Fatalf("thread %+v", th)
	}

	// bob replies to alice's thread head; alice turns it slippery and is offered detach-all.
	f.h.Login("bob")
	head := decode[post](t, f.do(t, http.MethodGet, "/api/posts/"+bobRef(t, f, th.Items[0].Ref), nil, http.StatusOK))
	reply := decode[post](t, f.do(t, http.MethodPost, "/api/posts", map[string]any{
		"body": "nice", "lang": "en", "parents": []any{map[string]any{"ref": head.Ref, "kind": "ref"}},
	}, http.StatusCreated))
	f.h.Login("alice")
	b := decode[struct {
		Offer bool `json:"offer_detach_all"`
	}](t, f.do(t, http.MethodPut, "/api/posts/"+th.Items[0].Ref+"/ref-policy", map[string]any{"ref_policy": "nobody"}, http.StatusOK))
	if !b.Offer {
		t.Fatal("no detach-all offer")
	}
	headView := decode[post](t, f.do(t, http.MethodGet, "/api/posts/"+th.Items[0].Ref, nil, http.StatusOK))
	if headView.ReplyCount != 1 {
		t.Fatalf("reply count %d", headView.ReplyCount)
	}
	childRef := aliceRef(t, f, reply.Ref)
	f.do(t, http.MethodPost, "/api/posts/"+th.Items[0].Ref+"/detach", map[string]any{"child": childRef}, http.StatusNoContent)
	f.do(t, http.MethodPost, "/api/posts/"+th.Items[0].Ref+"/detach", map[string]any{"child": childRef}, http.StatusNotFound)
	f.do(t, http.MethodPost, "/api/posts/"+th.Items[0].Ref+"/detach-all", nil, http.StatusNoContent)
	b = decode[struct {
		Offer bool `json:"offer_detach_all"`
	}](t, f.do(t, http.MethodPut, "/api/posts/"+th.Items[0].Ref+"/ref-policy", map[string]any{"ref_policy": "nobody"}, http.StatusOK))
	if b.Offer {
		t.Fatal("offer with no attached children")
	}
	f.do(t, http.MethodDelete, "/api/posts/"+p.Ref, nil, http.StatusNoContent)
	f.do(t, http.MethodGet, "/api/posts/"+p.Ref, nil, http.StatusNotFound)
	f.do(t, http.MethodDelete, "/api/posts/"+p.Ref, nil, http.StatusNotFound)
}

// bobRef converts one of alice's post refs into bob's (both see the post).
func bobRef(t *testing.T, f *fixture, aliceRef string) string {
	t.Helper()
	d := f.h.App.Deps()
	id, ok := d.Refs.OpenPost(f.w.ID("alice"), gateRef(aliceRef))
	if !ok {
		t.Fatal("ref")
	}
	return string(d.Refs.Post(f.w.ID("bob"), id))
}

func aliceRef(t *testing.T, f *fixture, bobRef string) string {
	t.Helper()
	d := f.h.App.Deps()
	id, ok := d.Refs.OpenPost(f.w.ID("bob"), gateRef(bobRef))
	if !ok {
		t.Fatal("ref")
	}
	return string(d.Refs.Post(f.w.ID("alice"), id))
}

// Acceptance: referencing an invisible or foreign post answers the uniform 404
// and moves the rate counters exactly like a valid reference.
func TestInvisibleReferenceIsUniform(t *testing.T) {
	f := newFixture(t)
	f.h.Login("alice")
	create := func(parent string) (int, string, int) {
		before := f.rateCount(t)
		rec := f.h.Do(http.MethodPost, "/api/posts", map[string]any{"body": "re", "lang": "en",
			"parents": []any{map[string]any{"ref": parent, "kind": "ref"}}})
		return rec.Code, rec.Body.String(), f.rateCount(t) - before
	}
	okCode, _, okDelta := create(f.ref("alice", "bob1"))
	if okCode != http.StatusCreated {
		t.Fatalf("valid reference %d", okCode)
	}
	for name, r := range map[string]string{
		"other component": f.ref("alice", "x1"),
		"another viewer":  f.ref("bob", "bob1"),
		"garbage":         "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
	} {
		code, body, delta := create(r)
		if code != http.StatusNotFound || body != notFound || delta != okDelta {
			t.Errorf("%s: %d %q delta %d (valid: %d)", name, code, body, delta, okDelta)
		}
	}
	for _, path := range []string{"/api/posts/" + f.ref("alice", "x1"), "/api/posts/garbage"} {
		if rec := f.h.Do(http.MethodGet, path, nil); rec.Code != http.StatusNotFound || rec.Body.String() != notFound {
			t.Errorf("GET %s = %d %q", path, rec.Code, rec.Body)
		}
	}
	// Someone else's visible post cannot be edited, deleted or managed: same 404.
	r := f.ref("alice", "bob1")
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPatch, "/api/posts/" + r, map[string]any{"body": "mine"}},
		{http.MethodDelete, "/api/posts/" + r, nil},
		{http.MethodPut, "/api/posts/" + r + "/ref-policy", map[string]any{"ref_policy": "nobody"}},
		{http.MethodPost, "/api/posts/" + r + "/detach-all", nil},
	} {
		if rec := f.h.Do(c.method, c.path, c.body); rec.Code != http.StatusNotFound || rec.Body.String() != notFound {
			t.Errorf("%s %s = %d %q", c.method, c.path, rec.Code, rec.Body)
		}
	}
}

// Acceptance: a 20,000-code-point emoji post fits the 96 KiB body limit; 20,001
// is body_too_long; a body over the limit is bad_request.
func TestBodyLimits(t *testing.T) {
	f := newFixture(t)
	f.h.Login("alice")
	f.do(t, http.MethodPost, "/api/posts", map[string]any{"body": strings.Repeat("😀", 20000), "lang": "und"}, http.StatusCreated)
	e := decode[errBody](t, f.do(t, http.MethodPost, "/api/posts",
		map[string]any{"body": strings.Repeat("😀", 20001), "lang": "und"}, http.StatusUnprocessableEntity))
	if e.Error != "body_too_long" || e.Field != "body" {
		t.Fatalf("%+v", e)
	}
	e = decode[errBody](t, f.do(t, http.MethodPost, "/api/posts",
		map[string]any{"body": strings.Repeat("a", 100<<10), "lang": "und"}, http.StatusBadRequest))
	if e.Error != "bad_request" {
		t.Fatalf("%+v", e)
	}
	e = decode[errBody](t, f.do(t, http.MethodPost, "/api/posts", map[string]any{"body": "x", "lang": "en", "extra": 1},
		http.StatusBadRequest))
	if e.Error != "bad_request" {
		t.Fatalf("%+v", e)
	}
	parts := make([]any, 26)
	for i := range parts {
		parts[i] = map[string]any{"body": "p", "lang": "en"}
	}
	e = decode[errBody](t, f.do(t, http.MethodPost, "/api/posts/thread", map[string]any{"parts": parts}, http.StatusUnprocessableEntity))
	if e.Error != "too_many_parts" {
		t.Fatalf("%+v", e)
	}
}

// Acceptance: a thread publish is one write; detach and ref-policy are social writes.
func TestRouteClasses(t *testing.T) {
	newFixture(t)
	want := map[string]string{
		"POST /posts": "w:post", "POST /posts/thread": "w:post", "GET /posts/{ref}": "r:post_read",
		"PATCH /posts/{ref}": "w:post", "DELETE /posts/{ref}": "w:post", "PUT /posts/{ref}/ref-policy": "w:social",
		"POST /posts/{ref}/detach": "w:social", "POST /posts/{ref}/detach-all": "w:social",
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
