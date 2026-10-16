// Package secheaders sets the security, TDM and anti-AI headers on every response
// (ARCHITECTURE §17.3(6), plan §6.6) and serves robots.txt / ai.txt / tdmrep.json.
//
// Every response carries the CSP (with report-to, plus report-uri for browsers
// without the Reporting API), X-Frame-Options: DENY, the W3C TDMRep
// reservation and policy, X-Robots-Tag with noai/noimageai, no-referrer,
// nosniff, same-origin opener and resource policies and the permissions policy;
// HSTS in production; no-store on /api/*. Headers are set before the handler
// runs, so errors, 404s, SSE streams and static files all carry them.
//
// Owner: P0-W12.
package secheaders

import (
	"encoding/json"
	"net/http"
	"strings"
)

// Options configure the middleware.
type Options struct {
	Origin      string // exact first-party origin
	MediaOrigin string // origin of signed media URLs (P1); "" = same origin
	Prod        bool
}

// ReportPath is the first-party CSP report endpoint (anonymous, rate-limited,
// aggregate counts only).
const ReportPath = "/api/csp-report"

// CSP returns the Content-Security-Policy for o.
func CSP(o Options) string {
	img := "'self' blob: data:"
	if o.MediaOrigin != "" && o.MediaOrigin != o.Origin {
		img += " " + o.MediaOrigin
	}
	d := []string{
		"default-src 'none'",
		"script-src 'self'",
		"style-src 'self'",
		"img-src " + img,
		"font-src 'self'",
		"connect-src 'self'",
		"worker-src 'self'",
		"manifest-src 'self'",
		"media-src 'self' blob:",
		"frame-ancestors 'none'",
		"base-uri 'none'",
		"form-action 'self'",
		"object-src 'none'",
		"require-trusted-types-for 'script'",
		"trusted-types pcc-forbid pcc-script-url",
	}
	if o.Prod {
		d = append(d, "upgrade-insecure-requests")
	}
	d = append(d, "report-uri "+ReportPath, "report-to csp")
	return strings.Join(d, "; ")
}

// Middleware sets the headers on EVERY response (HTML, JSON, SSE, assets, 404).
func Middleware(o Options) func(http.Handler) http.Handler {
	csp := CSP(o)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Content-Security-Policy", csp)
			h.Set("Reporting-Endpoints", `csp="`+ReportPath+`"`)
			h.Set("tdm-reservation", "1")
			h.Set("tdm-policy", o.Origin+"/terms#tdm")
			h.Set("X-Robots-Tag", "noindex, nofollow, noai, noimageai")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
			h.Set("Cross-Origin-Resource-Policy", "same-origin")
			h.Set("Permissions-Policy", "camera=(self), microphone=(self), geolocation=()")
			if o.Prod {
				h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
			}
			if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api" {
				h.Set("Cache-Control", "no-store")
			}
			next.ServeHTTP(w, r)
		})
	}
}

// AICrawlers are named explicitly in robots.txt.
var AICrawlers = []string{
	"GPTBot", "ChatGPT-User", "OAI-SearchBot", "ClaudeBot", "Claude-Web", "anthropic-ai", "CCBot",
	"Google-Extended", "PerplexityBot", "Bytespider", "Applebot-Extended", "meta-externalagent",
	"Amazonbot", "cohere-ai", "Diffbot", "ImagesiftBot", "omgili", "Timpibot", "YouBot",
}

// RobotsTxt disallows everything for everyone and names AI crawlers explicitly.
func RobotsTxt() []byte {
	var b strings.Builder
	for _, bot := range AICrawlers {
		b.WriteString("User-agent: " + bot + "\nDisallow: /\n\n")
	}
	b.WriteString("User-agent: *\nDisallow: /\n")
	return []byte(b.String())
}

// AITxt is the (non-standard, best-effort) ai.txt opt-out.
func AITxt() []byte {
	return []byte("# Members' content is never licensed or used for AI training.\nUser-Agent: *\nDisallow: /\nDisallow: *\n" +
		"Disallow: *.jpg\nDisallow: *.jpeg\nDisallow: *.png\nDisallow: *.webp\nDisallow: *.gif\n")
}

// TDMRepJSON is the W3C TDMRep /.well-known/tdmrep.json document.
func TDMRepJSON(origin string) []byte {
	b, _ := json.Marshal([]map[string]any{{"location": "/", "tdm-reservation": 1, "tdm-policy": origin + "/terms#tdm"}})
	return b
}
