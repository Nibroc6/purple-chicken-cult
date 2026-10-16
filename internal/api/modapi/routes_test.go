package modapi_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/getkin/kin-openapi/openapi3filter"

	"github.com/Nibroc6/purple-chicken-cult/internal/api"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/apitest"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/modapi"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/notifyapi"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/router"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire/wiretest"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/httpx"
)

func init() {
	for _, ct := range []string{"application/reports+json", "application/csp-report"} {
		openapi3filter.RegisterBodyDecoder(ct, openapi3filter.JSONBodyDecoder)
	}
}

type fixture struct {
	h *apitest.Harness
	w *wiretest.TestWorld
}

// newFixture: alice invited bob; alice – bob – carol; xavier elsewhere; mo1 and
// mo2 are moderators. bob posts b1, xavier x1.
func newFixture(t *testing.T, h *apitest.Harness) *fixture {
	t.Helper()
	w := wiretest.World(t, h.App)
	for _, u := range []string{"alice", "bob", "carol", "xavier", "mo1", "mo2"} {
		w.User(u)
	}
	w.Friends("alice", "bob")
	w.Friends("bob", "carol")
	w.Post("bob", "b1", "bob writes")
	w.Post("xavier", "x1", "far")
	ctx := context.Background()
	if _, err := h.App.Deps().DB.Q().Exec(ctx, "UPDATE users SET invited_by = $2, is_genesis = false WHERE id = $1",
		int64(w.ID("bob")), int64(w.ID("alice"))); err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"mo1", "mo2"} {
		if _, err := h.App.Deps().DB.Q().Exec(ctx, "UPDATE users SET role = 1 WHERE id = $1", int64(w.ID(m))); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.App.Users.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	return &fixture{h: h, w: w}
}

func newModFixture(t *testing.T) *fixture {
	return newFixture(t, apitest.Service(t, "mod", modapi.Register))
}

func (f *fixture) person(viewer, who string) string {
	return string(f.h.App.Deps().Refs.User(f.w.ID(viewer), f.w.ID(who)))
}

func (f *fixture) post(viewer, label string) string {
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

func (f *fixture) advance(t *testing.T, d time.Duration) {
	t.Helper()
	if err := f.h.App.Hooks().AdvanceClock(d); err != nil {
		t.Fatal(err)
	}
}

func decode[T any](t *testing.T, b []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("%s: %v", b, err)
	}
	return v
}

func report(kind, ref, reason string) map[string]any {
	return map[string]any{"target": map[string]string{"kind": kind, "ref": ref}, "reason": reason}
}

func TestReportAnswersAndChargesAlike(t *testing.T) {
	f := newModFixture(t)
	f.h.Login("carol")
	call := func(body any) (int, string, int) {
		before := f.rateCount(t)
		rec := f.h.Do(http.MethodPost, "/api/reports", body)
		return rec.Code, rec.Body.String(), f.rateCount(t) - before
	}
	okCode, okBody, okDelta := call(report("post", f.post("carol", "b1"), "spam"))
	if okCode != http.StatusAccepted || okDelta != 1 {
		t.Fatalf("valid report %d %q delta %d", okCode, okBody, okDelta)
	}
	for name, body := range map[string]any{
		"other component": report("post", f.post("carol", "x1"), "spam"),
		"another viewer":  report("post", f.post("bob", "b1"), "spam"),
		"garbage":         report("post", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "spam"),
		"invisible user":  report("person", f.person("carol", "xavier"), "bot"),
		"inviter":         report("inviter", f.person("carol", "bob"), "bot"),
		"preview":         report("preview", f.post("carol", "b1"), "misleading_preview"),
	} {
		code, b, delta := call(body)
		if code != okCode || b != okBody || delta != okDelta {
			t.Fatalf("%s: %d %q delta %d, want %d %q %d", name, code, b, delta, okCode, okBody, okDelta)
		}
	}
	rec := f.h.Do(http.MethodPost, "/api/reports", report("post", f.post("carol", "b1"), "rude"))
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `"error":"reason_invalid"`) {
		t.Fatalf("invalid reason %d %s", rec.Code, rec.Body)
	}
	f.do(t, http.MethodPost, "/api/reports", map[string]any{"target": map[string]string{"kind": "post", "ref": "x"},
		"reason": "spam", "extra": 1}, http.StatusBadRequest)
	f.h.Logout()
	f.do(t, http.MethodPost, "/api/reports", report("post", f.post("carol", "b1"), "spam"), http.StatusNotFound)
}

