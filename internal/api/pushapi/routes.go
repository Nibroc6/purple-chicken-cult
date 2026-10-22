// Package pushapi serves /api/push/* (plan §5.3): the VAPID key, and the
// caller's push subscriptions. Push fails closed without VAPID keys (D20):
// /push/key answers {enabled:false} and subscribing is feature_disabled.
//
// Owner: P1-W05.
package pushapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/api"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/apix"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
)

var wAccount = apix.Write("account")

// KeyOut is the answer to GET /api/push/key.
type KeyOut struct {
	Enabled bool   `json:"enabled"`
	Key     string `json:"key,omitempty"`
}

// EndpointIn is the body of DELETE /api/push/subscriptions.
type EndpointIn struct {
	Endpoint string `json:"endpoint"`
}

// Register registers the module routes on the /api router.
func Register(r chi.Router, d *api.Deps) {
	ns := d.Notify
	apix.Route(r, http.MethodGet, "/push/key", apix.Free, func(w http.ResponseWriter, r *http.Request) error {
		key, ok := ns.PushKey(r.Context())
		return apix.JSON(w, http.StatusOK, KeyOut{Enabled: ok, Key: key})
	})
	apix.Route(r, http.MethodGet, "/push/subscriptions", apix.Free, func(w http.ResponseWriter, r *http.Request) error {
		st, err := ns.SubscriptionState(r.Context(), r.URL.Query().Get("endpoint"))
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, st)
	})
	apix.Route(r, http.MethodPost, "/push/subscriptions", wAccount, func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[gate.PushSub](r, 8<<10)
		if err != nil {
			return err
		}
		if err := ns.Subscribe(r.Context(), in); err != nil {
			return err
		}
		return apix.NoContent(w)
	})
	apix.Route(r, http.MethodDelete, "/push/subscriptions", wAccount, func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[EndpointIn](r, 8<<10)
		if err != nil {
			return err
		}
		if err := ns.Unsubscribe(r.Context(), in.Endpoint); err != nil {
			return err
		}
		return apix.NoContent(w)
	})
}
