package secheaders

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHeadersOnEveryResponse(t *testing.T) {
	h := Middleware(Options{Origin: "https://pcc.example", Prod: true})(http.NotFoundHandler())
	for _, p := range []string{"/", "/api/feed", "/assets/x.js", "/nope"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		for k, want := range map[string]string{
			"tdm-reservation": "1", "X-Frame-Options": "DENY", "Referrer-Policy": "no-referrer",
			"X-Content-Type-Options": "nosniff",
		} {
			if got := w.Header().Get(k); got != want {
				t.Errorf("%s %s: %q want %q", p, k, got, want)
			}
		}
		csp := w.Header().Get("Content-Security-Policy")
		for _, d := range []string{"default-src 'none'", "script-src 'self'", "frame-ancestors 'none'", "require-trusted-types-for 'script'", "report-to csp"} {
			if !strings.Contains(csp, d) {
				t.Errorf("CSP lacks %q", d)
			}
		}
		if !strings.Contains(w.Header().Get("X-Robots-Tag"), "noai") {
			t.Error("X-Robots-Tag must carry noai")
		}
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
}
