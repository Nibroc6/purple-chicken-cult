// Package themeapi serves /api/me/theme (plan §5.3): the caller's own
// token-only theme (§15.2 A), validated by gate/themes.
//
// Owner: P1-W09.
package themeapi

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/api"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/apix"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/themes"
)

var wAccount = apix.Write("account")

// ThemeIn is the body of PUT /api/me/theme.
type ThemeIn struct {
	Tokens json.RawMessage `json:"tokens"`
}

// ThemeOut answers GET and PUT /api/me/theme; Theme is absent for the default.
type ThemeOut struct {
	Theme *themes.View `json:"theme,omitempty"`
}

// Register registers the module routes on the /api router.
func Register(r chi.Router, d *api.Deps) {
	ts := d.Themes
	// GET is the caller's own row (steward: contract request p1-w09-1).
	apix.Route(r, http.MethodGet, "/me/theme", apix.Free, func(w http.ResponseWriter, r *http.Request) error {
		v, err := ts.Get(r.Context())
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, ThemeOut{Theme: v})
	})
	apix.Route(r, http.MethodPut, "/me/theme", wAccount, func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[ThemeIn](r, 4<<10)
		if err != nil {
			return err
		}
		v, err := ts.Set(r.Context(), in.Tokens)
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, ThemeOut{Theme: v})
	})
	apix.Route(r, http.MethodDelete, "/me/theme", wAccount, func(w http.ResponseWriter, r *http.Request) error {
		if err := ts.Clear(r.Context()); err != nil {
			return err
		}
		return apix.NoContent(w)
	})
}
