package mediaapi

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/api"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/media"
)

// RegisterPublic registers GET /m/{key} on the ROOT router: the dev/test
// signed-media origin, which verifies the URL exactly like the Worker (403
// for an unsigned, tampered, other-viewer or expired URL; 404 for a valid
// signature over deleted or unknown media) and sends the media-origin headers.
// Nothing is registered in prod.
func RegisterPublic(r chi.Router, d *api.Deps) {
	if d.Cfg == nil || !d.Cfg.DevLike() {
		return
	}
	serve := func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
		h.Set("Content-Disposition", "inline")
		b, err := d.Media.Serve(r.Context(), chi.URLParam(r, "key"), r.URL.Query())
		if err != nil {
			h.Set("Cache-Control", "no-store")
			switch {
			case errors.Is(err, media.ErrForbidden):
				w.WriteHeader(http.StatusForbidden)
			case errors.Is(err, gate.ErrNotFound):
				w.WriteHeader(http.StatusNotFound)
			default:
				w.WriteHeader(http.StatusInternalServerError)
			}
			return
		}
		defer func() { _ = b.Body.Close() }()
		h.Set("Content-Type", b.MIME)
		h.Set("Content-Length", strconv.FormatInt(b.Size, 10))
		h.Set("Cache-Control", "private, max-age="+strconv.Itoa(b.MaxAge))
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = io.Copy(w, b.Body)
		}
	}
	r.Get("/m/{key}", serve)
	r.Head("/m/{key}", serve)
}
