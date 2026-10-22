// Package invitesapi serves /api/invites/* and /api/join/* (plan §5.3).
//
// Owner: P0-W04 → P1-W02. The inviter (and every initiator of an in-person
// handshake) drives /invites/handshakes/*; the other device drives /join/*.
// The join flow of an invite runs in a guest session that /join/start
// creates; /join/register/cosign replaces it with a user session. Every join
// call after the claim carries the claiming device's signature in the
// X-PCC-Join-Sig header (handshake protocol v2).
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

// JoinSigHeader carries the claiming device's signature of a join call.
const JoinSigHeader = "X-PCC-Join-Sig"

var (
	rGraph = apix.Read("graph_read")
	wI     = apix.Write("invite_handshake")
	// The client sends the ephemeral key, the sound reports and the
	// co-signature on its own as the handshake goes on, right after opening it
	// or each other: steps, without the minimum spacing.
	sI    = apix.Step("invite_handshake")
	aJoin = apix.Anon("join")
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

// EpubIn is POST /api/invites/handshakes/{hs}/epub.
type EpubIn struct {
	EI string `json:"e_i"`
}

// PeerOut is GET /api/invites/handshakes/{hs}/peer.
type PeerOut struct {
	EV string `json:"e_v"`
}

// JoinStartOut is POST /api/join/start: the guest session's CSRF token and
// what the join form must attest (D29).
type JoinStartOut struct {
	CSRF       string `json:"csrf"`
	TOSVersion int    `json:"tos_version"`
	MinAge     int    `json:"min_age"`
}

// AudioStartIn is POST /api/join/audio/start.
type AudioStartIn struct {
	Kmac string `json:"kmac"`
}

// OptionsOut carries WebAuthn creation options.
type OptionsOut struct {
	Options json.RawMessage `json:"options"`
}

// CredentialIn carries a WebAuthn registration response.
type CredentialIn struct {
	Credential json.RawMessage `json:"credential"`
}

// AssertionIn carries the signup ceremony's WebAuthn get response.
type AssertionIn struct {
	Assertion json.RawMessage `json:"assertion"`
}

func sig(r *http.Request) string { return r.Header.Get(JoinSigHeader) }

// Register registers the module routes on the /api router.
func Register(r chi.Router, d *api.Deps) {
	inv := d.Invites
	secure := d.Auth.SecureCookies()
	hs := func(r *http.Request) gate.Ref { return apix.Ref(r, "hs") }

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
	apix.Route(r, http.MethodPost, "/invites/handshakes/{hs}/epub", sI, func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[EpubIn](r, 0)
		if err != nil {
			return err
		}
		if err := inv.Epub(r.Context(), hs(r), in.EI); err != nil {
			return err
		}
		return apix.NoContent(w)
	})
	apix.Route(r, http.MethodGet, "/invites/handshakes/{hs}/frame", apix.Free, func(w http.ResponseWriter, r *http.Request) error {
		f, err := inv.Frame(r.Context(), hs(r))
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, f)
	})
	apix.Route(r, http.MethodGet, "/invites/handshakes/{hs}", apix.Free, func(w http.ResponseWriter, r *http.Request) error {
		st, err := inv.Status(r.Context(), hs(r))
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, st)
	})
	apix.Route(r, http.MethodGet, "/invites/handshakes/{hs}/peer", apix.Free, func(w http.ResponseWriter, r *http.Request) error {
		ev, err := inv.Peer(r.Context(), hs(r))
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, PeerOut{EV: ev})
	})
	apix.Route(r, http.MethodPost, "/invites/handshakes/{hs}/confirm", wI, func(w http.ResponseWriter, r *http.Request) error {
		if err := inv.Confirm(r.Context(), hs(r)); err != nil {
			return err
		}
		return apix.NoContent(w)
	})
	apix.Route(r, http.MethodDelete, "/invites/handshakes/{hs}", wI, func(w http.ResponseWriter, r *http.Request) error {
		if err := inv.Cancel(r.Context(), hs(r)); err != nil {
			return err
		}
		return apix.NoContent(w)
	})
	apix.Route(r, http.MethodPost, "/invites/handshakes/{hs}/report", sI, func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[invites.ReportIn](r, 0)
		if err != nil {
			return err
		}
		out, err := inv.Report(r.Context(), hs(r), in)
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, out)
	})
	apix.Route(r, http.MethodPost, "/invites/handshakes/{hs}/cosign", sI, func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[invites.CosignIn](r, WebAuthnMax)
		if err != nil {
			return err
		}
		if err := inv.Cosign(r.Context(), hs(r), in); err != nil {
			return err
		}
		return apix.NoContent(w)
	})

	// Join (the claiming device).
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
		in, err := apix.Decode[invites.ClaimIn](r, 0)
		if err != nil {
			return err
		}
		if err := inv.Claim(r.Context(), in); err != nil {
			return err
		}
		return apix.NoContent(w)
	})
	apix.Route(r, http.MethodGet, "/join/status", aJoin, func(w http.ResponseWriter, r *http.Request) error {
		st, err := inv.JoinStatus(r.Context(), sig(r))
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, st)
	})
	apix.Route(r, http.MethodPost, "/join/audio/start", aJoin, func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[AudioStartIn](r, 0)
		if err != nil {
			return err
		}
		out, err := inv.AudioStart(r.Context(), sig(r), in.Kmac)
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, out)
	})
	apix.Route(r, http.MethodPost, "/join/dual/frame", aJoin, func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[invites.DualIn](r, 0)
		if err != nil {
			return err
		}
		out, err := inv.DualFrame(r.Context(), sig(r), in)
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, out)
	})
	apix.Route(r, http.MethodPost, "/join/register/begin", aJoin, func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[invites.Registration](r, 0)
		if err != nil {
			return err
		}
		opts, err := inv.RegisterBegin(r.Context(), sig(r), in)
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
		out, err := inv.RegisterFinish(r.Context(), sig(r), in.Credential)
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, out)
	})
	apix.Route(r, http.MethodPost, "/join/register/cosign", aJoin, func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[AssertionIn](r, WebAuthnMax)
		if err != nil {
			return err
		}
		tok, me, err := inv.RegisterCosign(r.Context(), sig(r), in.Assertion)
		if err != nil {
			return err
		}
		csrf, err := d.Auth.CSRFForToken(r.Context(), tok)
		if err != nil {
			return err
		}
		httpx.SetSessionCookie(w, tok, auth.MaxAge("user"), secure)
		return apix.JSON(w, http.StatusCreated, gate.SessionView{Authenticated: true, Kind: "user", CSRF: csrf, Me: &me})
	})
}
