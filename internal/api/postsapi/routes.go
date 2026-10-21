// Package postsapi serves /api/posts (create, thread, get, edit, delete,
// ref-policy, detach) (plan §5.3).
//
// Owner: P0-W05 → P1-W12.
package postsapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/api"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/apix"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/posts"
)

// Per-route body limits (plan §5.1): 20,000 code points can be ~80 KB.
const (
	PostMax   = 96 << 10
	ThreadMax = 5 << 19 // 2.5 MiB
)

var (
	wPost   = apix.Write("post")
	wSocial = apix.Write("social")
	rPost   = apix.Read("post_read")
)

// PartIn is one part of POST /api/posts/thread.
type PartIn struct {
	Body  string     `json:"body"`
	Lang  string     `json:"lang"`
	Media []gate.Ref `json:"media,omitempty"` // P1: ≤ 4 of the caller's uploads
}

// ThreadIn is POST /api/posts/thread: the parts share the audience, ref policy
// and preview choice; parents attach to the first part.
type ThreadIn struct {
	Parts     []PartIn         `json:"parts"`
	Audience  string           `json:"audience,omitempty"`
	RefPolicy string           `json:"ref_policy,omitempty"`
	NoPreview bool             `json:"no_preview,omitempty"`
	Parents   []posts.ParentIn `json:"parents,omitempty"`
}

// ThreadOut is the created thread, in order.
type ThreadOut struct {
	Items []gate.PostView `json:"items"`
}

// EditIn is PATCH /api/posts/{ref}.
type EditIn struct {
	Body string `json:"body"`
	Lang string `json:"lang,omitempty"`
}

// RefPolicyIn is PUT /api/posts/{ref}/ref-policy.
type RefPolicyIn struct {
	RefPolicy string `json:"ref_policy"`
}

// RefPolicyOut tells the client whether to offer "detach all".
type RefPolicyOut struct {
	OfferDetachAll bool `json:"offer_detach_all"`
}

// DetachIn is POST /api/posts/{ref}/detach.
type DetachIn struct {
	Child gate.Ref `json:"child"`
}

// Register registers the module routes on the /api router.
func Register(r chi.Router, d *api.Deps) {
	ps := d.Posts

	apix.Route(r, http.MethodPost, "/posts", wPost, func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[posts.Draft](r, PostMax)
		if err != nil {
			return err
		}
		pv, err := ps.Create(r.Context(), in)
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusCreated, pv)
	})
	apix.Route(r, http.MethodPost, "/posts/thread", wPost, func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[ThreadIn](r, ThreadMax)
		if err != nil {
			return err
		}
		drafts := make([]posts.Draft, len(in.Parts))
		for i, p := range in.Parts {
			drafts[i] = posts.Draft{Body: p.Body, Lang: p.Lang, Audience: in.Audience, RefPolicy: in.RefPolicy,
				NoPreview: in.NoPreview, Media: p.Media}
		}
		if len(drafts) > 0 {
			drafts[0].Parents = in.Parents
		}
		items, err := ps.CreateThread(r.Context(), drafts)
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusCreated, ThreadOut{Items: items})
	})
	apix.Route(r, http.MethodGet, "/posts/{ref}", rPost, func(w http.ResponseWriter, r *http.Request) error {
		pv, err := ps.Get(r.Context(), apix.Ref(r, "ref"))
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, pv)
	})
	apix.Route(r, http.MethodPatch, "/posts/{ref}", wPost, func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[EditIn](r, PostMax)
		if err != nil {
			return err
		}
		pv, err := ps.Edit(r.Context(), apix.Ref(r, "ref"), in.Body, in.Lang)
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, pv)
	})
	apix.Route(r, http.MethodDelete, "/posts/{ref}", wPost, func(w http.ResponseWriter, r *http.Request) error {
		if err := ps.Delete(r.Context(), apix.Ref(r, "ref")); err != nil {
			return err
		}
		return apix.NoContent(w)
	})
	apix.Route(r, http.MethodPut, "/posts/{ref}/ref-policy", wSocial, func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[RefPolicyIn](r, 0)
		if err != nil {
			return err
		}
		offer, err := ps.SetRefPolicy(r.Context(), apix.Ref(r, "ref"), in.RefPolicy)
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, RefPolicyOut{OfferDetachAll: offer})
	})
	apix.Route(r, http.MethodPost, "/posts/{ref}/detach", wSocial, func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[DetachIn](r, 0)
		if err != nil {
			return err
		}
		if err := ps.Detach(r.Context(), apix.Ref(r, "ref"), in.Child); err != nil {
			return err
		}
		return apix.NoContent(w)
	})
	apix.Route(r, http.MethodPost, "/posts/{ref}/detach-all", wSocial, func(w http.ResponseWriter, r *http.Request) error {
		if err := ps.DetachAll(r.Context(), apix.Ref(r, "ref")); err != nil {
			return err
		}
		return apix.NoContent(w)
	})
}
