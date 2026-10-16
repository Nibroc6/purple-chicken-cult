package treeapi_test

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
	"github.com/Nibroc6/purple-chicken-cult/internal/api/treeapi"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire/wiretest"
)

type fixture struct {
	h *apitest.Harness
	w *wiretest.TestWorld
}

// newFixture: alice – bob – carol, xavier in another component. carol's root
// has 12 replies by bob (one minute apart); r11 (the newest) has a reply by
// alice; xavier posts once.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	h := apitest.Service(t, "tree", treeapi.Register)
	w := wiretest.World(t, h.App)
	for _, u := range []string{"alice", "bob", "carol", "xavier"} {
		w.User(u)
	}
	w.Friends("alice", "bob")
	w.Friends("bob", "carol")
	if err := h.App.Users.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	f := &fixture{h: h, w: w}
	f.advance(t, 90*24*time.Hour)
	w.Post("carol", "root", "the root")
	for i := 0; i < 12; i++ {
		f.advance(t, time.Minute)
		w.Post("bob", fmt.Sprintf("r%02d", i), fmt.Sprintf("reply %02d", i), w.ReplyTo("root"))
	}
	f.advance(t, time.Minute)
	w.Post("alice", "a1", "alice below", w.ReplyTo("r11"))
	w.Post("xavier", "x1", "far away")
	return f
}

func (f *fixture) advance(t *testing.T, d time.Duration) {
	t.Helper()
	if err := f.h.App.Hooks().AdvanceClock(d); err != nil {
		t.Fatal(err)
	}
}

// ref is viewer's ref for the post labelled label.
func (f *fixture) ref(viewer, label string) string {
	return string(f.h.App.Deps().Refs.Post(f.w.ID(viewer), f.w.PostID(label)))
}

func (f *fixture) do(t *testing.T, path string, want int) []byte {
	t.Helper()
	rec := f.h.Do(http.MethodGet, path, nil)
	if rec.Code != want {
		t.Fatalf("GET %s = %d %s, want %d", path, rec.Code, rec.Body, want)
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
	Post struct {
		Ref  string `json:"ref"`
		Body string `json:"body"`
	} `json:"post"`
	ShownAbove bool `json:"shown_above"`
}

type row struct {
	Gen    int    `json:"gen"`
	Label  string `json:"label"`
	Center string `json:"center"`
	Items  []item `json:"items"`
	More   int    `json:"more"`
	Cursor string `json:"cursor"`
}

type view struct {
	Snapshot   string `json:"snapshot"`
	Focal      string `json:"focal"`
	Rows       []row  `json:"rows"`
	HasEarlier bool   `json:"has_earlier"`
	HasLater   bool   `json:"has_later"`
}

const notFound = `{"error":"not_found"}` + "\n"

func q(path string, kv ...string) string {
	v := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		v.Set(kv[i], kv[i+1])
	}
	return path + "?" + v.Encode()
}

func rowPath(snapshot, kind string, kv ...string) string {
	return q("/api/tree/s/"+snapshot+"/"+kind, kv...)
}

func TestAnonymousGetsUniformNotFound(t *testing.T) {
	f := newFixture(t)
	for _, path := range []string{
		"/api/tree/" + f.ref("alice", "root"),
		rowPath("x", "row", "gen", "1", "center", f.ref("alice", "root")),
		rowPath("x", "more", "gen", "1", "center", f.ref("alice", "root"), "cursor", "x"),
	} {
		if rec := f.h.Do(http.MethodGet, path, nil); rec.Code != http.StatusNotFound || rec.Body.String() != notFound {
			t.Errorf("GET %s = %d %q", path, rec.Code, rec.Body)
		}
	}
}

