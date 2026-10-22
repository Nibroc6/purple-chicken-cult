package notifyapi_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/api"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/apitest"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/authapi"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/friendsapi"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/notifyapi"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/limits"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire/wiretest"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/httpx"
)

type fixture struct {
	h *apitest.Harness
	w *wiretest.TestWorld
}

// newFixture: alice – bob – carol, xavier in another component; alice posts p1.
// Friends and auth routes are mounted too (requests produce notifications,
// logout ends the stream); only the notify routes are validated.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	h := apitest.Service(t, "notify", func(r chi.Router, d *api.Deps) {
		notifyapi.Register(r, d)
		friendsapi.Register(r, d)
		authapi.Register(r, d)
	})
	w := wiretest.World(t, h.App)
	for _, u := range []string{"alice", "bob", "carol", "xavier"} {
		w.User(u)
	}
	w.Friends("alice", "bob")
	w.Friends("bob", "carol")
	w.Post("alice", "p1", "hello")
	w.Post("xavier", "x1", "far")
	if err := h.App.Users.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	return &fixture{h: h, w: w}
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

// request makes carol ask alice for friendship (alice gets friend_request).
func (f *fixture) request(t *testing.T) {
	t.Helper()
	f.h.Login("carol")
	if rec := f.h.Raw(http.MethodPost, "/api/people/"+f.person("carol", "alice")+"/request", nil, nil); rec.Code != http.StatusAccepted {
		t.Fatalf("request = %d %s", rec.Code, rec.Body)
	}
}

func (f *fixture) rateCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.h.App.Deps().DB.Q().QueryRow(context.Background(), "SELECT coalesce(sum(count), 0) FROM rate_counters").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// bucketCount is the caller-independent sum of one rate_counters bucket.
func (f *fixture) bucketCount(t *testing.T, bucket string) int {
	t.Helper()
	var n int
	if err := f.h.App.Deps().DB.Q().QueryRow(context.Background(),
		"SELECT coalesce(sum(count), 0) FROM rate_counters WHERE bucket = $1", bucket).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

type listOut struct {
	Items []struct {
		ID     string `json:"id"`
		Kind   string `json:"kind"`
		Count  int    `json:"count"`
		Person *struct {
			Ref    string `json:"ref"`
			Handle string `json:"handle"`
		} `json:"person"`
		Read bool `json:"read"`
	} `json:"items"`
	Next   string `json:"next"`
	Unread int    `json:"unread"`
}

func decode[T any](t *testing.T, b []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("%s: %v", b, err)
	}
	return v
}

func TestListReadAndCharging(t *testing.T) {
	f := newFixture(t)
	f.request(t)
	f.h.Login("alice")
	l := decode[listOut](t, f.do(t, http.MethodGet, "/api/notifications", nil, http.StatusOK))
	if len(l.Items) != 1 || l.Unread != 1 || l.Items[0].Kind != "friend_request" || l.Items[0].Person == nil ||
		l.Items[0].Person.Ref != f.person("alice", "carol") || l.Items[0].Person.Handle != "carol" || l.Items[0].Read {
		t.Fatalf("list: %+v", l)
	}
	// The list is a graph read (plan §5.3 class R:graph_read).
	reads := f.bucketCount(t, limits.ReadBucket(limits.ReadGraph))
	f.do(t, http.MethodGet, "/api/notifications", nil, http.StatusOK)
	if got := f.bucketCount(t, limits.ReadBucket(limits.ReadGraph)); got != reads+1 {
		t.Fatalf("GET /notifications charged %d graph reads, want 1", got-reads)
	}
	before := f.rateCount(t)
	f.do(t, http.MethodPost, "/api/notifications/read", map[string]string{"up_to": l.Items[0].ID}, http.StatusNoContent)
	if f.rateCount(t) != before {
		t.Fatal("POST /notifications/read is charged")
	}
	l = decode[listOut](t, f.do(t, http.MethodGet, "/api/notifications", nil, http.StatusOK))
	if l.Unread != 0 || !l.Items[0].Read {
		t.Fatalf("after read: %+v", l)
	}
	f.do(t, http.MethodPost, "/api/notifications/read", map[string]string{"up_to": "AAAA"}, http.StatusNotFound)
	f.do(t, http.MethodPost, "/api/notifications/read", map[string]any{"up_to": l.Items[0].ID, "x": 1}, http.StatusBadRequest)
	f.do(t, http.MethodGet, "/api/notifications?cursor=garbage", nil, http.StatusNotFound)
	// The requester (and anyone else) sees nothing of it.
	f.h.Login("carol")
	l = decode[listOut](t, f.do(t, http.MethodGet, "/api/notifications", nil, http.StatusOK))
	if len(l.Items) != 0 || l.Unread != 0 {
		t.Fatalf("carol: %+v", l)
	}
	f.h.Logout()
	f.do(t, http.MethodGet, "/api/notifications", nil, http.StatusNotFound)
}

func TestDeclineIsSilent(t *testing.T) {
	f := newFixture(t)
	f.request(t)
	f.h.Login("alice")
	if rec := f.h.Raw(http.MethodPost, "/api/friends/requests/"+f.person("alice", "carol")+"/decline", nil, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("decline = %d %s", rec.Code, rec.Body)
	}
	f.h.Login("carol")
	if l := decode[listOut](t, f.do(t, http.MethodGet, "/api/notifications", nil, http.StatusOK)); len(l.Items) != 0 {
		t.Fatalf("carol after decline: %+v", l)
	}
}

