package friendsapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/auth/authtest"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/friends"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/httpx"
)

// P1 routes: hide-via, the friend+ upgrade opener and co-signed changes.

func TestP1RoutesAnonymousNotFound(t *testing.T) {
	f := newFixture(t)
	r := f.ref("alice", "carol")
	for _, c := range []struct{ method, path string }{
		{http.MethodPut, "/api/people/" + r + "/hide-via"}, {http.MethodDelete, "/api/people/" + r + "/hide-via"},
		{http.MethodPost, "/api/friends/" + r + "/upgrade"},
	} {
		if rec := f.h.Do(c.method, c.path, nil); rec.Code != http.StatusNotFound || rec.Body.String() != `{"error":"not_found"}`+"\n" {
			t.Errorf("%s %s = %d %q", c.method, c.path, rec.Code, rec.Body)
		}
	}
}

// Hiding the paths through carol (alice – carol – bob) keeps carol and drops
// bob; showing them again brings bob back. Only friends can be hidden via.
func TestHideViaRoutes(t *testing.T) {
	f := newFixture(t)
	f.h.Login("alice")
	carol := "/api/people/" + f.ref("alice", "carol")
	bob := "/api/people/" + f.ref("alice", "bob")
	f.do(t, http.MethodPut, bob+"/hide-via", http.StatusNotFound) // not a friend
	f.do(t, http.MethodPut, carol+"/hide-via", http.StatusNoContent)
	f.do(t, http.MethodPut, carol+"/hide-via", http.StatusNoContent) // idempotent
	p := decode[struct {
		Relation  string `json:"relation"`
		HiddenVia bool   `json:"hidden_via"`
	}](t, f.do(t, http.MethodGet, carol, http.StatusOK))
	if !p.HiddenVia || p.Relation != "friend" {
		t.Fatalf("carol %+v", p)
	}
	f.do(t, http.MethodGet, bob, http.StatusNotFound)
	f.do(t, http.MethodDelete, carol+"/hide-via", http.StatusNoContent)
	f.do(t, http.MethodGet, bob, http.StatusOK)
	f.do(t, http.MethodDelete, carol+"/hide-via", http.StatusNoContent)
	f.do(t, http.MethodPut, "/api/people/garbage/hide-via", http.StatusNotFound)
}

// The upgrade opener answers 201 with the handshake for a plain friend only.
func TestUpgradeRoute(t *testing.T) {
	f := newFixture(t)
	f.h.Login("alice")
	b := f.do(t, http.MethodPost, "/api/friends/"+f.ref("alice", "carol")+"/upgrade", http.StatusCreated)
	hv := decode[gate.HandshakeView](t, b)
	if hv.HS == "" || hv.FrameKey == "" || hv.Mode != "qr_audio" {
		t.Fatalf("handshake %s", b)
	}
	f.do(t, http.MethodPost, "/api/friends/"+f.ref("alice", "bob")+"/upgrade", http.StatusNotFound)
	f.do(t, http.MethodPost, "/api/friends/"+f.ref("bob", "carol")+"/upgrade", http.StatusNotFound)
}

// passkey is an enrolled browser: cookie, CSRF token and authenticator.
type passkey struct {
	cookie, csrf string
	auth         *authtest.Authenticator
}

func (f *fixture) enroll(t *testing.T, handle string) *passkey {
	t.Helper()
	a := f.h.App
	ctx := context.Background()
	tok, err := a.Auth.GenesisToken(ctx, handle)
	if err != nil {
		t.Fatal(err)
	}
	p := &passkey{auth: authtest.New(a.Deps().Cfg.Origin)}
	ev, err := a.Auth.EnrollBegin(p.ctx(t, f), tok)
	if err != nil {
		t.Fatal(err)
	}
	p.cookie = ev.Token
	resp, err := p.auth.Create(ev.Options)
	if err != nil {
		t.Fatal(err)
	}
	if p.cookie, _, err = a.Auth.EnrollFinish(p.ctx(t, f), resp, false, a.Deps().Cfg.TOSVersion, true); err != nil {
		t.Fatal(err)
	}
	if p.csrf, err = a.Auth.CSRFForToken(ctx, p.cookie); err != nil {
		t.Fatal(err)
	}
	return p
}

