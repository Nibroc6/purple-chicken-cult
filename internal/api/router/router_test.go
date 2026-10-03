package router_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/api/apitest"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/apix"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/router"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
)

// requireAll is flipped to true by INT-0: then every catalogue route must be registered.
const requireAll = false

func TestCatalogueHasNoDuplicates(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range router.Catalogue {
		k := e.Method + " " + e.Pattern
		if seen[k] {
			t.Errorf("duplicate catalogue entry %s", k)
		}
		seen[k] = true
		if e.Class.Kind == 0 || e.Class.Name == "" {
			t.Errorf("%s has no class", k)
		}
		for _, seg := range strings.Split(e.Pattern, "/") {
			if strings.HasPrefix(seg, "{") {
				switch seg {
				case "{ref}", "{hs}", "{id}", "{snapshot}", "{key}":
				default:
					t.Errorf("%s uses a non-standard path parameter %s", k, seg)
				}
			}
		}
	}
}

func TestRegisteredRoutesMatchCatalogue(t *testing.T) {
	h := apitest.App(t)
	cat := map[string]router.Entry{}
	for _, e := range router.Catalogue {
		cat[e.Method+" /api"+e.Pattern] = e
	}
	classes := map[string]apix.Class{}
	for _, ri := range apix.Routes() {
		classes[ri.Method+" /api"+ri.Pattern] = ri.Class
	}
	registered := map[string]bool{}
	err := chi.Walk(h.Handler.(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if !strings.HasPrefix(route, "/api/") || route == "/api/*" {
			return nil
		}
		k := method + " " + route
		registered[k] = true
		e, ok := cat[k]
		if !ok {
			t.Errorf("route %s is not in the catalogue (plan §5.3; ask the contract steward)", k)
			return nil
		}
		c, ok := classes[k]
		if !ok {
			t.Errorf("route %s was not registered through apix.Route (no charge class, archtest A12)", k)
		} else if c != e.Class {
			t.Errorf("route %s has class %+v, catalogue says %+v", k, c, e.Class)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	missing := 0
	for k := range cat {
		if !registered[k] {
			missing++
			if requireAll {
				t.Errorf("catalogue route %s is not registered", k)
			}
		}
	}
	t.Logf("%d of %d catalogue routes registered (%d pending work packages)", len(cat)-missing, len(cat), missing)
}

func TestUniformNotFound(t *testing.T) {
	h := apitest.App(t)
	h.Login("alice")
	cases := map[string]func() []byte{
		"unknown GET": func() []byte { return h.Raw("GET", "/api/definitely/not/here", nil, nil).Body.Bytes() },
		"unknown POST": func() []byte {
			return h.Raw("POST", "/api/definitely/not/here", map[string]string{}, nil).Body.Bytes()
		},
		"wrong method": func() []byte { return h.Raw("DELETE", "/api/session", nil, nil).Body.Bytes() },
		"cross-site": func() []byte {
			return h.Raw("GET", "/api/session", nil, map[string]string{"Sec-Fetch-Site": "cross-site"}).Body.Bytes()
		},
		"bad csrf": func() []byte {
			return h.Raw("POST", "/api/auth/logout", nil, map[string]string{"X-PCC-CSRF": "nope"}).Body.Bytes()
		},
		"no csrf": func() []byte {
			return h.Raw("POST", "/api/auth/logout", nil, map[string]string{"X-PCC-CSRF": ""}).Body.Bytes()
		},
		"wrong origin": func() []byte {
			return h.Raw("POST", "/api/auth/logout", nil, map[string]string{"Origin": "https://evil.example"}).Body.Bytes()
		},
		"no origin": func() []byte {
			return h.Raw("POST", "/api/auth/logout", nil, map[string]string{"Origin": ""}).Body.Bytes()
		},
	}
	for name, f := range cases {
		if got := string(f()); got != gate.NotFoundJSON {
			t.Errorf("%s: body %q, want the uniform %q", name, got, gate.NotFoundJSON)
		}
	}
	rec := h.Raw("GET", "/api/definitely/not/here", nil, nil)
	if rec.Code != http.StatusNotFound || rec.Header().Get("tdm-reservation") != "1" {
		t.Errorf("unknown route: status %d, tdm %q", rec.Code, rec.Header().Get("tdm-reservation"))
	}
}

func TestSessionAndLogout(t *testing.T) {
	h := apitest.App(t)
	var sv gate.SessionView
	rec := h.Do("GET", "/api/session", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &sv); err != nil || sv.Authenticated || sv.Kind != "none" {
		t.Fatalf("anonymous session: %d %s", rec.Code, rec.Body)
	}
	h.Login("alice")
	rec = h.Do("GET", "/api/session", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &sv); err != nil || !sv.Authenticated || sv.Me == nil || sv.Me.Handle != "alice" || sv.CSRF == "" {
		t.Fatalf("signed-in session: %d %s", rec.Code, rec.Body)
	}
	if sv.Me.Inviter.Kind != "genesis" {
		t.Errorf("inviter label %+v, want genesis", sv.Me.Inviter)
	}
	if rec := h.Do("POST", "/api/auth/logout", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("logout: %d %s", rec.Code, rec.Body)
	}
	rec = h.Do("GET", "/api/session", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &sv); err != nil || sv.Authenticated {
		t.Fatalf("after logout: %s", rec.Body)
	}
}

func TestLoginBeginStartsAGuestCeremony(t *testing.T) {
	h := apitest.App(t)
	rec := h.Do("POST", "/api/auth/login/begin", map[string]any{})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"options"`) ||
		!strings.Contains(rec.Header().Get("Set-Cookie"), "SameSite=Strict") {
		t.Fatalf("login begin: %d %s", rec.Code, rec.Body)
	}
}

func TestStaticAndHeaders(t *testing.T) {
	h := apitest.App(t)
	rec := h.Raw("GET", "/robots.txt", nil, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "User-agent: GPTBot\nDisallow: /") {
		t.Errorf("robots.txt: %d %q", rec.Code, rec.Body)
	}
	for _, p := range []string{"/", "/feed", "/p/abc"} {
		rec := h.Raw("GET", p, nil, map[string]string{"Sec-Fetch-Site": "cross-site"})
		if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
			t.Errorf("SPA %s: %d %s", p, rec.Code, rec.Header().Get("Content-Type"))
		}
		for _, hdr := range []string{"Content-Security-Policy", "X-Frame-Options", "tdm-reservation", "X-Robots-Tag"} {
			if rec.Header().Get(hdr) == "" {
				t.Errorf("SPA %s: missing %s", p, hdr)
			}
		}
	}
}