func TestRateLimitedBodyIsUniform(t *testing.T) {
	f := newModFixture(t)
	p := f.h.App.Limits.Policy()
	p.Writes["report"] = p.Writes["report"][:1]
	p.Writes["report"][0].Max = 2
	f.h.App.Limits.SetPolicy(p)
	f.h.Login("carol")
	f.do(t, http.MethodPost, "/api/reports", report("post", f.post("carol", "b1"), "spam"), http.StatusAccepted)
	f.do(t, http.MethodPost, "/api/reports", report("person", f.person("carol", "bob"), "spam"), http.StatusAccepted)
	f.advance(t, 90*time.Minute)
	var first string
	for name, body := range map[string]any{
		"valid":   report("post", f.post("carol", "b1"), "spam"),
		"garbage": report("post", "garbage", "spam"),
		"foreign": report("post", f.post("xavier", "x1"), "spam"),
		"invalid": report("post", f.post("carol", "b1"), "rude"),
	} {
		rec := f.h.Do(http.MethodPost, "/api/reports", body)
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("%s: %d", name, rec.Code)
		}
		if first == "" {
			first = rec.Body.String()
		}
		if rec.Body.String() != first {
			t.Fatalf("%s: %s differs from %s", name, rec.Body, first)
		}
	}
	// retry_after_s is the rest of the caller's own UTC day.
	if v := decode[map[string]any](t, []byte(first)); v["error"] != "rate_limited" || v["retry_after_s"] != float64(22*3600+30*60) {
		t.Fatalf("429 %s", first)
	}
	// Someone else is unaffected.
	f.h.Login("alice")
	f.do(t, http.MethodPost, "/api/reports", report("person", f.person("alice", "bob"), "spam"), http.StatusAccepted)
}

type queueOut struct {
	Items []struct {
		ID      string `json:"id"`
		Reason  string `json:"reason"`
		Subject *struct {
			Ref    string `json:"ref"`
			Handle string `json:"handle"`
		} `json:"subject"`
		SubjectInviter *struct {
			Kind   string `json:"kind"`
			Handle string `json:"handle"`
		} `json:"subject_inviter"`
		SubjectProbation bool `json:"subject_probation"`
		Post             *struct {
			Body string `json:"body"`
		} `json:"post"`
		AwaitingSecond bool `json:"awaiting_second"`
	} `json:"items"`
	Next string `json:"next"`
}

type blameOut struct {
	Items []struct {
		Action        string `json:"action"`
		Reason        string `json:"reason"`
		InviteeHandle string `json:"invitee_handle"`
		Post          *struct {
			Body string `json:"body"`
		} `json:"post"`
		Read bool `json:"read"`
	} `json:"items"`
}