func TestOpenRowMore(t *testing.T) {
	f := newFixture(t)
	f.h.Login("alice")
	tv := decode[view](t, f.do(t, q("/api/tree/"+f.ref("alice", "root"), "preset", "latest"), http.StatusOK))
	if tv.Focal != f.ref("alice", "root") || len(tv.Rows) != 3 || tv.HasEarlier || tv.HasLater {
		t.Fatalf("view %+v", tv)
	}
	r0, r1, r2 := tv.Rows[0], tv.Rows[1], tv.Rows[2]
	if r0.Gen != 0 || r0.Label != "this_post" || r0.Items[0].Post.Body != "the root" ||
		r1.Label != "replies" || len(r1.Items) != 10 || r1.More != 2 || r1.Cursor == "" || r1.Items[0].Post.Body != "reply 11" ||
		r2.Label != "replies_to_replies" || r2.Center != f.ref("alice", "r11") || r2.Items[0].Post.Body != "alice below" {
		t.Fatalf("rows %+v", tv.Rows)
	}
	more := decode[row](t, f.do(t, rowPath(tv.Snapshot, "more", "gen", "1", "center", f.ref("alice", "root"), "cursor", r1.Cursor), http.StatusOK))
	if len(more.Items) != 2 || more.More != 0 || more.Cursor != "" || more.Items[1].Post.Body != "reply 00" {
		t.Fatalf("more %+v", more)
	}
	// Swiping row +1 to a reply served on the second page: its (empty) replies.
	re := decode[row](t, f.do(t, rowPath(tv.Snapshot, "row", "gen", "2", "center", f.ref("alice", "r00")), http.StatusOK))
	if re.Gen != 2 || re.Center != f.ref("alice", "r00") || len(re.Items) != 0 {
		t.Fatalf("recentered %+v", re)
	}
	// Rows above: the parents of alice's reply.
	up := decode[view](t, f.do(t, "/api/tree/"+f.ref("alice", "a1"), http.StatusOK))
	if len(up.Rows) != 3 || up.Rows[0].Label != "earlier" || up.Rows[1].Label != "replying_to" || up.HasEarlier {
		t.Fatalf("up %+v", up)
	}

	// Problems about the caller's own input.
	for _, p := range []string{"top", "nope"} {
		rec := f.h.Do(http.MethodGet, q("/api/tree/"+f.ref("alice", "root"), "preset", p), nil)
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `"preset_unavailable"`) {
			t.Fatalf("preset %s: %d %s", p, rec.Code, rec.Body)
		}
	}
	if rec := f.h.Raw(http.MethodGet, rowPath(tv.Snapshot, "row", "gen", "one", "center", f.ref("alice", "root")), nil, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad gen: %d %s", rec.Code, rec.Body)
	}
	// Another viewer's snapshot or cursor, garbage, a center the snapshot has not
	// served next to the row: the uniform 404.
	for _, path := range []string{
		rowPath(tv.Snapshot, "row", "gen", "3", "center", f.ref("alice", "r05")),
		rowPath("garbage", "row", "gen", "1", "center", f.ref("alice", "root")),
	} {
		if rec := f.h.Do(http.MethodGet, path, nil); rec.Code != http.StatusNotFound || rec.Body.String() != notFound {
			t.Fatalf("GET %s = %d %q", path, rec.Code, rec.Body)
		}
	}
	f.h.Login("bob")
	for _, path := range []string{
		rowPath(tv.Snapshot, "row", "gen", "1", "center", f.ref("bob", "root")),
		rowPath(tv.Snapshot, "more", "gen", "1", "center", f.ref("bob", "root"), "cursor", r1.Cursor),
	} {
		if rec := f.h.Do(http.MethodGet, path, nil); rec.Code != http.StatusNotFound || rec.Body.String() != notFound {
			t.Fatalf("GET %s = %d %q", path, rec.Code, rec.Body)
		}
	}
	// The caller's own snapshot after 30 unused minutes: 409, reopen.
	f.h.Login("alice")
	f.advance(t, 31*time.Minute)
	rec := f.h.Do(http.MethodGet, rowPath(tv.Snapshot, "row", "gen", "2", "center", f.ref("alice", "r11")), nil)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"tree_snapshot_expired"`) {
		t.Fatalf("expired: %d %s", rec.Code, rec.Body)
	}
}

// Acceptance (charging uniformity): opening an invisible, foreign or garbage
// ref answers the uniform 404 and moves the rate counters exactly like a valid one.
func TestInvisibleRefsAreUniform(t *testing.T) {
	f := newFixture(t)
	f.h.Login("alice")
	call := func(ref string) (int, string, int) {
		before := f.rateCount(t)
		rec := f.h.Do(http.MethodGet, "/api/tree/"+ref, nil)
		return rec.Code, rec.Body.String(), f.rateCount(t) - before
	}
	okCode, _, okDelta := call(f.ref("alice", "root"))
	if okCode != http.StatusOK {
		t.Fatalf("valid: %d delta %d", okCode, okDelta)
	}
	for name, r := range map[string]string{
		"other component": f.ref("alice", "x1"),
		"another viewer":  f.ref("bob", "root"),
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
		"GET /tree/{ref}": "r:tree_view", "GET /tree/s/{snapshot}/row": "r:tree_view", "GET /tree/s/{snapshot}/more": "r:tree_view",
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
