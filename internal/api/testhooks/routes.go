// Package testhooks serves /api/_test/* for Playwright runs. Registered ONLY when
// PCC_ENV=test and PCC_TEST_HOOKS=1 (d.Hooks != nil); otherwise those paths get
// the uniform not-found like any unknown route.
//
// Owner: SCAFFOLD.
package testhooks

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/api"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/apix"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/auth"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/httpx"
)

// Register registers the hooks when enabled.
func Register(r chi.Router, d *api.Deps) {
	h := d.Hooks
	if h == nil || d.Cfg == nil || string(d.Cfg.Env) != "test" || !d.Cfg.TestHooks {
		return
	}
	apix.Route(r, http.MethodPost, "/_test/reset", apix.Free, func(w http.ResponseWriter, r *http.Request) error {
		if err := h.Reset(r.Context()); err != nil {
			return err
		}
		return apix.NoContent(w)
	})
	apix.Route(r, http.MethodPost, "/_test/clock", apix.Free, func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[struct {
			Advance string `json:"advance"`
		}](r, 0)
		if err != nil {
			return err
		}
		dur, err := time.ParseDuration(in.Advance)
		if err != nil {
			return err
		}
		if err := h.AdvanceClock(dur); err != nil {
			return err
		}
		return apix.NoContent(w)
	})
	apix.Route(r, http.MethodPost, "/_test/seed", apix.Free, func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[struct {
			Fixture string `json:"fixture"`
		}](r, 0)
		if err != nil {
			return err
		}
		if err := h.Seed(r.Context(), in.Fixture); err != nil {
			return err
		}
		return apix.NoContent(w)
	})
	apix.Route(r, http.MethodPost, "/_test/login", apix.Free, func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[struct {
			Handle string `json:"handle"`
		}](r, 0)
		if err != nil {
			return err
		}
		tok, csrf, err := h.Login(r.Context(), in.Handle)
		if err != nil {
			return err
		}
		httpx.SetSessionCookie(w, tok, auth.MaxAge("user"), d.Auth.SecureCookies())
		return apix.JSON(w, http.StatusOK, map[string]string{"csrf": csrf})
	})
	apix.Route(r, http.MethodPost, "/_test/genesis", apix.Free, func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[struct {
			Handle string `json:"handle"`
		}](r, 0)
		if err != nil {
			return err
		}
		tok, err := h.GenesisToken(r.Context(), in.Handle)
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, map[string]string{"token": tok})
	})
}