func TestModeratorFlow(t *testing.T) {
	f := newModFixture(t)
	f.h.Login("carol")
	f.do(t, http.MethodPost, "/api/reports", report("post", f.post("carol", "b1"), "spam"), http.StatusAccepted)
	f.do(t, http.MethodPost, "/api/reports", report("person", f.person("carol", "bob"), "bot"), http.StatusAccepted)
	// Moderator surfaces do not exist for members.
	for _, p := range []string{"/api/mod/queue"} {
		f.do(t, http.MethodGet, p, nil, http.StatusNotFound)
	}
	f.do(t, http.MethodPost, "/api/mod/items/garbage/resolve", map[string]string{"action": "dismiss", "reason": "other"}, http.StatusNotFound)

	f.h.Login("mo1")
	q := decode[queueOut](t, f.do(t, http.MethodGet, "/api/mod/queue", nil, http.StatusOK))
	if len(q.Items) != 2 || q.Items[0].Reason != "spam" || q.Items[0].Post == nil || q.Items[0].Post.Body != "bob writes" ||
		q.Items[0].Subject == nil || q.Items[0].Subject.Handle != "bob" || q.Items[0].SubjectInviter == nil ||
		q.Items[0].SubjectInviter.Handle != "alice" {
		t.Fatalf("queue %+v", q)
	}
	spam, bot := q.Items[0].ID, q.Items[1].ID
	f.do(t, http.MethodPost, "/api/mod/items/"+spam+"/resolve", map[string]string{"action": "ban", "reason": "spam"}, http.StatusUnprocessableEntity)
	f.do(t, http.MethodPost, "/api/mod/items/"+spam+"/resolve", map[string]string{"action": "remove_post", "reason": "spam"}, http.StatusNoContent)
	f.do(t, http.MethodPost, "/api/mod/items/"+spam+"/resolve", map[string]string{"action": "remove_post", "reason": "spam"}, http.StatusConflict)

	// suspend_user needs a fresh step-up: the session's passkey time is 10 min old.
	f.advance(t, 10*time.Minute)
	b := f.do(t, http.MethodPost, "/api/mod/items/"+bot+"/resolve", map[string]string{"action": "suspend_user", "reason": "bot"}, http.StatusUnauthorized)
	if !strings.Contains(string(b), "step_up_required") {
		t.Fatalf("%s", b)
	}
	f.h.Login("mo1") // a fresh passkey sign-in
	f.do(t, http.MethodPost, "/api/mod/items/"+bot+"/resolve", map[string]string{"action": "suspend_user", "reason": "bot"}, http.StatusNoContent)
	f.do(t, http.MethodPost, "/api/mod/items/"+bot+"/confirm", nil, http.StatusConflict) // the same moderator

	// Only the removal reached the inviter so far.
	f.h.Login("alice")
	bl := decode[blameOut](t, f.do(t, http.MethodGet, "/api/blame", nil, http.StatusOK))
	if len(bl.Items) != 1 || bl.Items[0].Action != "remove_post" || bl.Items[0].Post == nil || bl.Items[0].Post.Body != "bob writes" {
		t.Fatalf("blame %+v", bl)
	}

	f.h.Login("mo2")
	q = decode[queueOut](t, f.do(t, http.MethodGet, "/api/mod/queue", nil, http.StatusOK))
	if len(q.Items) != 1 || !q.Items[0].AwaitingSecond {
		t.Fatalf("queue %+v", q)
	}
	f.do(t, http.MethodPost, "/api/mod/items/"+q.Items[0].ID+"/confirm", nil, http.StatusNoContent)

	f.h.Login("alice")
	bl = decode[blameOut](t, f.do(t, http.MethodGet, "/api/blame", nil, http.StatusOK))
	if len(bl.Items) != 2 || bl.Items[0].Action != "suspend_user" || bl.Items[0].Read || !bl.Items[1].Read {
		t.Fatalf("blame %+v", bl)
	}
	// carol sees bob by name in the log; xavier sees a pseudonym.
	f.h.Login("carol")
	type logOut struct {
		Items []struct {
			Action  string            `json:"action"`
			Count   int               `json:"count"`
			Subject map[string]string `json:"subject"`
		} `json:"items"`
	}
	l := decode[logOut](t, f.do(t, http.MethodGet, "/api/mod/log", nil, http.StatusOK))
	if len(l.Items) != 2 || l.Items[0].Subject["handle"] != "bob" {
		t.Fatalf("carol's log %+v", l)
	}
	f.h.Login("xavier")
	l = decode[logOut](t, f.do(t, http.MethodGet, "/api/mod/log", nil, http.StatusOK))
	if len(l.Items) != 2 || l.Items[0].Subject["handle"] != "" || l.Items[0].Subject["pseudonym"] == "" {
		t.Fatalf("xavier's log %+v", l)
	}
	f.do(t, http.MethodGet, "/api/mod/log?cursor=garbage", nil, http.StatusNotFound)
	// The suspended account's sessions no longer work.
	f.h.Login("bob")
	f.do(t, http.MethodGet, "/api/blame", nil, http.StatusNotFound)
}

