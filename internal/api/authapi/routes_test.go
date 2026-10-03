package authapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"

	"github.com/Nibroc6/purple-chicken-cult/internal/api/apitest"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/authapi"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/auth/authtest"
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
	h := apitest.Service(t, "auth", authapi.Register)
	doc := apitest.LoadSpec(t, "auth")
	doc.Servers = openapi3.Servers{{URL: "/"}}
	r, err := legacy.NewRouter(doc)
	if err != nil {
		t.Fatal(err)
	}
	return &suite{t: t, h: h, spec: r}
}

func (s *suite) advance(d time.Duration) {
	s.t.Helper()
	if err := s.h.App.Hooks().AdvanceClock(d); err != nil {
		s.t.Fatal(err)
	}
}

// browser is one first-party client with its own cookie jar and CSRF token.
type browser struct {
	s      *suite
	cookie string
	csrf   string
}

func (s *suite) browser() *browser { return &browser{s: s} }

// do sends a first-party request (exact Origin, Sec-Fetch-Site same-origin, the
// cookie and the CSRF token), applies hdr on top, keeps Set-Cookie and
// validates request and response against auth.yaml.
func (b *browser) do(method, path string, body any, hdr map[string]string) *httptest.ResponseRecorder {
	b.s.t.Helper()
	h := map[string]string{"Cookie": "", "X-PCC-CSRF": ""}
	if b.cookie != "" {
		h["Cookie"] = httpx.CookieName(false) + "=" + b.cookie
	}
	if b.csrf != "" {
		h["X-PCC-CSRF"] = b.csrf
	}
	for k, v := range hdr {
		h[k] = v
	}
	rec := b.s.h.Raw(method, path, body, h)
	for _, c := range rec.Result().Cookies() {
		if c.Name != httpx.CookieName(false) {
			continue
		}
		if c.MaxAge < 0 {
			b.cookie = ""
		} else {
			b.cookie = c.Value
		}
	}
	b.s.validate(method, path, body, h, rec)
	return rec
}

