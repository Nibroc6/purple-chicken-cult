package staticapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/Nibroc6/purple-chicken-cult/internal/api/staticapi"
)

func TestSPA(t *testing.T) {
	static := fstest.MapFS{
		"index.html":         {Data: []byte("<!doctype html><div id=root></div>")},
		"assets/index-a1.js": {Data: []byte("x")},
		"sw.js":              {Data: []byte("self")},
	}
	h := staticapi.SPA(static)
	for _, c := range []struct {
		method, path string
		status       int
		cache, ctype string
	}{
		{"GET", "/", 200, "no-store", "text/html; charset=utf-8"},
		{"GET", "/terms", 200, "no-store", "text/html; charset=utf-8"},
		{"GET", "/p/someref", 200, "no-store", "text/html; charset=utf-8"},
		{"HEAD", "/terms", 200, "no-store", "text/html; charset=utf-8"},
		{"GET", "/index.html", 200, "no-store", "text/html; charset=utf-8"},
		{"GET", "/assets/index-a1.js", 200, "public, max-age=31536000, immutable", "text/javascript; charset=utf-8"},
		{"GET", "/sw.js", 200, "no-cache", "text/javascript; charset=utf-8"},
		{"GET", "/assets/missing.js", 404, "no-store", ""},
		{"GET", "/favicon.ico", 404, "no-store", ""},
		{"GET", "/../../etc/passwd", 200, "no-store", "text/html; charset=utf-8"}, // cleaned to /etc/passwd: the SPA shell
		{"POST", "/terms", 404, "", ""},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(c.method, c.path, nil))
		if w.Code != c.status || w.Header().Get("Cache-Control") != c.cache {
			t.Errorf("%s %s: %d %q", c.method, c.path, w.Code, w.Header().Get("Cache-Control"))
		}
		if c.ctype != "" && w.Header().Get("Content-Type") != c.ctype {
			t.Errorf("%s %s: Content-Type %q", c.method, c.path, w.Header().Get("Content-Type"))
		}
	}
	// Without a build the shell placeholder is served; without any FS, 503.
	w := httptest.NewRecorder()
	staticapi.SPA(fstest.MapFS{}).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != 200 || w.Body.Len() == 0 {
		t.Fatalf("placeholder %d", w.Code)
	}
	w = httptest.NewRecorder()
	staticapi.SPA(nil).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("no static fs %d", w.Code)
	}
}
