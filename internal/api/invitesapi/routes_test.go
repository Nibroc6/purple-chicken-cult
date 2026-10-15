package invitesapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"

	"github.com/Nibroc6/purple-chicken-cult/internal/api/apitest"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/invitesapi"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/auth/authtest"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire/wiretest"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/httpx"
)

const notFoundBody = "{\"error\":\"not_found\"}\n"

type suite struct {
	t    *testing.T
	h    *apitest.Harness
	spec routers.Router
}

func newSuite(t *testing.T) *suite {
	t.Helper()
	h := apitest.Service(t, "invites", invitesapi.Register)
	doc := apitest.LoadSpec(t, "invites")
	doc.Servers = openapi3.Servers{{URL: "/"}}
	r, err := legacy.NewRouter(doc)
	if err != nil {
		t.Fatal(err)
	}
	return &suite{t: t, h: h, spec: r}
}

// browser is one first-party client with its own cookie and CSRF token.
type browser struct {
	s      *suite
	cookie string
	csrf   string
}

func (s *suite) browser() *browser { return &browser{s: s} }

// user is a signed-in genesis account's browser.
func (s *suite) user(handle string) *browser {
	tok, csrf := wiretest.Login(s.t, s.h.App, handle)
	return &browser{s: s, cookie: tok, csrf: csrf}
}

func (b *browser) do(method, path string, body any) *httptest.ResponseRecorder {
	b.s.t.Helper()
	h := map[string]string{"Cookie": "", "X-PCC-CSRF": b.csrf}
	if b.cookie != "" {
		h["Cookie"] = httpx.CookieName(false) + "=" + b.cookie
	}
	rec := b.s.h.Raw(method, path, body, h)
	for _, c := range rec.Result().Cookies() {
		if c.Name == httpx.CookieName(false) {
			b.cookie = c.Value
		}
	}
	b.s.validate(method, path, body, rec)
	return rec
}

func (s *suite) validate(method, path string, body any, rec *httptest.ResponseRecorder) {
	s.t.Helper()
	if rec.Code == http.StatusNotFound {
		return
	}
	var raw []byte
	if body != nil {
		var err error
		if raw, err = json.Marshal(body); err != nil {
			s.t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, strings.TrimPrefix(path, "/api"), bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	route, params, err := s.spec.FindRoute(req)
	if err != nil {
		s.t.Errorf("%s %s: no operation in invites.yaml (%d)", method, path, rec.Code)
		return
	}
	in := &openapi3filter.RequestValidationInput{Request: req, PathParams: params, Route: route,
		Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc}}
	ctx := context.Background()
	if rec.Code < 400 {
		if err := openapi3filter.ValidateRequest(ctx, in); err != nil {
			s.t.Errorf("%s %s request: %v", method, path, err)
		}
	}
	out := &openapi3filter.ResponseValidationInput{RequestValidationInput: in, Status: rec.Code, Header: rec.Header(),
		Body: io.NopCloser(bytes.NewReader(rec.Body.Bytes()))}
	if err := openapi3filter.ValidateResponse(ctx, out); err != nil {
		s.t.Errorf("%s %s response %d: %v", method, path, rec.Code, err)
	}
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %s: %v", rec.Body, err)
	}
	return v
}

func want(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status %d, want %d: %s", rec.Code, status, rec.Body)
	}
	if code != "" && !strings.Contains(rec.Body.String(), `"error":"`+code+`"`) {
		t.Fatalf("body %s, want %s", rec.Body, code)
	}
}

func wantUniform404(t *testing.T, rec *httptest.ResponseRecorder, what string) {
	t.Helper()
	if rec.Code != http.StatusNotFound || rec.Body.String() != notFoundBody ||
		rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("%s: %d %q %q", what, rec.Code, rec.Body, rec.Header().Get("Content-Type"))
	}
}

type handshakeOut struct {
	HS         string `json:"hs"`
	Mode       string `json:"mode"`
	FrameTTLms int    `json:"frame_ttl_ms"`
}

type statusOut struct {
	State string `json:"state"`
	SAS   string `json:"sas"`
}

type startOut struct {
	CSRF       string `json:"csrf"`
	TOSVersion int    `json:"tos_version"`
	MinAge     int    `json:"min_age"`
}

