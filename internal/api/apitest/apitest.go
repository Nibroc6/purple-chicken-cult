// Package apitest is the HTTP test harness (plan §4.6): Service mounts ONE
// module's routes over a full wiretest App (what a WP's handler tests use) and App
// mounts everything (INT tests). Both validate every request and response against
// the module's OpenAPI fragment(s) in api/openapi (external refs into
// _common.yaml resolved by kin-openapi; no Node step).
//
// Owner: SCAFFOLD.
package apitest

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"
	"github.com/go-chi/chi/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/api"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/router"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire/wiretest"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/httpx"
)

// Harness drives the HTTP stack.
type Harness struct {
	T       testing.TB
	App     *wire.App
	Handler http.Handler
	Origin  string

	specs  []routers.Router
	cookie string
	csrf   string
}

// OpenAPIDir returns the absolute path of api/openapi.
func OpenAPIDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "api", "openapi")
}

// LoadSpec loads api/openapi/<module>.yaml (with refs into _common.yaml).
func LoadSpec(t testing.TB, module string) *openapi3.T {
	t.Helper()
	l := openapi3.NewLoader()
	l.IsExternalRefsAllowed = true
	doc, err := l.LoadFromFile(filepath.Join(OpenAPIDir(), module+".yaml"))
	if err != nil {
		t.Fatalf("apitest: load %s.yaml: %v", module, err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("apitest: invalid %s.yaml: %v", module, err)
	}
	return doc
}

func newRouter(t testing.TB, doc *openapi3.T) routers.Router {
	t.Helper()
	doc.Servers = openapi3.Servers{{URL: "/"}}
	r, err := legacy.NewRouter(doc)
	if err != nil {
		t.Fatalf("apitest: router: %v", err)
	}
	return r
}

func newHarness(t testing.TB, modules []string, register ...func(chi.Router, *api.Deps)) *Harness {
	t.Helper()
	a := wiretest.NewApp(t)
	cfg := a.Config()
	d := api.FromApp(a, cfg, slog.New(slog.DiscardHandler), fstest.MapFS{"index.html": {Data: []byte(TestIndexHTML)}})
	h := &Harness{T: t, App: a, Handler: router.NewWith(d, register...), Origin: cfg.Origin}
	for _, m := range modules {
		h.specs = append(h.specs, newRouter(t, LoadSpec(t, m)))
	}
	return h
}

// Service mounts one module (register) and validates against api/openapi/<module>.yaml.
func Service(t testing.TB, module string, register func(chi.Router, *api.Deps)) *Harness {
	return newHarness(t, []string{module}, register)
}

// App mounts every module and validates against every fragment.
func App(t testing.TB) *Harness {
	entries, err := os.ReadDir(OpenAPIDir())
	if err != nil {
		t.Fatal(err)
	}
	var mods []string
	for _, e := range entries {
		n := e.Name()
		if strings.HasSuffix(n, ".yaml") && !strings.HasPrefix(n, "_") {
			mods = append(mods, strings.TrimSuffix(n, ".yaml"))
		}
	}
	return newHarness(t, mods, router.Modules...)
}

// Login signs in as handle (creating a genesis user if needed).
func (h *Harness) Login(handle string) {
	h.cookie, h.csrf = wiretest.Login(h.T, h.App, handle)
}

// Logout forgets the session.
func (h *Harness) Logout() { h.cookie, h.csrf = "", "" }

// Raw performs a request without validation, applying hdr on top of the
// first-party defaults (Origin, Sec-Fetch-Site, cookie, CSRF).
func (h *Harness) Raw(method, path string, body any, hdr map[string]string) *httptest.ResponseRecorder {
	h.T.Helper()
	req := h.request(method, path, body)
	for k, v := range hdr {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	rec := httptest.NewRecorder()
	h.Handler.ServeHTTP(rec, req)
	return rec
}

func (h *Harness) request(method, path string, body any) *http.Request {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			h.T.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if method != http.MethodGet && method != http.MethodHead {
		req.Header.Set("Origin", h.Origin)
	}
	if h.cookie != "" {
		req.AddCookie(&http.Cookie{ //nolint:gosec // request cookie in a test harness
			Name: httpx.CookieName(strings.HasPrefix(h.Origin, "https://")), Value: h.cookie})
		req.Header.Set("X-PCC-CSRF", h.csrf)
	}
	return req
}

// Do performs a first-party request and validates request and response against
// the OpenAPI fragment(s). Paths under /api/_test/ are not validated.
func (h *Harness) Do(method, path string, body any) *httptest.ResponseRecorder {
	h.T.Helper()
	req := h.request(method, path, body)
	var reqBody []byte
	if req.Body != nil {
		reqBody, _ = io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewReader(reqBody))
	}
	rec := httptest.NewRecorder()
	h.Handler.ServeHTTP(rec, req)
	if !strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/api/_test/") {
		return rec
	}
	h.validate(method, path, reqBody, req.Header, rec)
	return rec
}

func (h *Harness) validate(method, path string, reqBody []byte, hdr http.Header, rec *httptest.ResponseRecorder) {
	h.T.Helper()
	vreq := httptest.NewRequest(method, strings.TrimPrefix(path, "/api"), bytes.NewReader(reqBody))
	vreq.Header = hdr.Clone()
	ctx := context.Background()
	for _, r := range h.specs {
		route, params, err := r.FindRoute(vreq)
		if err != nil {
			continue
		}
		in := &openapi3filter.RequestValidationInput{Request: vreq, PathParams: params, Route: route,
			Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc}}
		if rec.Code < 400 {
			if err := openapi3filter.ValidateRequest(ctx, in); err != nil {
				h.T.Errorf("apitest: %s %s request does not match the spec: %v", method, path, err)
			}
		}
		out := &openapi3filter.ResponseValidationInput{RequestValidationInput: in, Status: rec.Code,
			Header: rec.Header(), Body: io.NopCloser(bytes.NewReader(rec.Body.Bytes()))}
		if err := openapi3filter.ValidateResponse(ctx, out); err != nil {
			h.T.Errorf("apitest: %s %s response %d does not match the spec: %v", method, path, rec.Code, err)
		}
		return
	}
	if rec.Code != http.StatusNotFound {
		h.T.Errorf("apitest: %s %s answered %d but has no OpenAPI operation", method, path, rec.Code)
	}
}

// TestIndexHTML is the SPA shell served by harnesses.
const TestIndexHTML = `<!doctype html><html><head><meta charset="utf-8"></head><body><div id="root"></div></body></html>`
