// Package engageapi serves likes, seen, hide, show-less and (P1) saves (plan §5.3).
//
// Owner: P0-W06 → P1-W08.
package engageapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/api"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/apix"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
)

var (
	wLike   = apix.Write("like")
	wSocial = apix.Write("social")
	rPost   = apix.Read("post_read")
	rFeed   = apix.Read("feed_page")
)

// LikeOut is the answer to PUT/DELETE /api/posts/{ref}/like.
type LikeOut struct {
	Likes gate.LikeSummary `json:"likes"`
}

// SeenIn is POST /api/seen: at most 50 refs count; the rest are ignored.
type SeenIn struct {
	Posts []gate.Ref `json:"posts"`
}

// Register registers the module routes on the /api router.
func Register(r chi.Router, d *api.Deps) {
	es := d.Engage

	like := func(on bool) apix.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) error {
			ref := apix.Ref(r, "ref")
			if err := es.Like(r.Context(), ref, on); err != nil {
				return err
			}
			ls, err := es.Likers(r.Context(), ref)
			if err != nil {
				return err
			}
			return apix.JSON(w, http.StatusOK, LikeOut{Likes: ls})
		}
	}
	apix.Route(r, http.MethodPut, "/posts/{ref}/like", wLike, like(true))
	apix.Route(r, http.MethodDelete, "/posts/{ref}/like", wLike, like(false))
	apix.Route(r, http.MethodGet, "/posts/{ref}/likes", rPost, func(w http.ResponseWriter, r *http.Request) error {
		ls, err := es.Likers(r.Context(), apix.Ref(r, "ref"))
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, ls)
	})
	apix.Route(r, http.MethodPost, "/seen", apix.Free, func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[SeenIn](r, 0)
		if err != nil {
			return err
		}
		if err := es.MarkSeen(r.Context(), in.Posts); err != nil {
			return err
		}
		return apix.NoContent(w)
	})
	apix.Route(r, http.MethodGet, "/seen/recent", rFeed, func(w http.ResponseWriter, r *http.Request) error {
		page, err := es.RecentlySeen(r.Context(), r.URL.Query().Get("cursor"))
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, page)
	})
	apix.Route(r, http.MethodPost, "/posts/{ref}/hide", wSocial, func(w http.ResponseWriter, r *http.Request) error {
		if err := es.Hide(r.Context(), apix.Ref(r, "ref")); err != nil {
			return err
		}
		return apix.NoContent(w)
	})
	apix.Route(r, http.MethodPost, "/posts/{ref}/show-less", wSocial, func(w http.ResponseWriter, r *http.Request) error {
		if err := es.ShowLess(r.Context(), apix.Ref(r, "ref")); err != nil {
			return err
		}
		return apix.NoContent(w)
	})
}
