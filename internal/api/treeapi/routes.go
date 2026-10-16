// Package treeapi serves /api/tree/* (plan §5.3): the family tree around a
// focal post, its rows re-centered or extended within a snapshot, and further
// pages of a row.
//
// Owner: P0-W08 → P1-W08.
package treeapi

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/api"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/apix"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
)

var rTree = apix.Read("tree_view")

// gen parses the row generation; anything but an integer is the caller's own
// bad input.
func gen(r *http.Request) (int, error) {
	g, err := strconv.Atoi(r.URL.Query().Get("gen"))
	if err != nil {
		return 0, gate.NewProblem(gate.StatusBadRequest, "bad_request", "gen")
	}
	return g, nil
}

// Register registers the module routes on the /api router.
func Register(r chi.Router, d *api.Deps) {
	ts := d.Tree
	apix.Route(r, http.MethodGet, "/tree/{ref}", rTree, func(w http.ResponseWriter, r *http.Request) error {
		tv, err := ts.Open(r.Context(), apix.Ref(r, "ref"), r.URL.Query().Get("preset"))
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, tv)
	})
	apix.Route(r, http.MethodGet, "/tree/s/{snapshot}/row", rTree, func(w http.ResponseWriter, r *http.Request) error {
		g, err := gen(r)
		if err != nil {
			return err
		}
		row, err := ts.Row(r.Context(), chi.URLParam(r, "snapshot"), g, gate.Ref(r.URL.Query().Get("center")))
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, row)
	})
	apix.Route(r, http.MethodGet, "/tree/s/{snapshot}/more", rTree, func(w http.ResponseWriter, r *http.Request) error {
		g, err := gen(r)
		if err != nil {
			return err
		}
		q := r.URL.Query()
		row, err := ts.More(r.Context(), chi.URLParam(r, "snapshot"), g, gate.Ref(q.Get("center")), q.Get("cursor"))
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, row)
	})
}