func TestNotifyMute(t *testing.T) {
	f := newFixture(t)
	f.h.Login("alice")
	p := "/api/posts/" + f.post("alice", "p1") + "/notify-mute"
	social := f.h.Charges()["b:social"]
	f.do(t, http.MethodPut, p, nil, http.StatusNoContent)
	f.do(t, http.MethodPut, p, nil, http.StatusNoContent)
	f.do(t, http.MethodDelete, p, nil, http.StatusNoContent)
	// Each is a social write (W:social), garbage included (charged before the
	// ref resolves); the DELETE that turns the last PUT back is free.
	if got := f.h.Charges()["b:social"]; got != social+2 {
		t.Fatalf("notify-mute charged %d social writes, want 2", got-social)
	}
	social += 2
	defer func() {
		if got := f.h.Charges()["b:social"]; got != social+3 {
			t.Errorf("not-found notify-mute charged %d social writes, want 3", got-social)
		}
	}()
	// Another component's post, someone else's ref and garbage look the same.
	for _, ref := range []string{f.post("alice", "x1"), f.post("bob", "p1"), "garbage"} {
		rec := f.h.Do(http.MethodPut, "/api/posts/"+ref+"/notify-mute", nil)
		if rec.Code != http.StatusNotFound || strings.TrimSpace(rec.Body.String()) != `{"error":"not_found"}` {
			t.Fatalf("%s = %d %s", ref, rec.Code, rec.Body)
		}
	}
}

// sse is one open /api/events stream over a real server.
type sse struct {
	t    *testing.T
	resp *http.Response
	rd   *bufio.Reader
	ev   chan string // "event:data" frames and "ping"
}

func openSSE(t *testing.T, srv *httptest.Server, cookie string) *sse {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: httpx.CookieName(false), Value: cookie}) //nolint:gosec // test request
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	s := &sse{t: t, resp: resp, rd: bufio.NewReader(resp.Body), ev: make(chan string, 64)}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		return s
	}
	go func() {
		defer close(s.ev)
		var typ string
		for {
			line, err := s.rd.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\n")
			switch {
			case line == ": ping":
				s.ev <- "ping"
			case strings.HasPrefix(line, "event: "):
				typ = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				s.ev <- typ + ":" + strings.TrimPrefix(line, "data: ")
			}
		}
	}()
	return s
}

// next returns the next frame other than a ping, or "" after d, or "EOF".
func (s *sse) next(d time.Duration) string {
	timeout := time.After(d)
	for {
		select {
		case f, ok := <-s.ev:
			if !ok {
				return "EOF"
			}
			if f != "ping" {
				return f
			}
		case <-timeout:
			return ""
		}
	}
}

func TestEventsDeliverAndCloseOnLogout(t *testing.T) {
	old := notifyapi.PingEvery
	notifyapi.PingEvery = 50 * time.Millisecond
	t.Cleanup(func() { notifyapi.PingEvery = old })
	f := newFixture(t)
	srv := httptest.NewServer(f.h.Handler)
	t.Cleanup(srv.Close)

	if s := openSSE(t, srv, ""); s.resp.StatusCode != http.StatusNotFound {
		t.Fatalf("anonymous /events = %d", s.resp.StatusCode)
	}
	cookie, csrf := wiretest.Login(t, f.h.App, "alice")
	s := openSSE(t, srv, cookie)
	if ct := s.resp.Header.Get("Content-Type"); s.resp.StatusCode != http.StatusOK || ct != "text/event-stream" {
		t.Fatalf("events = %d %s", s.resp.StatusCode, ct)
	}
	if got := s.next(time.Second); got != `hello:{"unread":0}` {
		t.Fatalf("first frame %q", got)
	}
	// Keep-alive comments.
	pings := 0
	for deadline := time.After(time.Second); pings < 2; {
		select {
		case fr := <-s.ev:
			if fr == "ping" {
				pings++
			}
		case <-deadline:
			t.Fatalf("pings: %d", pings)
		}
	}
	// A friend request reaches alice's stream within 1 s.
	start := time.Now()
	f.request(t)
	if got := s.next(time.Second); got != `notification:{"unread":1}` {
		t.Fatalf("after request %q", got)
	}
	if time.Since(start) > time.Second {
		t.Fatal("delivered too late")
	}
	// Logging out closes the stream within 1 s.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/auth/logout", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Origin", f.h.Origin)
	req.Header.Set("X-PCC-CSRF", csrf)
	req.AddCookie(&http.Cookie{Name: httpx.CookieName(false), Value: cookie}) //nolint:gosec // test request
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout = %d", resp.StatusCode)
	}
	start = time.Now()
	if got := s.next(time.Second); got != "EOF" {
		t.Fatalf("after logout %q", got)
	}
	if time.Since(start) > time.Second {
		t.Fatal("stream closed too late")
	}
	// The ended session cannot reconnect.
	if s2 := openSSE(t, srv, cookie); s2.resp.StatusCode != http.StatusNotFound {
		t.Fatalf("reconnect after logout = %d", s2.resp.StatusCode)
	}
}

func TestEventsSpecDocumented(t *testing.T) {
	f := newFixture(t)
	f.h.Logout()
	// The uniform 404 for an anonymous caller, validated against the fragment.
	f.do(t, http.MethodGet, "/api/events", nil, http.StatusNotFound)
}
