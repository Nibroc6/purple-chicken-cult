// Package authapi serves /api/session, /api/auth/* and /api/enroll/* (plan §5.3).
//
// Owner: P0-W01 → P1-W07. Enrollment tokens travel only in request bodies (the
// SPA reads them from the URL fragment), never in a path or query (security #13).
package authapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/api"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/apix"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/auth"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/dbsc"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/httpx"
)

// WebAuthnMax bounds a WebAuthn response body (plan §5.2).
const WebAuthnMax = 64 << 10

type credentialIn struct {
	Credential json.RawMessage `json:"credential"`
}

// passkeyIn finishes a passkey addition: the new credential (and its name),
// or — for a session that must co-sign — the intent and assertion of the
// PasskeyChange record.
type passkeyIn struct {
	Credential json.RawMessage `json:"credential,omitempty"`
	Name       string          `json:"name,omitempty"`
	Intent     gate.Ref        `json:"intent,omitempty"`
	Assertion  json.RawMessage `json:"assertion,omitempty"`
}

// cosignIn is the optional co-signature of a passkey removal.
type cosignIn struct {
	Intent    gate.Ref        `json:"intent"`
	Assertion json.RawMessage `json:"assertion"`
}

type enrollBeginIn struct {
	Token string `json:"token"`
}

type enrollFinishIn struct {
	Credential  json.RawMessage `json:"credential"`
	RemoveOld   bool            `json:"remove_old"`
	TOSVersion  int             `json:"tos_version"`
	AgeAttested bool            `json:"age_attested"`
}

type optionsOut struct {
	Options json.RawMessage `json:"options"`
}

type passkeysOut struct {
	Items []auth.PasskeyView `json:"items"`
}