// joiner starts a join flow in a fresh browser.
func (s *suite) joiner() *browser {
	s.t.Helper()
	b := s.browser()
	rec := b.do(http.MethodPost, "/api/join/start", map[string]any{})
	want(s.t, rec, http.StatusOK, "")
	st := decode[startOut](s.t, rec)
	if st.CSRF == "" || b.cookie == "" || st.TOSVersion != s.h.App.Config().TOSVersion || st.MinAge != s.h.App.Config().MinAge {
		s.t.Fatalf("join start %s", rec.Body)
	}
	b.csrf = st.CSRF
	return b
}

// open opens a handshake and returns its ref and current frame payload.
func (b *browser) open() (string, string) {
	b.s.t.Helper()
	rec := b.do(http.MethodPost, "/api/invites/handshakes", map[string]any{"mode": "qr"})
	want(b.s.t, rec, http.StatusCreated, "")
	hv := decode[handshakeOut](b.s.t, rec)
	if hv.Mode != "qr" || hv.FrameTTLms != 10000 {
		b.s.t.Fatalf("handshake %s", rec.Body)
	}
	rec = b.do(http.MethodGet, "/api/invites/handshakes/"+hv.HS+"/frame", nil)
	want(b.s.t, rec, http.StatusOK, "")
	return hv.HS, decode[struct {
		Payload string `json:"payload"`
	}](b.s.t, rec).Payload
}

func (s *suite) form(handle string) map[string]any {
	return map[string]any{"handle": handle, "display_name": "Newbie", "locale": "en", "languages": []string{"en"},
		"tos_version": s.h.App.Config().TOSVersion, "age_attested": true}
}

func TestInviteAndJoinOverHTTP(t *testing.T) {
	s := newSuite(t)
	alice := s.user("alice")
	rec := alice.do(http.MethodGet, "/api/invites/budget", nil)
	want(t, rec, http.StatusOK, "")
	if b := decode[struct {
		Available int `json:"available"`
		Cap       int `json:"cap"`
	}](t, rec); b.Available != 5 || b.Cap != 5 {
		t.Fatalf("budget %s", rec.Body)
	}
	rec = alice.do(http.MethodPost, "/api/invites/handshakes", map[string]any{"mode": "dual_qr"})
	want(t, rec, http.StatusUnprocessableEntity, "mode_unsupported")

	hs, payload := alice.open()
	g := s.joiner()
	rec = g.do(http.MethodPost, "/api/join/register/begin", s.form("newbie"))
	wantUniform404(t, rec, "register before claim")
	rec = g.do(http.MethodPost, "/api/join/claim", map[string]any{"payload": payload})
	want(t, rec, http.StatusNoContent, "")

	rec = alice.do(http.MethodGet, "/api/invites/handshakes/"+hs, nil)
	want(t, rec, http.StatusOK, "")
	inv := decode[statusOut](t, rec)
	rec = g.do(http.MethodGet, "/api/join/status", nil)
	want(t, rec, http.StatusOK, "")
	mine := decode[statusOut](t, rec)
	if inv.State != "claimed" || inv.SAS == "" || inv.SAS != mine.SAS {
		t.Fatalf("inviter %+v invitee %+v", inv, mine)
	}
	rec = g.do(http.MethodPost, "/api/join/register/begin", s.form("newbie"))
	wantUniform404(t, rec, "register before confirm")
	rec = alice.do(http.MethodPost, "/api/invites/handshakes/"+hs+"/confirm", nil)
	want(t, rec, http.StatusNoContent, "")

	f := s.form("alice")
	rec = g.do(http.MethodPost, "/api/join/register/begin", f)
	want(t, rec, http.StatusConflict, "handle_taken")
	f = s.form("newbie")
	f["age_attested"] = false
	rec = g.do(http.MethodPost, "/api/join/register/begin", f)
	want(t, rec, http.StatusUnprocessableEntity, "age_required")

	rec = g.do(http.MethodPost, "/api/join/register/begin", s.form("newbie"))
	want(t, rec, http.StatusOK, "")
	opts := decode[struct {
		Options json.RawMessage `json:"options"`
	}](t, rec).Options
	a := authtest.New(s.h.App.Config().Origin)
	cred, err := a.Create(opts)
	if err != nil {
		t.Fatal(err)
	}
	guestCookie := g.cookie
	rec = g.do(http.MethodPost, "/api/join/register/finish", map[string]any{"credential": json.RawMessage(cred)})
	want(t, rec, http.StatusOK, "")
	sv := decode[struct {
		Authenticated bool   `json:"authenticated"`
		Kind          string `json:"kind"`
		CSRF          string `json:"csrf"`
		Me            struct {
			Handle  string `json:"handle"`
			Inviter struct {
				Kind   string `json:"kind"`
				Handle string `json:"handle"`
			} `json:"inviter"`
		} `json:"me"`
	}](t, rec)
	if !sv.Authenticated || sv.Kind != "user" || sv.CSRF == "" || g.cookie == guestCookie ||
		sv.Me.Handle != "newbie" || sv.Me.Inviter.Handle != "alice" {
		t.Fatalf("session %s", rec.Body)
	}
	g.csrf = sv.CSRF

	// The new account is signed in, on probation, and appears in alice's list.
	rec = g.do(http.MethodGet, "/api/invites/budget", nil)
	want(t, rec, http.StatusOK, "")
	if !strings.Contains(rec.Body.String(), `"probation"`) {
		t.Fatalf("invitee budget %s", rec.Body)
	}
	rec = g.do(http.MethodPost, "/api/invites/handshakes", map[string]any{"mode": "qr"})
	want(t, rec, http.StatusConflict, "on_probation")
	rec = alice.do(http.MethodGet, "/api/invites/invitees", nil)
	want(t, rec, http.StatusOK, "")
	if !strings.Contains(rec.Body.String(), `"handle":"newbie"`) || !strings.Contains(rec.Body.String(), `"on_probation":true`) {
		t.Fatalf("invitees %s", rec.Body)
	}
	rec = alice.do(http.MethodGet, "/api/invites/handshakes/"+hs, nil)
	if decode[statusOut](t, rec).State != "done" {
		t.Fatalf("final status %s", rec.Body)
	}
	// Replaying the finished handshake is the uniform 404.
	rec = s.joiner().do(http.MethodPost, "/api/join/claim", map[string]any{"payload": payload})
	wantUniform404(t, rec, "reused handshake")
}