func (s *suite) validate(method, path string, body any, hdr map[string]string, rec *httptest.ResponseRecorder) {
	s.t.Helper()
	if !strings.HasPrefix(path, "/api/") || rec.Code == http.StatusNotFound {
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
		s.t.Errorf("%s %s: no operation in auth.yaml (%d)", method, path, rec.Code)
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

type sessionOut struct {
	Authenticated bool   `json:"authenticated"`
	Kind          string `json:"kind"`
	CSRF          string `json:"csrf"`
	Me            *struct {
		Handle string `json:"handle"`
	} `json:"me"`
}

type beginOut struct {
	Options json.RawMessage `json:"options"`
	CSRF    string          `json:"csrf"`
}

type enrollOut struct {
	Options  json.RawMessage `json:"options"`
	CSRF     string          `json:"csrf"`
	Purpose  string          `json:"purpose"`
	Existing []struct {
		ID string `json:"id"`
	} `json:"existing_passkeys"`
}

type passkeysOut struct {
	Items []struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Current bool   `json:"current"`
	} `json:"items"`
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

// enroll runs genesis enrollment over HTTP and returns the signed-in browser.
func (s *suite) enroll(handle string, a *authtest.Authenticator) *browser {
	s.t.Helper()
	tok, err := s.h.App.Hooks().GenesisToken(context.Background(), handle)
	if err != nil {
		s.t.Fatal(err)
	}
	b := s.browser()
	rec := b.do(http.MethodPost, "/api/enroll/begin", map[string]string{"token": tok}, nil)
	want(s.t, rec, http.StatusOK, "")
	ev := decode[enrollOut](s.t, rec)
	if ev.Purpose != "genesis" || ev.CSRF == "" || b.cookie == "" {
		s.t.Fatalf("enroll begin %s", rec.Body)
	}
	b.csrf = ev.CSRF
	cred, err := a.Create(ev.Options)
	if err != nil {
		s.t.Fatal(err)
	}
	rec = b.do(http.MethodPost, "/api/enroll/finish", map[string]any{"credential": json.RawMessage(cred),
		"tos_version": s.h.App.Config().TOSVersion, "age_attested": true}, nil)
	want(s.t, rec, http.StatusOK, "")
	sv := decode[sessionOut](s.t, rec)
	if !sv.Authenticated || sv.Me == nil || sv.Me.Handle != handle {
		s.t.Fatalf("enroll finish %s", rec.Body)
	}
	b.csrf = sv.CSRF
	return b
}

// login runs the passkey login over HTTP in a new browser.
func (s *suite) login(a *authtest.Authenticator) (*browser, *httptest.ResponseRecorder) {
	s.t.Helper()
	b := s.browser()
	rec := b.do(http.MethodPost, "/api/auth/login/begin", map[string]any{}, nil)
	want(s.t, rec, http.StatusOK, "")
	bo := decode[beginOut](s.t, rec)
	b.csrf = bo.CSRF
	cred, err := a.Get(bo.Options)
	if err != nil {
		s.t.Fatal(err)
	}
	rec = b.do(http.MethodPost, "/api/auth/login/finish", map[string]any{"credential": json.RawMessage(cred)}, nil)
	if rec.Code == http.StatusOK {
		b.csrf = decode[sessionOut](s.t, rec).CSRF
	}
	return b, rec
}

func (b *browser) session() sessionOut {
	b.s.t.Helper()
	rec := b.do(http.MethodGet, "/api/session", nil, nil)
	want(b.s.t, rec, http.StatusOK, "")
	return decode[sessionOut](b.s.t, rec)
}

func (b *browser) stepUp(a *authtest.Authenticator) *httptest.ResponseRecorder {
	b.s.t.Helper()
	rec := b.do(http.MethodPost, "/api/auth/stepup/begin", map[string]any{}, nil)
	want(b.s.t, rec, http.StatusOK, "")
	cred, err := a.Get(decode[beginOut](b.s.t, rec).Options)
	if err != nil {
		b.s.t.Fatal(err)
	}
	return b.do(http.MethodPost, "/api/auth/stepup/finish", map[string]any{"credential": json.RawMessage(cred)}, nil)
}

func (b *browser) addPasskey(a *authtest.Authenticator, name string) *httptest.ResponseRecorder {
	b.s.t.Helper()
	rec := b.do(http.MethodPost, "/api/auth/passkeys/begin", map[string]any{}, nil)
	if rec.Code != http.StatusOK {
		return rec
	}
	cred, err := a.Create(decode[beginOut](b.s.t, rec).Options)
	if err != nil {
		b.s.t.Fatal(err)
	}
	return b.do(http.MethodPost, "/api/auth/passkeys/finish", map[string]any{"credential": json.RawMessage(cred), "name": name}, nil)
}

func (b *browser) passkeys() passkeysOut {
	b.s.t.Helper()
	rec := b.do(http.MethodGet, "/api/auth/passkeys", nil, nil)
	want(b.s.t, rec, http.StatusOK, "")
	return decode[passkeysOut](b.s.t, rec)
}

func TestGenesisEnrollThenSessionAuthenticated(t *testing.T) {
	s := newSuite(t)
	a := authtest.New(s.h.Origin)
	b := s.enroll("alice", a)
	sv := b.session()
	if !sv.Authenticated || sv.Kind != "user" || sv.Me.Handle != "alice" || sv.CSRF != b.csrf {
		t.Fatalf("session %+v", sv)
	}
	// Logout ends it and clears the cookie.
	want(t, b.do(http.MethodPost, "/api/auth/logout", nil, nil), http.StatusNoContent, "")
	if b.cookie != "" {
		t.Fatal("logout kept the cookie")
	}
	if sv := b.session(); sv.Authenticated || sv.Kind != "none" {
		t.Fatalf("after logout %+v", sv)
	}
}

func TestCSRFAndOriginFailuresAreTheUniform404(t *testing.T) {
	s := newSuite(t)
	a := authtest.New(s.h.Origin)
	b := s.enroll("alice", a)
	unknown := b.do(http.MethodPost, "/api/definitely/not/here", map[string]any{}, nil)
	wantUniform404(t, unknown, "unknown route")
	for name, hdr := range map[string]map[string]string{
		"missing csrf":    {"X-PCC-CSRF": ""},
		"wrong csrf":      {"X-PCC-CSRF": "AAAAAAAAAAAAAAAAAAAAAA"},
		"other csrf":      {"X-PCC-CSRF": strings.ToUpper(b.csrf)},
		"missing origin":  {"Origin": ""},
		"foreign origin":  {"Origin": "https://evil.example"},
		"origin w/ slash": {"Origin": s.h.Origin + "/"},
		"cross-site":      {"Sec-Fetch-Site": "cross-site"},
		"same-site":       {"Sec-Fetch-Site": "same-site"},
	} {
		rec := b.do(http.MethodPost, "/api/auth/stepup/begin", map[string]any{}, hdr)
		wantUniform404(t, rec, name)
		if !bytes.Equal(rec.Body.Bytes(), unknown.Body.Bytes()) {
			t.Fatalf("%s: body differs from an unknown route", name)
		}
	}
	// The session is intact after the refusals.
	if sv := b.session(); !sv.Authenticated {
		t.Fatal("refusals ended the session")
	}

	// Unauthenticated POSTs need the exact Origin too.
	anon := s.browser()
	for name, hdr := range map[string]map[string]string{
		"login without origin":  {"Origin": ""},
		"login foreign origin":  {"Origin": "http://localhost:8081"},
		"login https origin":    {"Origin": strings.Replace(s.h.Origin, "http://", "https://", 1)},
		"login cross-site":      {"Sec-Fetch-Site": "cross-site"},
		"login null origin":     {"Origin": "null"},
		"login origin w/ slash": {"Origin": s.h.Origin + "/"},
	} {
		wantUniform404(t, anon.do(http.MethodPost, "/api/auth/login/begin", map[string]any{}, hdr), name)
		wantUniform404(t, anon.do(http.MethodPost, "/api/enroll/begin", map[string]any{"token": "x"}, hdr), name+" (enroll)")
	}
	if anon.cookie != "" {
		t.Fatal("a refused login began a session")
	}
	// A guest session's CSRF is required once it exists.
	rec := anon.do(http.MethodPost, "/api/auth/login/begin", map[string]any{}, nil)
	want(t, rec, http.StatusOK, "")
	wantUniform404(t, anon.do(http.MethodPost, "/api/auth/login/finish", map[string]any{"credential": map[string]any{}}, nil),
		"guest without csrf")
}

func TestCrossSiteAPIIsNotFoundButNavigationGetsTheSPA(t *testing.T) {
	s := newSuite(t)
	b := s.browser()
	unknown := b.do(http.MethodGet, "/api/definitely/not/here", nil, nil)
	for _, site := range []string{"cross-site", "same-site", "none"} {
		rec := b.do(http.MethodGet, "/api/session", nil, map[string]string{"Sec-Fetch-Site": site})
		wantUniform404(t, rec, site)
		if !bytes.Equal(rec.Body.Bytes(), unknown.Body.Bytes()) {
			t.Fatalf("%s: body differs", site)
		}
	}
	for _, site := range []string{"cross-site", "none"} {
		rec := b.do(http.MethodGet, "/", nil, map[string]string{"Sec-Fetch-Site": site, "Sec-Fetch-Mode": "navigate",
			"Sec-Fetch-Dest": "document", "Origin": ""})
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `<div id="root">`) {
			t.Fatalf("%s navigation: %d %s", site, rec.Code, rec.Body)
		}
	}
}

func TestLoginRotatesTheSessionAndCannotBeReplayed(t *testing.T) {
	s := newSuite(t)
	a := authtest.New(s.h.Origin)
	s.enroll("alice", a)

	b := s.browser()
	rec := b.do(http.MethodPost, "/api/auth/login/begin", map[string]any{}, nil)
	want(t, rec, http.StatusOK, "")
	guestCookie := b.cookie
	if guestCookie == "" || !strings.Contains(rec.Header().Get("Set-Cookie"), "SameSite=Strict") ||
		!strings.Contains(rec.Header().Get("Set-Cookie"), "HttpOnly") {
		t.Fatalf("guest cookie %q", rec.Header().Get("Set-Cookie"))
	}
	bo := decode[beginOut](t, rec)
	var opts struct {
		Mediation string `json:"mediation"`
		PublicKey struct {
			UserVerification string `json:"userVerification"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(bo.Options, &opts); err != nil || opts.Mediation != "conditional" ||
		opts.PublicKey.UserVerification != "required" {
		t.Fatalf("login options %s", bo.Options)
	}
	b.csrf = bo.CSRF
	if sv := b.session(); sv.Kind != "guest" || sv.CSRF != bo.CSRF {
		t.Fatalf("guest session %+v", sv)
	}
	cred, err := a.Get(bo.Options)
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"credential": json.RawMessage(cred)}
	rec = b.do(http.MethodPost, "/api/auth/login/finish", body, nil)
	want(t, rec, http.StatusOK, "")
	setCookie := rec.Header().Get("Set-Cookie")
	for _, attr := range []string{"SameSite=Strict", "HttpOnly", "Path=/", "Max-Age="} {
		if !strings.Contains(setCookie, attr) {
			t.Fatalf("user cookie %q lacks %s", setCookie, attr)
		}
	}
	if strings.Contains(strings.ToLower(setCookie), "domain=") {
		t.Fatalf("user cookie %q has a Domain", setCookie)
	}
	sv := decode[sessionOut](t, rec)
	if b.cookie == guestCookie || sv.CSRF == bo.CSRF || !sv.Authenticated {
		t.Fatal("login did not rotate the session (fixation)")
	}
	userCookie := b.cookie
	b.csrf = sv.CSRF
	if got := b.session(); got.Me == nil || got.Me.Handle != "alice" {
		t.Fatalf("user session %+v", got)
	}

	// The old guest token is dead.
	old := &browser{s: s, cookie: guestCookie, csrf: bo.CSRF}
	if got := old.session(); got.Kind != "none" {
		t.Fatalf("guest token still live: %+v", got)
	}
	// Replaying the finish: from the dead guest session, and from the user session.
	wantUniform404(t, old.do(http.MethodPost, "/api/auth/login/finish", body, nil), "replay from guest token")
	wantUniform404(t, b.do(http.MethodPost, "/api/auth/login/finish", body, nil), "replay from user session")
	// Replaying into a new ceremony.
	n := s.browser()
	want(t, n.do(http.MethodPost, "/api/auth/login/begin", map[string]any{}, nil), http.StatusOK, "")
	n.csrf = decode[beginOut](t, n.do(http.MethodGet, "/api/session", nil, nil)).CSRF
	wantUniform404(t, n.do(http.MethodPost, "/api/auth/login/finish", body, nil), "replay into a new ceremony")
	if b.cookie != userCookie {
		t.Fatal("replay touched the user session")
	}
}

func TestStepUpPasskeysAndRemoval(t *testing.T) {
	s := newSuite(t)
	a, other := authtest.New(s.h.Origin), authtest.New(s.h.Origin)
	b := s.enroll("alice", a)
	s.enroll("bob", other)
	s.advance(6 * time.Minute)

	// Passkey add/remove without a fresh step-up.
	want(t, b.addPasskey(authtest.New(s.h.Origin), "x"), http.StatusUnauthorized, "step_up_required")
	list := b.passkeys()
	if len(list.Items) != 1 || !list.Items[0].Current {
		t.Fatalf("passkeys %+v", list)
	}
	want(t, b.do(http.MethodDelete, "/api/auth/passkeys/"+list.Items[0].ID, nil, nil), http.StatusUnauthorized, "step_up_required")

	// Step-up with another account's passkey is rejected.
	rec := b.do(http.MethodPost, "/api/auth/stepup/begin", map[string]any{}, nil)
	want(t, rec, http.StatusOK, "")
	cred, err := other.GetWith(decode[beginOut](t, rec).Options, other.Credentials()[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	want(t, b.do(http.MethodPost, "/api/auth/stepup/finish", map[string]any{"credential": json.RawMessage(cred)}, nil),
		http.StatusBadRequest, "passkey_rejected")
	want(t, b.addPasskey(authtest.New(s.h.Origin), "x"), http.StatusUnauthorized, "step_up_required")

	// A real step-up, then the last passkey cannot be removed.
	want(t, b.stepUp(a), http.StatusNoContent, "")
	want(t, b.do(http.MethodDelete, "/api/auth/passkeys/"+list.Items[0].ID, nil, nil), http.StatusConflict, "last_passkey")

	// Add a second passkey and sign in with it elsewhere.
	a2 := authtest.New(s.h.Origin)
	want(t, b.addPasskey(a2, "Laptop"), http.StatusNoContent, "")
	c, rec := s.login(a2)
	want(t, rec, http.StatusOK, "")
	d, rec := s.login(a)
	want(t, rec, http.StatusOK, "")
	list = c.passkeys()
	if len(list.Items) != 2 || list.Items[1].Name != "Laptop" || !list.Items[1].Current || list.Items[0].Current {
		t.Fatalf("passkeys from c %+v", list)
	}
	// Removing the first passkey kills every session it authenticated (b, d).
	want(t, c.do(http.MethodDelete, "/api/auth/passkeys/"+list.Items[0].ID, nil, nil), http.StatusNoContent, "")
	for name, x := range map[string]*browser{"enrolled": b, "login": d} {
		if sv := x.session(); sv.Authenticated {
			t.Fatalf("%s session survived the removal of its credential", name)
		}
		wantUniform404(t, x.do(http.MethodGet, "/api/auth/passkeys", nil, nil), name)
	}
	if sv := c.session(); !sv.Authenticated {
		t.Fatal("unrelated session ended")
	}
	_, rec = s.login(a)
	wantUniform404(t, rec, "removed passkey")
	// A ref of someone else's passkey is the uniform 404.
	o, rec := s.login(other)
	want(t, rec, http.StatusOK, "")
	want(t, o.stepUp(other), http.StatusNoContent, "")
	wantUniform404(t, o.do(http.MethodDelete, "/api/auth/passkeys/"+list.Items[1].ID, nil, nil), "foreign ref")
}

func TestUnauthenticatedSurfaces(t *testing.T) {
	s := newSuite(t)
	b := s.browser()
	unknown := b.do(http.MethodGet, "/api/definitely/not/here", nil, nil)
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/auth/passkeys"}, {http.MethodPost, "/api/auth/stepup/begin"},
		{http.MethodPost, "/api/auth/passkeys/begin"}, {http.MethodDelete, "/api/auth/passkeys/abc"},
	} {
		var body any
		if c.method == http.MethodPost {
			body = map[string]any{}
		}
		rec := b.do(c.method, c.path, body, nil)
		if !bytes.Equal(rec.Body.Bytes(), unknown.Body.Bytes()) || rec.Code != http.StatusNotFound {
			t.Fatalf("%s %s: %d %s", c.method, c.path, rec.Code, rec.Body)
		}
	}
	// Logout without a session is a no-op.
	want(t, b.do(http.MethodPost, "/api/auth/logout", nil, nil), http.StatusNoContent, "")
	if sv := b.session(); sv.Kind != "none" || sv.Authenticated {
		t.Fatalf("anonymous session %+v", sv)
	}
}

// logCapture collects every slog record written while it is installed.
type logCapture struct {
	buf bytes.Buffer
}

func captureLogs(t *testing.T) *logCapture {
	t.Helper()
	c := &logCapture{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&c.buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return c
}

func TestEnrollmentTokenNeverInARequestLineOrLog(t *testing.T) {
	logs := captureLogs(t)
	s := newSuite(t)
	tok, err := s.h.App.Hooks().GenesisToken(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	b := s.browser()
	// The SPA page itself: the token stays in the fragment, which browsers never send.
	nav := b.do(http.MethodGet, "/enroll", nil, map[string]string{"Sec-Fetch-Site": "none", "Sec-Fetch-Mode": "navigate"})
	if nav.Code != http.StatusOK {
		t.Fatalf("enroll page %d", nav.Code)
	}
	lines = append(lines, "GET /enroll")
	a := authtest.New(s.h.Origin)
	// A failed attempt (bad token) logs nothing about the token either.
	wantUniform404(t, b.do(http.MethodPost, "/api/enroll/begin", map[string]string{"token": tok[:20]}, nil), "bad token")
	rec := b.do(http.MethodPost, "/api/enroll/begin", map[string]string{"token": tok}, nil)
	lines = append(lines, "POST /api/enroll/begin")
	want(t, rec, http.StatusOK, "")
	ev := decode[enrollOut](t, rec)
	b.csrf = ev.CSRF
	cred, err := a.Create(ev.Options)
	if err != nil {
		t.Fatal(err)
	}
	// Wrong terms first (a Problem), then the real finish.
	want(t, b.do(http.MethodPost, "/api/enroll/finish", map[string]any{"credential": json.RawMessage(cred),
		"tos_version": 0, "age_attested": true}, nil), http.StatusUnprocessableEntity, "tos_required")
	rec = b.do(http.MethodPost, "/api/enroll/finish", map[string]any{"credential": json.RawMessage(cred),
		"tos_version": s.h.App.Config().TOSVersion, "age_attested": true}, nil)
	lines = append(lines, "POST /api/enroll/finish")
	want(t, rec, http.StatusOK, "")
	b.csrf = decode[sessionOut](t, rec).CSRF
	if sv := b.session(); !sv.Authenticated || sv.Me.Handle != "alice" {
		t.Fatalf("session after enrollment %+v", sv)
	}
	if logs.buf.Len() == 0 || !strings.Contains(logs.buf.String(), `"route"`) {
		t.Fatal("no access log captured")
	}
	for _, needle := range []string{tok, tok[:20]} {
		if strings.Contains(logs.buf.String(), needle) {
			t.Fatal("the enrollment token appears in the logs")
		}
		for _, l := range lines {
			if strings.Contains(l, needle) {
				t.Fatal("the enrollment token appears in a request line")
			}
		}
	}
	// Used: a second redemption is the uniform 404.
	wantUniform404(t, s.browser().do(http.MethodPost, "/api/enroll/begin", map[string]string{"token": tok}, nil), "reused token")
}
