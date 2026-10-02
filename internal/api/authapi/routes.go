// Package authapi serves /api/session, /api/auth/* and /api/enroll/* (plan §5.3).
//
// Owner: P0-W01 → P1-W07. The scaffold registered the working GET /session and
// POST /auth/logout plus the login routes (whose service methods are stubs).
package authapi

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/api"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/apix"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/auth"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/httpx"
)

// Register registers the module's routes on the /api router.
func Register(r chi.Router, d *api.Deps) {
	apix.Route(r, http.MethodGet, "/session", apix.Free, func(w http.ResponseWriter, r *http.Request) error {
		sv, err := d.Auth.Session(r.Context())
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, sv)
	})
	apix.Route(r, http.MethodPost, "/auth/logout", apix.Free, func(w http.ResponseWriter, r *http.Request) error {
		if err := d.Auth.Logout(r.Context()); err != nil {
			return err
		}
		httpx.ClearSessionCookie(w, d.Auth.SecureCookies())
		return apix.NoContent(w)
	})
	apix.Route(r, http.MethodPost, "/auth/login/begin", apix.Anon("login"), func(w http.ResponseWriter, r *http.Request) error {
		if _, err := apix.Decode[struct{}](r, 0); err != nil {
			return err
		}
		tok, opts, err := d.Auth.LoginBegin(r.Context())
		if err != nil {
			return err
		}
		httpx.SetSessionCookie(w, tok, auth.MaxAge("guest"), d.Auth.SecureCookies())
		return apix.JSON(w, http.StatusOK, map[string]json.RawMessage{"options": opts})
	})
	apix.Route(r, http.MethodPost, "/auth/login/finish", apix.Anon("login"), func(w http.ResponseWriter, r *http.Request) error {
		body, err := apix.Decode[struct {
			Credential json.RawMessage `json:"credential"`
		}](r, 64<<10)
		if err != nil {
			return err
		}
		tok, me, err := d.Auth.LoginFinish(r.Context(), body.Credential)
		if err != nil {
			return err
		}
		httpx.SetSessionCookie(w, tok, auth.MaxAge("user"), d.Auth.SecureCookies())
		return apix.JSON(w, http.StatusOK, gate.SessionView{Authenticated: true, Kind: "user", Me: &me})
	})
}