// Register registers the module's routes on the /api router.
func Register(r chi.Router, d *api.Deps) {
	secure := d.Auth.SecureCookies()
	apix.Route(r, http.MethodGet, "/session", apix.Free, func(w http.ResponseWriter, r *http.Request) error {
		sv, err := d.Auth.Session(r.Context())
		if err != nil {
			return err
		}
		offer(w, d.Auth.DBSCOffer(r.Context(), ""))
		return apix.JSON(w, http.StatusOK, sv)
	})
	apix.Route(r, http.MethodPost, "/auth/logout", apix.Free, func(w http.ResponseWriter, r *http.Request) error {
		if err := d.Auth.Logout(r.Context()); err != nil {
			return err
		}
		httpx.ClearSessionCookie(w, secure)
		dbsc.ClearBoundCookie(w, secure)
		return apix.NoContent(w)
	})

	// Login (security #11): the ceremony lives in a fresh guest session.
	apix.Route(r, http.MethodPost, "/auth/login/begin", apix.Anon("login"), func(w http.ResponseWriter, r *http.Request) error {
		if _, err := apix.Decode[struct{}](r, 0); err != nil {
			return err
		}
		bv, err := d.Auth.LoginBegin(r.Context())
		if err != nil {
			return err
		}
		httpx.SetSessionCookie(w, bv.Token, auth.MaxAge("guest"), secure)
		return apix.JSON(w, http.StatusOK, bv)
	})
	apix.Route(r, http.MethodPost, "/auth/login/finish", apix.Anon("login"), func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[credentialIn](r, WebAuthnMax)
		if err != nil {
			return err
		}
		tok, sv, err := d.Auth.LoginFinish(r.Context(), in.Credential)
		if err != nil {
			return err
		}
		httpx.SetSessionCookie(w, tok, auth.MaxAge("user"), secure)
		offer(w, d.Auth.DBSCOffer(r.Context(), tok))
		return apix.JSON(w, http.StatusOK, sv)
	})

	// Step-up (D27): an assertion over the viewer's own credentials.
	apix.Route(r, http.MethodPost, "/auth/stepup/begin", apix.Step("account"), func(w http.ResponseWriter, r *http.Request) error {
		if _, err := apix.Decode[struct{}](r, 0); err != nil {
			return err
		}
		opts, err := d.Auth.StepUpBegin(r.Context())
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, optionsOut{Options: opts})
	})
	apix.Route(r, http.MethodPost, "/auth/stepup/finish", apix.Step("account"), func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[credentialIn](r, WebAuthnMax)
		if err != nil {
			return err
		}
		if err := d.Auth.StepUpFinish(r.Context(), in.Credential); err != nil {
			return err
		}
		return apix.NoContent(w)
	})

	// Passkeys.
	apix.Route(r, http.MethodGet, "/auth/passkeys", apix.Read("graph_read"), func(w http.ResponseWriter, r *http.Request) error {
		items, err := d.Auth.Passkeys(r.Context())
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, passkeysOut{Items: items})
	})
	apix.Route(r, http.MethodPost, "/auth/passkeys/begin", apix.Write("account"), func(w http.ResponseWriter, r *http.Request) error {
		if _, err := apix.Decode[struct{}](r, 0); err != nil {
			return err
		}
		opts, err := d.Auth.AddPasskeyBegin(r.Context())
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, optionsOut{Options: opts})
	})
	apix.Route(r, http.MethodPost, "/auth/passkeys/finish", apix.Step("account"), func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[passkeyIn](r, WebAuthnMax)
		if err != nil {
			return err
		}
		res, err := d.Auth.AddPasskeyFinish(r.Context(), in.Credential, in.Name, in.Intent, in.Assertion)
		if err != nil {
			return err
		}
		if res.Cosign != "" {
			return apix.JSON(w, http.StatusAccepted, res)
		}
		return apix.NoContent(w)
	})
	apix.Route(r, http.MethodDelete, "/auth/passkeys/{id}", apix.Write("account"), func(w http.ResponseWriter, r *http.Request) error {
		var in cosignIn
		if r.ContentLength != 0 {
			var err error
			if in, err = apix.Decode[cosignIn](r, WebAuthnMax); err != nil {
				return err
			}
		}
		if err := d.Auth.RemovePasskey(r.Context(), apix.Ref(r, "id"), in.Intent, in.Assertion); err != nil {
			return err
		}
		return apix.NoContent(w)
	})

	// Device Bound Session Credentials (P1): called by the browser itself; the
	// proof is the JWT in Sec-Session-Response (plan §5.3).
	apix.Route(r, http.MethodPost, "/auth/dbsc/register", apix.Anon("dbsc"), func(w http.ResponseWriter, r *http.Request) error {
		v, err := d.Auth.DBSCRegister(r.Context(), proof(r))
		if err != nil {
			return err
		}
		dbsc.SetBoundCookie(w, v.Token, secure)
		return apix.JSON(w, http.StatusOK, v.Config)
	})
	apix.Route(r, http.MethodPost, "/auth/dbsc/refresh", apix.Anon("dbsc"), func(w http.ResponseWriter, r *http.Request) error {
		v, err := d.Auth.DBSCRefresh(r.Context(), proof(r))
		if err != nil {
			return err
		}
		if v.Challenge != "" {
			w.Header().Set(dbsc.HeaderChallenge, v.Challenge)
			return apix.JSON(w, http.StatusUnauthorized, challengeOut{Error: "dbsc_challenge"})
		}
		dbsc.SetBoundCookie(w, v.Token, secure)
		return apix.JSON(w, http.StatusOK, v.Config)
	})

	// Enrollment (genesis and recovery): the token comes from the URL fragment.
	apix.Route(r, http.MethodPost, "/enroll/begin", apix.Anon("enroll"), func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[enrollBeginIn](r, 0)
		if err != nil {
			return err
		}
		ev, err := d.Auth.EnrollBegin(r.Context(), in.Token)
		if err != nil {
			return err
		}
		if ev.Token != "" {
			httpx.SetSessionCookie(w, ev.Token, auth.MaxAge("enrollment"), secure)
		}
		return apix.JSON(w, http.StatusOK, ev)
	})
	apix.Route(r, http.MethodPost, "/enroll/finish", apix.Anon("enroll"), func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[enrollFinishIn](r, WebAuthnMax)
		if err != nil {
			return err
		}
		tok, sv, err := d.Auth.EnrollFinish(r.Context(), in.Credential, in.RemoveOld, in.TOSVersion, in.AgeAttested)
		if err != nil {
			return err
		}
		httpx.SetSessionCookie(w, tok, auth.MaxAge("user"), secure)
		offer(w, d.Auth.DBSCOffer(r.Context(), tok))
		return apix.JSON(w, http.StatusOK, sv)
	})
}

// challengeOut is the body of a refresh that needs a proof first.
type challengeOut struct {
	Error string `json:"error"`
}

// offer sends a DBSC registration offer, if any.
func offer(w http.ResponseWriter, h string) {
	if h != "" {
		w.Header().Set(dbsc.HeaderRegistration, h)
	}
}

// proof is the DBSC JWT of a request (header Sec-Session-Response), bounded.
func proof(r *http.Request) []byte {
	v := r.Header.Get(dbsc.HeaderResponse)
	if len(v) > dbsc.MaxJWT {
		return nil
	}
	return []byte(strings.Trim(v, `"`))
}
