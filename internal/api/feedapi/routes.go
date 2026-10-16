// Package feedapi serves /api/feed/* and /api/posts/{ref}/why (plan §5.3).
//
// Owner: P0-W07 → P1-W08.
package feedapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/api"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/apix"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
)

var (
	rFeed = apix.Read("feed_page")
	rPost = apix.Read("post_read")
)

// ExpandOut is the answer to GET /api/feed/expand.
type ExpandOut struct {
	Items []gate.FeedItem `json:"items"`
}

// NewOut is the answer to GET /api/feed/new: posts published since the session
// started in the tiers the reader reached, and the closest of those tiers.
type NewOut struct {
	Count int `json:"count"`
	Tier  int `json:"tier"`
}

// Register registers the module routes on the /api router.
func Register(r chi.Router, d *api.Deps) {
	fs := d.Feed
	apix.Route(r, http.MethodGet, "/feed", rFeed, func(w http.ResponseWriter, r *http.Request) error {
		q := r.URL.Query()
		page, err := fs.Page(r.Context(), q.Get("preset"), q.Get("cursor"))
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, page)
	})
	apix.Route(r, http.MethodGet, "/feed/expand", rFeed, func(w http.ResponseWriter, r *http.Request) error {
		items, err := fs.Expand(r.Context(), r.URL.Query().Get("token"))
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, ExpandOut{Items: items})
	})
	apix.Route(r, http.MethodGet, "/feed/new", rFeed, func(w http.ResponseWriter, r *http.Request) error {
		n, tier, err := fs.NewAbove(r.Context(), r.URL.Query().Get("session"))
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, NewOut{Count: n, Tier: tier})
	})
	apix.Route(r, http.MethodGet, "/posts/{ref}/why", rPost, func(w http.ResponseWriter, r *http.Request) error {
		why, err := fs.Why(r.Context(), apix.Ref(r, "ref"))
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, why)
	})
}
