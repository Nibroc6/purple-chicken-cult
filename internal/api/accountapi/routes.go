// Package accountapi serves /api/me/*, /api/keys, /api/mod/recovery/* and (P1)
// /api/recovery/* (plan §5.3). Deviations from the plan's table (P1-W10):
// POST /api/recovery/claim answers 200 {ticket} rather than 204, and GET
// /api/recovery/status takes that ticket in X-PCC-Recovery and answers the
// enrollment URL once released, because the recovering device's guest session
// (30 minutes) cannot outlive the 24 h hold.
//
// Owner: P0-W11 → P1-W10.
package accountapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/api"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/apix"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/account"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/httpx"
)

var (
	free      = apix.Free
	wAccount  = apix.Write("account")
	wSettings = apix.Write("settings")
	rGraph    = apix.Read("graph_read")
	aRecovery = apix.Anon("recovery")
)

// Body limits.
const (
	maxSettingsBody = 8 << 10
	maxSmallBody    = 1 << 10
	maxCosignBody   = 16 << 10
	maxClaimBody    = 4 << 10
)

// KeysView is GET /api/keys: the server's keys and (P1) the caller's own
// custodial signing keys.
type KeysView struct {
	Server []gate.KeyView `json:"server"`
	Mine   []gate.KeyView `json:"mine,omitempty"`
}

// RecoveryView is GET /api/me/recovery (the banner).
type RecoveryView struct {
	PendingUntil *time.Time `json:"pending_until,omitempty"`
}

// DeleteIn is POST /api/me/delete; a passkey session co-signs (P1).
type DeleteIn struct {
	ConfirmHandle string          `json:"confirm_handle"`
	Intent        gate.Ref        `json:"intent,omitempty"`
	Assertion     json.RawMessage `json:"assertion,omitempty"`
}

// CosignIn is the co-signature of a change: {intent, assertion} (P1).
type CosignIn struct {
	Intent    gate.Ref        `json:"intent,omitempty"`
	Assertion json.RawMessage `json:"assertion,omitempty"`
}

func (c CosignIn) cosig() *account.Cosig {
	return &account.Cosig{Intent: c.Intent, Assertion: c.Assertion}
}

// RecoveryStartIn is POST /api/recovery/sessions.
type RecoveryStartIn struct {
	Friend gate.Ref `json:"friend"`
}

// RecoveryIn is POST /api/mod/recovery.
type RecoveryIn struct {
	Subject gate.Ref `json:"subject"`
}

// RecoveryCreated is the 201 answer of POST /api/mod/recovery.
type RecoveryCreated struct {
	ID gate.Ref `json:"id"`
}

// RecoveryLinkView is GET /api/mod/recovery/{id}/link.
type RecoveryLinkView struct {
	EnrollURL string `json:"enroll_url"`
}

