package secheaders

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serve(o Options, h http.Handler, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	Middleware(o)(h).ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

func TestHeadersOnEveryResponse(t *testing.T) {
	o := Options{Origin: "https://pcc.example", Prod: true}
	handlers := map[string]http.Handler{
		"404": http.NotFoundHandler(),
		"500": http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }),
		"sse": http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(": ping\n\n"))
		}),
		"html": http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<!doctype html>")) }),
	}
	for name, h := range handlers {
		for _, p := range []string{"/", "/api/feed", "/api/events", "/assets/x.js", "/nope", "/robots.txt"} {
			w := serve(o, h, p)
			for k, want := range map[string]string{
				"tdm-reservation": "1", "tdm-policy": "https://pcc.example/terms#tdm", "X-Frame-Options": "DENY",
				"Referrer-Policy": "no-referrer", "X-Content-Type-Options": "nosniff",
				"Cross-Origin-Opener-Policy": "same-origin", "Cross-Origin-Resource-Policy": "same-origin",
				"X-Robots-Tag":              "noindex, nofollow, noai, noimageai",
				"Reporting-Endpoints":       `csp="/api/csp-report"`,
				"Permissions-Policy":        "camera=(self), microphone=(self), geolocation=()",
				"Strict-Transport-Security": "max-age=63072000; includeSubDomains",
			} {
				if got := w.Header().Get(k); got != want {
					t.Errorf("%s %s %s: %q want %q", name, p, k, got, want)
				}
			}
			if got := w.Header().Get("Content-Security-Policy"); got != CSP(o) {
				t.Errorf("%s %s: CSP %q", name, p, got)
			}
			noStore := w.Header().Get("Cache-Control") == "no-store"
			if noStore != strings.HasPrefix(p, "/api/") {
				t.Errorf("%s %s: Cache-Control %q", name, p, w.Header().Get("Cache-Control"))
			}
		}
	}
}

func TestCSP(t *testing.T) {
	want := "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' blob: data:; font-src 'self'; " +
		"connect-src 'self'; worker-src 'self'; manifest-src 'self'; media-src 'self' blob:; frame-ancestors 'none'; " +
		"base-uri 'none'; form-action 'self'; object-src 'none'; require-trusted-types-for 'script'; " +
		"trusted-types pcc-forbid pcc-script-url; upgrade-insecure-requests; report-uri /api/csp-report; report-to csp"
	if got := CSP(Options{Origin: "https://pcc.example", Prod: true}); got != want {
		t.Fatalf("prod CSP\n got %s\nwant %s", got, want)
	}
	dev := CSP(Options{Origin: "http://localhost:5173"})
	if strings.Contains(dev, "upgrade-insecure-requests") {
		t.Fatal("dev over http must not upgrade requests")
	}
	for _, bad := range []string{"unsafe-inline", "unsafe-eval", "*"} {
		if strings.Contains(dev, bad) {
			t.Fatalf("CSP contains %s", bad)
		}
	}
	media := CSP(Options{Origin: "https://pcc.example", MediaOrigin: "https://media.pcc.example", Prod: true})
	if !strings.Contains(media, "img-src 'self' blob: data: https://media.pcc.example;") {
		t.Fatalf("media origin: %s", media)
	}
	if same := CSP(Options{Origin: "https://pcc.example", MediaOrigin: "https://pcc.example"}); strings.Count(same, "https://pcc.example") != 0 {
		t.Fatalf("same-origin media listed: %s", same)
	}
	w := serve(Options{Origin: "http://localhost:5173"}, http.NotFoundHandler(), "/")
	if w.Header().Get("Strict-Transport-Security") != "" {
		t.Fatal("HSTS outside prod")
	}
}

func TestRobots(t *testing.T) {
	r := string(RobotsTxt())
	for _, bot := range []string{"GPTBot", "ClaudeBot", "CCBot", "Google-Extended", "PerplexityBot", "Bytespider", "Applebot-Extended", "meta-externalagent"} {
		if !strings.Contains(r, "User-agent: "+bot+"\nDisallow: /") {
			t.Errorf("robots.txt must name %s", bot)
		}
	}
	if !strings.Contains(r, "User-agent: *\nDisallow: /") {
		t.Error("robots.txt must disallow everything for *")
	}
	if strings.Contains(r, "Allow:") {
		t.Error("robots.txt allows something")
	}
}

func TestAITxtAndTDMRep(t *testing.T) {
	a := string(AITxt())
	if !strings.Contains(a, "User-Agent: *\nDisallow: /\n") {
		t.Fatalf("ai.txt %q", a)
	}
	var doc []map[string]any
	if err := json.Unmarshal(TDMRepJSON("https://pcc.example"), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc) != 1 || doc[0]["location"] != "/" || doc[0]["tdm-reservation"] != float64(1) ||
		doc[0]["tdm-policy"] != "https://pcc.example/terms#tdm" {
		t.Fatalf("tdmrep %v", doc)
	}
}
