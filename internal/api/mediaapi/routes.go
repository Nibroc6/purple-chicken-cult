// Package mediaapi serves /api/media/* and, in dev/test only, the signed-media
// origin /m/{key} (plan §5.3). Production serves media from the Cloudflare
// Worker (deploy/cloudflare/media-worker.js), which checks the same signature.
//
// Owner: P1-W04.
package mediaapi

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/api"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/apix"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/media"
)

var (
	wMedia = apix.Write("media")
	rPost  = apix.Read("post_read")
)

// maxBody is the multipart limit of POST /media: the 15 MiB file plus the
// alt field and the multipart framing.
const maxBody = media.MaxUploadBytes + 64<<10

// URLOut is the answer of GET /media/{ref}/url.
type URLOut struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Register registers the module routes on the /api router.
func Register(r chi.Router, d *api.Deps) {
	apix.Route(r, http.MethodPost, "/media", wMedia, func(w http.ResponseWriter, r *http.Request) error {
		return upload(w, r, d)
	})
	apix.Route(r, http.MethodGet, "/media/{ref}/url", rPost, func(w http.ResponseWriter, r *http.Request) error {
		u, exp, err := d.Media.URL(r.Context(), apix.Ref(r, "ref"), r.URL.Query().Get("variant"))
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, URLOut{URL: u, ExpiresAt: exp})
	})
}

func upload(w http.ResponseWriter, r *http.Request, d *api.Deps) error {
	if err := d.Media.Ready(r.Context()); err != nil {
		return err // before reading a byte of the body
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	mr, err := r.MultipartReader()
	if err != nil {
		return gate.NewProblem(gate.StatusBadRequest, "bad_request")
	}
	var (
		alt  string
		file []byte
		seen bool
	)
	for {
		p, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return partError(err)
		}
		switch p.FormName() {
		case "alt":
			b, err := io.ReadAll(io.LimitReader(p, 8<<10))
			if err != nil {
				return partError(err)
			}
			alt = string(b)
		case "file":
			if seen {
				return gate.NewProblem(gate.StatusBadRequest, "bad_request", "file")
			}
			seen = true
			// The alt part may come before or after the file: buffer the file
			// (bounded by maxBody) so the upload is processed once.
			if file, err = io.ReadAll(p); err != nil {
				return partError(err)
			}
		default:
			return gate.NewProblem(gate.StatusBadRequest, "bad_request")
		}
	}
	if !seen {
		return gate.NewProblem(gate.StatusBadRequest, "bad_request", "file")
	}
	mv, err := d.Media.Upload(r.Context(), bytes.NewReader(file), int64(len(file)), "", alt)
	if err != nil {
		return err
	}
	return apix.JSON(w, http.StatusCreated, mv)
}

func partError(err error) error {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return gate.NewProblem(gate.StatusUnprocessable, "media_too_large", "file")
	}
	return gate.NewProblem(gate.StatusBadRequest, "bad_request")
}