func TestClaimFailuresAreByteIdentical(t *testing.T) {
	s := newSuite(t)
	alice := s.user("alice")
	hs, payload := alice.open()
	parts := strings.Split(payload, ".")
	forged := strings.Join(append(parts[:3:3], "AAAAAAAAAAAAAA"), ".")
	if err := s.h.App.Hooks().AdvanceClock(20_000_000_000); err != nil { // two windows
		t.Fatal(err)
	}
	g := s.joiner()
	for name, p := range map[string]string{"stale": payload, "forged": forged, "garbage": "pcc1.x.1.y", "empty": ""} {
		wantUniform404(t, g.do(http.MethodPost, "/api/join/claim", map[string]any{"payload": p}), name)
	}
	// Wrong session: a signed-in user cannot claim, and an anonymous request has no session.
	_, fresh := alice.open()
	wantUniform404(t, alice.do(http.MethodPost, "/api/join/claim", map[string]any{"payload": fresh}), "user session")
	wantUniform404(t, s.browser().do(http.MethodGet, "/api/join/status", nil), "no session")
	// Someone else's handshake, and the anonymous inviter surface.
	bob := s.user("bob")
	wantUniform404(t, bob.do(http.MethodGet, "/api/invites/handshakes/"+hs, nil), "foreign handshake")
	wantUniform404(t, bob.do(http.MethodGet, "/api/invites/handshakes/"+hs+"/frame", nil), "foreign frame")
	wantUniform404(t, s.browser().do(http.MethodGet, "/api/invites/budget", nil), "anonymous budget")
	wantUniform404(t, s.browser().do(http.MethodGet, "/api/invites/invitees", nil), "anonymous invitees")
}

func TestCancelEndsTheHandshake(t *testing.T) {
	s := newSuite(t)
	alice := s.user("alice")
	hs, payload := alice.open()
	g := s.joiner()
	want(t, g.do(http.MethodPost, "/api/join/claim", map[string]any{"payload": payload}), http.StatusNoContent, "")
	want(t, alice.do(http.MethodDelete, "/api/invites/handshakes/"+hs, nil), http.StatusNoContent, "")
	rec := g.do(http.MethodGet, "/api/join/status", nil)
	want(t, rec, http.StatusOK, "")
	if st := decode[statusOut](t, rec); st.State != "cancelled" || st.SAS != "" {
		t.Fatalf("status %s", rec.Body)
	}
	wantUniform404(t, alice.do(http.MethodPost, "/api/invites/handshakes/"+hs+"/confirm", nil), "confirm cancelled")
	wantUniform404(t, g.do(http.MethodPost, "/api/join/register/begin", s.form("late")), "register cancelled")
}