// Register registers the module routes on the /api router.
func Register(r chi.Router, d *api.Deps) {
	apix.Route(r, http.MethodGet, "/me", free, func(w http.ResponseWriter, r *http.Request) error {
		me, err := d.Account.Me(r.Context())
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, me)
	})
	apix.Route(r, http.MethodPatch, "/me/settings", wSettings, func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[gate.SettingsPatch](r, maxSettingsBody)
		if err != nil {
			return err
		}
		sv, err := d.Account.UpdateSettings(r.Context(), in)
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, sv)
	})
	apix.Route(r, http.MethodPost, "/me/export", wAccount, func(w http.ResponseWriter, r *http.Request) error {
		if _, err := apix.Decode[struct{}](r, maxSmallBody); err != nil {
			return err
		}
		var buf bytes.Buffer
		if err := d.Account.Export(r.Context(), &buf); err != nil {
			return err
		}
		h := w.Header()
		h.Set("Content-Type", "application/zip")
		h.Set("Cache-Control", "no-store")
		h.Set("Content-Disposition", `attachment; filename="pcc-export.zip"`)
		h.Set("Content-Length", strconv.Itoa(buf.Len()))
		w.WriteHeader(http.StatusOK)
		_, err := w.Write(buf.Bytes())
		return err
	})
	apix.Route(r, http.MethodPost, "/me/delete", wAccount, func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[DeleteIn](r, maxCosignBody)
		if err != nil {
			return err
		}
		c := CosignIn{Intent: in.Intent, Assertion: in.Assertion}
		if err := d.Account.Delete(r.Context(), in.ConfirmHandle, c.cosig()); err != nil {
			return err
		}
		httpx.ClearSessionCookie(w, d.Auth.SecureCookies())
		return apix.JSON(w, http.StatusAccepted, nil)
	})
	apix.Route(r, http.MethodGet, "/me/recovery", free, func(w http.ResponseWriter, r *http.Request) error {
		at, err := d.Account.RecoveryPending(r.Context())
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, RecoveryView{PendingUntil: at})
	})
	apix.Route(r, http.MethodPost, "/me/recovery/cancel", wAccount, func(w http.ResponseWriter, r *http.Request) error {
		if err := d.Account.RecoveryCancel(r.Context()); err != nil {
			return err
		}
		return apix.NoContent(w)
	})
	apix.Route(r, http.MethodGet, "/keys", rGraph, func(w http.ResponseWriter, r *http.Request) error {
		keys, err := d.Records.PublishedKeys(r.Context())
		if err != nil {
			return err
		}
		mine, err := d.Records.MyKeys(r.Context())
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, KeysView{Server: keys, Mine: mine})
	})
	apix.Route(r, http.MethodPost, "/mod/recovery", wAccount, func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[RecoveryIn](r, maxSmallBody)
		if err != nil {
			return err
		}
		id, err := d.Account.RecoveryRequest(r.Context(), in.Subject)
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusCreated, RecoveryCreated{ID: id})
	})
	apix.Route(r, http.MethodPost, "/mod/recovery/{id}/approve", wAccount, func(w http.ResponseWriter, r *http.Request) error {
		in, err := decodeCosign(r)
		if err != nil {
			return err
		}
		if err := d.Account.RecoveryApprove(r.Context(), apix.Ref(r, "id"), in.cosig()); err != nil {
			return err
		}
		return apix.NoContent(w)
	})
	apix.Route(r, http.MethodGet, "/mod/recovery/{id}/link", wAccount, func(w http.ResponseWriter, r *http.Request) error {
		u, err := d.Account.RecoveryLink(r.Context(), apix.Ref(r, "id"))
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, RecoveryLinkView{EnrollURL: u})
	})
	registerGuardian(r, d)
}

// decodeCosign reads an optional {intent, assertion} body (an empty body is
// no co-signature).
func decodeCosign(r *http.Request) (CosignIn, error) {
	if r.ContentLength == 0 {
		return CosignIn{}, nil
	}
	return apix.Decode[CosignIn](r, maxCosignBody)
}

// registerGuardian registers the P1 guardian recovery routes.
func registerGuardian(r chi.Router, d *api.Deps) {
	apix.Route(r, http.MethodPost, "/recovery/sessions", wAccount, func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[RecoveryStartIn](r, maxSmallBody)
		if err != nil {
			return err
		}
		hv, err := d.Account.RecoveryStart(r.Context(), in.Friend)
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusCreated, hv)
	})
	apix.Route(r, http.MethodPost, "/recovery/claim", aRecovery, func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[json.RawMessage](r, maxClaimBody)
		if err != nil {
			return err
		}
		out, err := d.Account.RecoveryClaim(r.Context(), in)
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, out)
	})
	apix.Route(r, http.MethodGet, "/recovery/requests", rGraph, func(w http.ResponseWriter, r *http.Request) error {
		out, err := d.Account.RecoveryRequests(r.Context())
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, out)
	})
	apix.Route(r, http.MethodPost, "/recovery/requests/{id}/approve", wAccount, func(w http.ResponseWriter, r *http.Request) error {
		in, err := decodeCosign(r)
		if err != nil {
			return err
		}
		if err := d.Account.RecoveryGuardianApprove(r.Context(), apix.Ref(r, "id"), in.cosig()); err != nil {
			return err
		}
		return apix.NoContent(w)
	})
	apix.Route(r, http.MethodGet, "/recovery/status", aRecovery, func(w http.ResponseWriter, r *http.Request) error {
		st, err := d.Account.RecoveryStatus(r.Context(), r.Header.Get(account.TicketHeader))
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, st)
	})
}