// ctx is the request context the session middleware gives this browser.
func (p *passkey) ctx(t *testing.T, f *fixture) context.Context {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/x", nil)
	if p.cookie != "" {
		req.AddCookie(&http.Cookie{Name: httpx.CookieName(false), Value: p.cookie}) //nolint:gosec // test request
	}
	var got context.Context
	f.h.App.Auth.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = r.Context() })).
		ServeHTTP(httptest.NewRecorder(), req)
	return got
}

// send sends body (a raw JSON string, a value, or nil) as this browser, with
// the first-party headers (no OpenAPI validation).
func (p *passkey) send(f *fixture, method, path string, body any) *httptest.ResponseRecorder {
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			panic(err)
		}
		rd = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Origin", f.h.Origin)
	req.Header.Set("X-PCC-CSRF", p.csrf)
	req.AddCookie(&http.Cookie{Name: httpx.CookieName(strings.HasPrefix(f.h.Origin, "https://")), Value: p.cookie}) //nolint:gosec // test request
	rec := httptest.NewRecorder()
	f.h.Handler.ServeHTTP(rec, req)
	return rec
}

// A passkey session's request needs its co-signature in the body; a malformed
// body is a Problem, a missing or foreign one the uniform not-found.
func TestCosignedRequestOverHTTP(t *testing.T) {
	f := newFixture(t)
	alice, bob := f.enroll(t, "anne"), f.enroll(t, "bert")
	a := f.h.App
	anne, _ := a.Users.ByHandle(context.Background(), "anne")
	bert, _ := a.Users.ByHandle(context.Background(), "bert")
	carol := f.w.ID("carol")
	link := func(x, y uint32) {
		for _, p := range [][2]uint32{{x, y}, {y, x}} {
			if _, err := a.Deps().DB.Q().Exec(context.Background(),
				"INSERT INTO edges (user_id, friend_id, level, since, record_hash) VALUES ($1, $2, 1, now(), '\\x00')",
				int64(p[0]), int64(p[1])); err != nil {
				t.Fatal(err)
			}
		}
	}
	link(uint32(anne), uint32(carol))
	link(uint32(carol), uint32(bert))
	if err := a.Graph.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	ref := string(a.Deps().Refs.User(anne, bert))
	path := "/api/people/" + ref + "/request"

	if rec := alice.send(f, http.MethodPost, path, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unsigned = %d %s", rec.Code, rec.Body)
	}
	for _, bad := range []string{`{"intent": 1}`, `{"intent": "x", "assertion": {}, "extra": true}`, `[]`, `{} {}`} {
		if rec := alice.send(f, http.MethodPost, path, bad); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d %s", bad, rec.Code, rec.Body)
		}
	}
	in, err := a.Cosign.Begin(alice.ctx(t, f), friends.PurposeFriendRequest, gate.Ref(ref))
	if err != nil {
		t.Fatal(err)
	}
	asr, err := alice.auth.Get(in.Options)
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"intent": in.Intent, "assertion": json.RawMessage(asr)}
	if rec := alice.send(f, http.MethodPost, path, body); rec.Code != http.StatusAccepted ||
		rec.Body.String() != `{"state":"outgoing"}`+"\n" {
		t.Fatalf("signed = %d %s", rec.Code, rec.Body)
	}
	// bob accepts with his own co-signature.
	back := string(a.Deps().Refs.User(bert, anne))
	in, err = a.Cosign.Begin(bob.ctx(t, f), friends.PurposeFriendAccept, gate.Ref(back))
	if err != nil {
		t.Fatal(err)
	}
	if asr, err = bob.auth.Get(in.Options); err != nil {
		t.Fatal(err)
	}
	rec := bob.send(f, http.MethodPost, "/api/friends/requests/"+back+"/accept",
		map[string]any{"intent": in.Intent, "assertion": json.RawMessage(asr)})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("accept = %d %s", rec.Code, rec.Body)
	}
	if _, ok := a.Graph.Level(anne, bert); !ok {
		t.Fatal("friends")
	}
	// The fixture's test-hook session still works without a body.
	f.h.Login("alice")
	f.do(t, http.MethodDelete, "/api/friends/"+f.ref("alice", "carol"), http.StatusNoContent)
}
