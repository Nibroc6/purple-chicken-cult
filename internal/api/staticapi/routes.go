// Package staticapi serves /robots.txt, /ai.txt, /.well-known/tdmrep.json and the
// SPA: hashed assets (immutable) and index.html (no-store) for every other path.
// The SPA comes from web/dist (embedded) or PCC_STATIC_DIR.
//
// Owner: P0-W12 (the scaffold ships a working SPA fallback).
package staticapi

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/api"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/secheaders"
)

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
	spa := SPA(d.Static)
	r.NotFound(spa)
	r.MethodNotAllowed(spa)
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
				ctype := mime.TypeByExtension(path.Ext(p))
				if ctype == "" {
					ctype = "application/octet-stream"
				}
				w.Header().Set("Content-Type", ctype)
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
