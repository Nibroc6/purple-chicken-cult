// Package themeapi serves /api/me/theme (plan §5.3): the caller's own theme
// (§15.2 A: tokens, and optionally theme CSS with its images), validated by
// gate/themes.
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

// ThemeIn is the body of PUT /api/me/theme. CSS and Images are the optional
// advanced mode (theme CSS and the uploads it names).
type ThemeIn struct {
	Tokens json.RawMessage  `json:"tokens"`
	CSS    string           `json:"css,omitempty"`
	Images []themes.ImageIn `json:"images,omitempty"`
}

// maxBody fits the tokens, the largest theme CSS and its image refs.
const maxBody = themes.MaxTokens + themes.MaxSheet + 8<<10

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
		in, err := apix.Decode[ThemeIn](r, maxBody)
		if err != nil {
			return err
		}
		v, err := ts.Set(r.Context(), themes.Theme{Tokens: in.Tokens, CSS: in.CSS, Images: in.Images})
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
