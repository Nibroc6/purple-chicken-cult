// Package invitesapi serves /api/invites/* and /api/join/* (plan §5.3).
//
// Owner: P0-W04 → P1-W02. The join flow runs in a guest session that
// /join/start creates; /join/register/finish replaces it with a user session.
package invitesapi

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/api"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/apix"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/auth"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/invites"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/httpx"
)

// WebAuthnMax bounds a WebAuthn response body (plan §5.2).
const WebAuthnMax = 64 << 10

var (
	rGraph = apix.Read("graph_read")
	wI     = apix.Write("invite_handshake")
	aJoin  = apix.Anon("join")
)

// InviteesOut is GET /api/invites/invitees.
type InviteesOut struct {
	Items []gate.InviteeView `json:"items"`
}

// OpenIn is POST /api/invites/handshakes.
type OpenIn struct {
	Mode  string `json:"mode"`
	Quiet bool   `json:"quiet"`
}

// JoinStartOut is POST /api/join/start: the guest session's CSRF token and
// what the join form must attest (D29).
type JoinStartOut struct {
	CSRF       string `json:"csrf"`
	TOSVersion int    `json:"tos_version"`
	MinAge     int    `json:"min_age"`
}

// ClaimIn is POST /api/join/claim.
type ClaimIn struct {
	Payload string `json:"payload"`
}

// OptionsOut carries WebAuthn creation options.
type OptionsOut struct {
	Options json.RawMessage `json:"options"`
}

// CredentialIn carries a WebAuthn registration response.
type CredentialIn struct {
	Credential json.RawMessage `json:"credential"`
}

// Register registers the module routes on the /api router.
func Register(r chi.Router, d *api.Deps) {
	inv := d.Invites
	secure := d.Auth.SecureCookies()

	apix.Route(r, http.MethodGet, "/invites/budget", rGraph, func(w http.ResponseWriter, r *http.Request) error {
		b, err := inv.Budget(r.Context())
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, b)
	})
	apix.Route(r, http.MethodGet, "/invites/invitees", rGraph, func(w http.ResponseWriter, r *http.Request) error {
		items, err := inv.Invitees(r.Context())
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, InviteesOut{Items: items})
	})
	apix.Route(r, http.MethodPost, "/invites/handshakes", wI, func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[OpenIn](r, 0)
		if err != nil {
			return err
		}
		hv, err := inv.Open(r.Context(), in.Mode, in.Quiet)
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusCreated, hv)
	})
	apix.Route(r, http.MethodGet, "/invites/handshakes/{hs}/frame", apix.Free, func(w http.ResponseWriter, r *http.Request) error {
		f, err := inv.Frame(r.Context(), apix.Ref(r, "hs"))
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, f)
	})
	apix.Route(r, http.MethodGet, "/invites/handshakes/{hs}", apix.Free, func(w http.ResponseWriter, r *http.Request) error {
		st, err := inv.Status(r.Context(), apix.Ref(r, "hs"))
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, st)
	})
	apix.Route(r, http.MethodPost, "/invites/handshakes/{hs}/confirm", wI, func(w http.ResponseWriter, r *http.Request) error {
		if err := inv.Confirm(r.Context(), apix.Ref(r, "hs")); err != nil {
			return err
		}
		return apix.NoContent(w)
	})
	apix.Route(r, http.MethodDelete, "/invites/handshakes/{hs}", wI, func(w http.ResponseWriter, r *http.Request) error {
		if err := inv.Cancel(r.Context(), apix.Ref(r, "hs")); err != nil {
			return err
		}
		return apix.NoContent(w)
	})

	// Join (invitee side).
	apix.Route(r, http.MethodPost, "/join/start", aJoin, func(w http.ResponseWriter, r *http.Request) error {
		if _, err := apix.Decode[struct{}](r, 0); err != nil {
			return err
		}
		tok, err := d.Auth.StartGuest(r.Context())
		if err != nil {
			return err
		}
		csrf, err := d.Auth.CSRFForToken(r.Context(), tok)
		if err != nil {
			return err
		}
		httpx.SetSessionCookie(w, tok, auth.MaxAge("guest"), secure)
		return apix.JSON(w, http.StatusOK, JoinStartOut{CSRF: csrf, TOSVersion: d.Cfg.TOSVersion, MinAge: d.Cfg.MinAge})
	})
	apix.Route(r, http.MethodPost, "/join/claim", aJoin, func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[ClaimIn](r, 0)
		if err != nil {
			return err
		}
		if err := inv.Claim(r.Context(), in.Payload); err != nil {
			return err
		}
		return apix.NoContent(w)
	})
	apix.Route(r, http.MethodGet, "/join/status", aJoin, func(w http.ResponseWriter, r *http.Request) error {
		st, err := inv.JoinStatus(r.Context())
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, st)
	})
	apix.Route(r, http.MethodPost, "/join/register/begin", aJoin, func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[invites.Registration](r, 0)
		if err != nil {
			return err
		}
		opts, err := inv.RegisterBegin(r.Context(), in)
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, OptionsOut{Options: opts})
	})
	apix.Route(r, http.MethodPost, "/join/register/finish", aJoin, func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[CredentialIn](r, WebAuthnMax)
		if err != nil {
			return err
		}
		tok, me, err := inv.RegisterFinish(r.Context(), in.Credential)
		if err != nil {
			return err
		}
		csrf, err := d.Auth.CSRFForToken(r.Context(), tok)
		if err != nil {
			return err
		}
		httpx.SetSessionCookie(w, tok, auth.MaxAge("user"), secure)
		return apix.JSON(w, http.StatusOK, gate.SessionView{Authenticated: true, Kind: "user", CSRF: csrf, Me: &me})
	})
}