func TestCSPReports(t *testing.T) {
	f := newModFixture(t)
	reports := `[{"type":"csp-violation","url":"https://pcc.example/p/secret","body":{"effectiveDirective":"script-src-elem",` +
		`"blockedURL":"https://evil.example/x.js","sample":"alert(1)"}},{"type":"deprecation","body":{}}]`
	legacy := `{"csp-report":{"document-uri":"https://pcc.example/","violated-directive":"img-src https:"}}`
	for _, c := range []struct{ ct, body string }{{"application/reports+json", reports}, {"application/csp-report", legacy}} {
		req := httptest.NewRequest(http.MethodPost, "/api/csp-report", strings.NewReader(c.body))
		req.Header.Set("Content-Type", c.ct)
		req.Header.Set("Origin", f.h.Origin)
		w := httptest.NewRecorder()
		f.h.Handler.ServeHTTP(w, req)
		if w.Code != http.StatusNoContent {
			t.Fatalf("%s: %d %s", c.ct, w.Code, w.Body)
		}
	}
	got, err := f.h.App.Moderation.CSPCounts(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if got["script-src-elem"] != 1 || got["img-src"] != 1 || len(got) != 2 {
		t.Fatalf("counts %v", got)
	}
	// Only a count per directive and day is kept.
	var cols int
	if err := f.h.App.Deps().DB.Q().QueryRow(context.Background(),
		"SELECT count(*) FROM information_schema.columns WHERE table_name = 'csp_violations'").Scan(&cols); err != nil || cols != 3 {
		t.Fatalf("columns %d %v", cols, err)
	}
	if modapi.Directives(json.RawMessage(`"x"`)) != nil {
		t.Fatal("a string is no report")
	}
}

// TestAnonBucketIgnoresSpoofedForwardedFor: X-Forwarded-For from an untrusted
// peer does not move the anonymous bucket (RealIP honors it only from
// PCC_TRUSTED_PROXIES).
func TestAnonBucketIgnoresSpoofedForwardedFor(t *testing.T) {
	f := newModFixture(t)
	send := func(xff string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/csp-report", strings.NewReader(`[]`))
		req.Header.Set("Content-Type", "application/reports+json")
		req.Header.Set("Origin", f.h.Origin)
		req.Header.Set("X-Forwarded-For", xff)
		req.Header.Set("CF-Connecting-IP", xff)
		w := httptest.NewRecorder()
		f.h.Handler.ServeHTTP(w, req)
		return w.Code
	}
	for i := 0; i < 30; i++ {
		if c := send("198.51." + string(rune('0'+i%10)) + ".1"); c != http.StatusNoContent {
			t.Fatalf("report %d: %d", i, c)
		}
	}
	if c := send("203.0.113.9"); c != http.StatusTooManyRequests {
		t.Fatalf("31st report from the same peer with a new X-Forwarded-For: %d", c)
	}
}

// TestHeadersOnEveryResponse: HTML, JSON, SSE, static files and 404s all carry
// the TDM reservation, the CSP and X-Frame-Options: DENY.
func TestHeadersOnEveryResponse(t *testing.T) {
	h := apitest.Service(t, "mod", modapi.Register)
	cfg := h.App.Config()
	static := fstest.MapFS{
		"index.html":           {Data: []byte(apitest.TestIndexHTML)},
		"assets/app-1.js":      {Data: []byte("console.log(1)")},
		"manifest.webmanifest": {Data: []byte("{}")},
	}
	d := api.FromApp(h.App, cfg, slog.New(slog.DiscardHandler), static)
	srv := httptest.NewServer(router.NewWith(d, modapi.Register, notifyapi.Register))
	t.Cleanup(srv.Close)
	cookie, _ := wiretest.Login(t, h.App, "alice")

	check := func(name string, resp *http.Response) {
		t.Helper()
		for k, want := range map[string]string{"tdm-reservation": "1", "X-Frame-Options": "DENY", "X-Content-Type-Options": "nosniff"} {
			if got := resp.Header.Get(k); got != want {
				t.Errorf("%s: %s = %q", name, k, got)
			}
		}
		if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") ||
			!strings.Contains(csp, "frame-ancestors 'none'") || !strings.Contains(csp, "report-to csp") {
			t.Errorf("%s: CSP %q", name, csp)
		}
		if !strings.Contains(resp.Header.Get("X-Robots-Tag"), "noai") {
			t.Errorf("%s: X-Robots-Tag %q", name, resp.Header.Get("X-Robots-Tag"))
		}
	}
	get := func(path string, auth bool) *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		if auth {
			req.AddCookie(&http.Cookie{Name: httpx.CookieName(false), Value: cookie}) //nolint:gosec // test request
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	for _, c := range []struct {
		name, path string
		auth       bool
		status     int
		ctype      string
		cache      string
	}{
		{"html", "/", false, 200, "text/html; charset=utf-8", "no-store"},
		{"spa route", "/terms", false, 200, "text/html; charset=utf-8", "no-store"},
		{"asset", "/assets/app-1.js", false, 200, "text/javascript; charset=utf-8", "public, max-age=31536000, immutable"},
		{"manifest", "/manifest.webmanifest", false, 200, "application/manifest+json", "no-cache"},
		{"missing asset", "/assets/nope.js", false, 404, "", "no-store"},
		{"json", "/api/mod/log", true, 200, "application/json", "no-store"},
		{"api 404", "/api/nope", true, 404, "application/json", "no-store"},
		{"api 404 signed out", "/api/mod/log", false, 404, "application/json", "no-store"},
		{"robots", "/robots.txt", false, 200, "text/plain; charset=utf-8", "public, max-age=3600"},
		{"ai.txt", "/ai.txt", false, 200, "text/plain; charset=utf-8", "public, max-age=3600"},
		{"tdmrep", "/.well-known/tdmrep.json", false, 200, "application/json", "public, max-age=3600"},
		{"keys", "/.well-known/pcc-keys.json", false, 200, "application/json", "public, max-age=300"},
	} {
		resp := get(c.path, c.auth)
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != c.status {
			t.Errorf("%s: %d %s", c.name, resp.StatusCode, body)
		}
		if c.ctype != "" && resp.Header.Get("Content-Type") != c.ctype {
			t.Errorf("%s: Content-Type %q", c.name, resp.Header.Get("Content-Type"))
		}
		if resp.Header.Get("Cache-Control") != c.cache {
			t.Errorf("%s: Cache-Control %q", c.name, resp.Header.Get("Cache-Control"))
		}
		check(c.name, resp)
		if c.name == "keys" {
			var doc struct {
				Server []struct {
					KeyID       string `json:"key_id"`
					Fingerprint string `json:"fingerprint"`
				} `json:"server"`
			}
			if err := json.Unmarshal(body, &doc); err != nil || len(doc.Server) == 0 || len(doc.Server[0].Fingerprint) != 64 {
				t.Errorf("keys %s %v", body, err)
			}
		}
	}
	// The SSE stream.
	resp := get("/api/events", true)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("events %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	check("sse", resp)
	line, _ := bufio.NewReader(resp.Body).ReadString('\n')
	if !strings.HasPrefix(line, "event: hello") {
		t.Errorf("first frame %q", line)
	}
	_ = resp.Body.Close()
}

// TestChargingIsUniformForEveryRoute: for every Phase 0 route with a path
// parameter, a valid post ref, a valid person ref, another viewer's ref and
// garbage change the rate counters identically (fidelity #7): one charge per
// window for read and write classes, none for free and anonymous ones,
// whatever the answer.
func TestChargingIsUniformForEveryRoute(t *testing.T) {
	f := newFixture(t, apitest.App(t))
	f.h.Login("carol")
	for _, e := range router.Catalogue {
		if e.Phase != 0 || e.Module == "testhooks" || !strings.Contains(e.Pattern, "{") {
			continue
		}
		var body any
		if e.Method != http.MethodGet && e.Method != http.MethodDelete {
			body = map[string]any{}
		}
		want := 0 // free and anonymous classes never touch rate_counters
		switch e.Class.Kind {
		case 'r':
			want = 1
		case 'w':
			want = len(f.h.App.Limits.Policy().Writes[e.Class.Name]) // one row per window (post: hour and day)
		}
		for name, ref := range map[string]string{
			"post": f.post("carol", "b1"), "person": f.person("carol", "bob"),
			"garbage": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "foreign": f.post("xavier", "x1"),
		} {
			path := "/api" + e.Pattern
			for _, p := range []string{"{ref}", "{hs}", "{id}", "{snapshot}"} {
				path = strings.ReplaceAll(path, p, ref)
			}
			before := f.rateCount(t)
			rec := f.h.Raw(e.Method, path, body, nil)
			if delta := f.rateCount(t) - before; delta != want {
				t.Errorf("%s %s with a %s ref (%d): charged %d, want %d", e.Method, e.Pattern, name, rec.Code, delta, want)
			}
			if rec.Code == http.StatusTooManyRequests {
				t.Fatalf("%s %s: rate limited during the test", e.Method, e.Pattern)
			}
		}
	}
}
