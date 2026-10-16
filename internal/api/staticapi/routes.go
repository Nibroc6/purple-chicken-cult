// Package staticapi serves /robots.txt, /ai.txt, /.well-known/tdmrep.json, the
// published server keys (/.well-known/pcc-keys.json, shown on /terms) and the
// SPA: hashed assets (immutable), other files (no-cache) and index.html
// (no-store) for every other path, /terms and the landing page included. The
// SPA comes from web/dist (embedded) or PCC_STATIC_DIR. The security, TDM and
// anti-AI headers come from secheaders on every response, 404s included.
//
// Owner: P0-W12.
package staticapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/api"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/apix"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/secheaders"
)

// KeysPath serves the published server signing keys with their fingerprints
// (public material; the terms page lists them so members can pin them).
const KeysPath = "/.well-known/pcc-keys.json"

// keysTTL is how long the keys document is cached in memory.
const keysTTL = time.Minute

// KeysDoc is the document at KeysPath.
type KeysDoc struct {
	Server []gate.KeyView `json:"server"`
}

// Register registers the static routes on the ROOT router and installs the SPA
// fallback as its NotFound handler.
func Register(r chi.Router, d *api.Deps) {
	text := func(body []byte, ctype string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", ctype)
			w.Header().Set("Cache-Control", "public, max-age=3600")
			_, _ = w.Write(body)
		}
	}
	r.Get("/robots.txt", text(secheaders.RobotsTxt(), "text/plain; charset=utf-8"))
	r.Get("/ai.txt", text(secheaders.AITxt(), "text/plain; charset=utf-8"))
	r.Get("/.well-known/tdmrep.json", text(secheaders.TDMRepJSON(d.Cfg.Origin), "application/json"))
	r.Get(KeysPath, keysHandler(d))
	spa := SPA(d.Static)
	r.NotFound(spa)
	r.MethodNotAllowed(spa)
}

// keysHandler serves KeysPath from a one-minute memory cache, charged to the
// client network like the other anonymous surfaces.
func keysHandler(d *api.Deps) http.HandlerFunc {
	var (
		mu      sync.Mutex
		body    []byte
		fetched time.Time
	)
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Limits != nil {
			if err := d.Limits.ChargeRequest(r.Context(), apix.Anon("keys")); err != nil {
				apix.Error(w, r, err)
				return
			}
		}
		mu.Lock()
		if body == nil || time.Since(fetched) > keysTTL {
			keys, err := d.Records.Keys(r.Context())
			if err == nil {
				body, err = json.Marshal(KeysDoc{Server: keys})
			}
			if err != nil {
				mu.Unlock()
				apix.Error(w, r, err)
				return
			}
			fetched = time.Now()
		}
		b := body
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=300")
		_, _ = w.Write(b)
	}
}

// types fixes the Content-Type of the SPA's file kinds (mime tables differ
// between systems; .webmanifest is missing from most).
var types = map[string]string{
	".js": "text/javascript; charset=utf-8", ".mjs": "text/javascript; charset=utf-8",
	".css": "text/css; charset=utf-8", ".html": "text/html; charset=utf-8", ".json": "application/json",
	".webmanifest": "application/manifest+json", ".svg": "image/svg+xml", ".png": "image/png",
	".ico": "image/x-icon", ".woff2": "font/woff2", ".wasm": "application/wasm", ".txt": "text/plain; charset=utf-8",
	".map": "application/json",
}

func contentType(p string) string {
	ext := path.Ext(p)
	if t, ok := types[ext]; ok {
		return t
	}
	if t := mime.TypeByExtension(ext); t != "" {
		return t
	}
	return "application/octet-stream"
}

// SPA serves files from static; unknown non-asset paths get index.html.
func SPA(static fs.FS) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		p := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if static != nil && p != "" && p != "index.html" {
			if b, err := fs.ReadFile(static, p); err == nil {
				w.Header().Set("Content-Type", contentType(p))
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else {
					w.Header().Set("Cache-Control", "no-cache")
				}
				http.ServeContent(w, r, p, time.Time{}, bytes.NewReader(b))
				return
			}
			if strings.HasPrefix(p, "assets/") || path.Ext(p) != "" {
				w.Header().Set("Cache-Control", "no-store")
				w.WriteHeader(http.StatusNotFound)
				return
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if static == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		f, err := static.Open("index.html")
		if errors.Is(err, fs.ErrNotExist) {
			// web/dist not built yet: serve the placeholder shell.
			_, _ = io.WriteString(w, placeholder)
			return
		}
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		defer func() { _ = f.Close() }()
		_, _ = io.Copy(w, f)
	}
}

// placeholder is served when the SPA has not been built (npm --prefix web run build).
// It contains no user-facing prose: only the app's name.
const placeholder = `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>pcc</title></head><body><div id="root"></div></body></html>`
