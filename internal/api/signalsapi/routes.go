// Package signalsapi serves /api/signals (plan §5.3): the client's automation
// signal report — counts only, never content (ARCHITECTURE §17.2(4)).
//
// Owner: P1-W07.
package signalsapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/api"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/apix"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/signals/automation"
)

// Register registers the module routes on the /api router.
func Register(r chi.Router, d *api.Deps) {
	apix.Route(r, http.MethodPost, "/signals", apix.Free, func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[automation.ClientSignals](r, 0)
		if err != nil {
			return err
		}
		if err := d.Automation.Client(r.Context(), in); err != nil {
			return err
		}
		return apix.NoContent(w)
	})
}
