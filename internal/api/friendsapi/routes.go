// Package friendsapi serves /api/friends/*, /api/people/*, /api/blocks and /api/suggestions (plan §5.3).
// Phase 1: request, accept, unfriend and block take an optional {intent,
// assertion} co-signature body (required for passkey sessions), hide-via and
// the friend+ upgrade opener.
//
// Owner: P0-W03 → P1-W03.
package friendsapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/api"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/apix"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/friends"
)

var (
	rGraph   = apix.Read("graph_read")
	rPost    = apix.Read("post_read")
	wRequest = apix.Write("friend_request")
	wSocial  = apix.Write("social")
	// An upgrade spends no code attempt (invite_handshake): it has its own cap.
	wHandshake = apix.Write("friend_upgrade")
)

// maxCosig bounds a co-signed change's body (an intent ref and one WebAuthn assertion).
const maxCosig = 16 << 10

// cosig decodes the optional {intent, assertion} body of a graph-changing
// write (P1). An empty body is no co-signature; anything else must be exactly
// that object.
func cosig(r *http.Request) ([]friends.Cosig, error) {
	b, err := io.ReadAll(io.LimitReader(r.Body, maxCosig+1))
	if err != nil || len(b) > maxCosig {
		return nil, gate.NewProblem(gate.StatusBadRequest, "bad_request")
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var c friends.Cosig
	if err := dec.Decode(&c); err != nil || dec.More() {
		return nil, gate.NewProblem(gate.StatusBadRequest, "bad_request")
	}
	return []friends.Cosig{c}, nil
}

// signed adapts a co-signed change to a 204 handler.
func signed(fn func(*http.Request, gate.Ref, []friends.Cosig) error) apix.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		c, err := cosig(r)
		if err != nil {
			return err
		}
		if err := fn(r, apix.Ref(r, "ref"), c); err != nil {
			return err
		}
		return apix.NoContent(w)
	}
}

// RequestsView is GET /api/friends/requests.
type RequestsView struct {
	Incoming []gate.PersonView `json:"incoming"`
	Outgoing []gate.PersonView `json:"outgoing"`
}

// RequestSent is the 202 answer of POST /api/people/{ref}/request. It is the
// same whether the request is pending, declined or silently dropped.
type RequestSent struct {
	State string `json:"state"`
}

// PeopleView is GET /api/blocks.
type PeopleView struct {
	Items []gate.PersonView `json:"items"`
}

// UpgradeIn is POST /api/friends/{ref}/upgrade (optional body).
type UpgradeIn struct {
	Quiet bool `json:"quiet"`
}

// SuggestionsView is GET /api/suggestions.
type SuggestionsView struct {
	Items []friends.Suggestion `json:"items"`
}

// Register registers the module routes on the /api router.
func Register(r chi.Router, d *api.Deps) {
	f := d.Friends
	apix.Route(r, http.MethodGet, "/friends", rGraph, func(w http.ResponseWriter, r *http.Request) error {
		page, err := f.List(r.Context(), r.URL.Query().Get("cursor"))
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, page)
	})
	apix.Route(r, http.MethodGet, "/friends/requests", rGraph, func(w http.ResponseWriter, r *http.Request) error {
		in, out, err := f.Requests(r.Context())
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, RequestsView{Incoming: in, Outgoing: out})
	})
	apix.Route(r, http.MethodPost, "/people/{ref}/request", wRequest, func(w http.ResponseWriter, r *http.Request) error {
		c, err := cosig(r)
		if err != nil {
			return err
		}
		if err := f.Request(r.Context(), apix.Ref(r, "ref"), c...); err != nil {
			return err
		}
		return apix.JSON(w, http.StatusAccepted, RequestSent{State: "outgoing"})
	})
	noContent := func(fn func(*http.Request, gate.Ref) error) apix.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) error {
			if err := fn(r, apix.Ref(r, "ref")); err != nil {
				return err
			}
			return apix.NoContent(w)
		}
	}
	apix.Route(r, http.MethodPost, "/friends/requests/{ref}/accept", wRequest, signed(func(r *http.Request, ref gate.Ref, c []friends.Cosig) error {
		return f.Accept(r.Context(), ref, c...)
	}))
	apix.Route(r, http.MethodPost, "/friends/requests/{ref}/decline", wSocial, noContent(func(r *http.Request, ref gate.Ref) error {
		return f.Decline(r.Context(), ref)
	}))
	apix.Route(r, http.MethodDelete, "/people/{ref}/request", wSocial, noContent(func(r *http.Request, ref gate.Ref) error {
		return f.Withdraw(r.Context(), ref)
	}))
	apix.Route(r, http.MethodDelete, "/friends/{ref}", wSocial, signed(func(r *http.Request, ref gate.Ref, c []friends.Cosig) error {
		return f.Unfriend(r.Context(), ref, c...)
	}))
	apix.Route(r, http.MethodPut, "/people/{ref}/block", wSocial, signed(func(r *http.Request, ref gate.Ref, c []friends.Cosig) error {
		return f.Block(r.Context(), ref, c...)
	}))
	apix.Route(r, http.MethodDelete, "/people/{ref}/block", wSocial, noContent(func(r *http.Request, ref gate.Ref) error {
		return f.Unblock(r.Context(), ref)
	}))
	apix.Route(r, http.MethodGet, "/blocks", rGraph, func(w http.ResponseWriter, r *http.Request) error {
		items, err := f.Blocks(r.Context())
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, PeopleView{Items: items})
	})
	apix.Route(r, http.MethodPut, "/people/{ref}/mute", wSocial, noContent(func(r *http.Request, ref gate.Ref) error {
		return f.Mute(r.Context(), ref, true)
	}))
	apix.Route(r, http.MethodDelete, "/people/{ref}/mute", wSocial, noContent(func(r *http.Request, ref gate.Ref) error {
		return f.Mute(r.Context(), ref, false)
	}))
	apix.Route(r, http.MethodGet, "/people/{ref}", rGraph, func(w http.ResponseWriter, r *http.Request) error {
		p, err := f.Person(r.Context(), apix.Ref(r, "ref"))
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, p)
	})
	apix.Route(r, http.MethodGet, "/people/{ref}/posts", rPost, func(w http.ResponseWriter, r *http.Request) error {
		page, err := f.PersonPosts(r.Context(), apix.Ref(r, "ref"), r.URL.Query().Get("cursor"))
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, page)
	})
	apix.Route(r, http.MethodPut, "/people/{ref}/hide-via", wSocial, noContent(func(r *http.Request, ref gate.Ref) error {
		return f.HideVia(r.Context(), ref, true)
	}))
	apix.Route(r, http.MethodDelete, "/people/{ref}/hide-via", wSocial, noContent(func(r *http.Request, ref gate.Ref) error {
		return f.HideVia(r.Context(), ref, false)
	}))
	apix.Route(r, http.MethodPost, "/friends/{ref}/upgrade", wHandshake, func(w http.ResponseWriter, r *http.Request) error {
		// The body is optional: {"quiet": true} turns the jingle off.
		var in UpgradeIn
		if r.ContentLength != 0 {
			var err error
			if in, err = apix.Decode[UpgradeIn](r, 0); err != nil {
				return err
			}
		}
		hv, err := f.UpgradeBegin(r.Context(), apix.Ref(r, "ref"), in.Quiet)
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusCreated, hv)
	})
	apix.Route(r, http.MethodGet, "/suggestions", rGraph, func(w http.ResponseWriter, r *http.Request) error {
		items, err := f.Suggestions(r.Context())
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, SuggestionsView{Items: items})
	})
}
