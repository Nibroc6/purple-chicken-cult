package router_test

import (
	"maps"
	"net/http"
	"testing"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/api/apitest"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire/wiretest"
)

// TestChargingUniformForBodyRefs (INT-0, plan §7 "charging uniformity",
// fidelity #7): for the write routes that carry refs in the body rather than
// the path (modapi's TestChargingIsUniformForEveryRoute covers path refs), the
// rate-counter deltas — every bucket and window, not only the total — are the
// same for a visible parent, an invisible one, another viewer's ref and garbage
// (build-up buckets included).
func TestChargingUniformForBodyRefs(t *testing.T) {
	h := apitest.App(t)
	w := wiretest.World(t, h.App)
	for _, u := range []string{"alice", "bob", "xavier"} {
		w.User(u)
	}
	w.Friends("alice", "bob")
	w.Post("bob", "b1", "bob writes")
	w.Post("xavier", "x1", "far away")
	h.Login("alice")
	d := h.App.Deps()
	refs := map[string]string{
		"visible":   string(d.Refs.Post(w.ID("alice"), w.PostID("b1"))),
		"invisible": string(d.Refs.Post(w.ID("alice"), w.PostID("x1"))),
		"foreign":   string(d.Refs.Post(w.ID("bob"), w.PostID("b1"))),
		"garbage":   "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
	}
	counters := h.Charges
	for _, route := range []string{"/api/posts", "/api/posts/thread"} {
		var want map[string]int
		for _, name := range []string{"visible", "invisible", "foreign", "garbage"} {
			// Past the minimum spacing; still inside the same hour and day windows.
			if err := h.App.Hooks().AdvanceClock(3 * time.Second); err != nil {
				t.Fatal(err)
			}
			parents := []map[string]string{{"ref": refs[name], "kind": "ref"}}
			var body any = map[string]any{"body": "reply " + name, "lang": "en", "parents": parents}
			if route == "/api/posts/thread" {
				body = map[string]any{"parents": parents, "parts": []any{
					map[string]any{"body": "part one " + name, "lang": "en"}, map[string]any{"body": "part two", "lang": "en"}}}
			}
			before := counters()
			rec := h.Raw(http.MethodPost, route, body, nil)
			if rec.Code == http.StatusTooManyRequests {
				t.Fatalf("%s %s: rate limited during the test", route, name)
			}
			if name == "visible" && rec.Code >= 300 {
				t.Fatalf("%s with a visible parent: %d %s", route, rec.Code, rec.Body)
			}
			after := counters()
			delta := map[string]int{}
			for k, v := range after {
				if v != before[k] {
					delta[k] = v - before[k]
				}
			}
			if want == nil {
				want = delta
				if len(want) == 0 {
					t.Fatalf("%s with a %s parent (%d) charged nothing", route, name, rec.Code)
				}
				continue
			}
			if !maps.Equal(delta, want) {
				t.Errorf("%s with a %s parent (%d): charged %v, the visible parent charged %v", route, name, rec.Code, delta, want)
			}
		}
	}
}
