// Package accountapi serves /api/me/*, /api/keys, /api/mod/recovery/* and (P1)
// /api/recovery/* (plan §5.3).
//
// Owner: P0-W11 → P1-W10.
package accountapi

import (
	"bytes"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/api"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/apix"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/httpx"
)

var (
	free     = apix.Free
	wAccount = apix.Write("account")
	rGraph   = apix.Read("graph_read")
)

// Body limits.
const (
	maxSettingsBody = 8 << 10
	maxSmallBody    = 1 << 10
)

// KeysView is GET /api/keys.
type KeysView struct {
	Server []gate.KeyView `json:"server"`
}

// RecoveryView is GET /api/me/recovery (the banner).
type RecoveryView struct {
	PendingUntil *time.Time `json:"pending_until,omitempty"`
}

// DeleteIn is POST /api/me/delete.
type DeleteIn struct {
	ConfirmHandle string `json:"confirm_handle"`
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
	apix.Route(r, http.MethodPatch, "/me/settings", wAccount, func(w http.ResponseWriter, r *http.Request) error {
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
		in, err := apix.Decode[DeleteIn](r, maxSmallBody)
		if err != nil {
			return err
		}
		if err := d.Account.Delete(r.Context(), in.ConfirmHandle); err != nil {
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
		return apix.JSON(w, http.StatusOK, KeysView{Server: keys})
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
		if err := d.Account.RecoveryApprove(r.Context(), apix.Ref(r, "id")); err != nil {
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
}
