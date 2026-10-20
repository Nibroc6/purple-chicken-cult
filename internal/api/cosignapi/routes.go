// Package cosignapi serves /api/cosign (plan §5.3): co-sign intents for the
// graph and account changes that need a passkey assertion (P1).
//
// Owner: P1-W01.
package cosignapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/api"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/apix"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
)

// BeginMax bounds the request body of POST /cosign.
const BeginMax = 4 << 10

type beginIn struct {
	Purpose string   `json:"purpose"`
	Target  gate.Ref `json:"target"`
}

// Register registers the module routes on the /api router.
func Register(r chi.Router, d *api.Deps) {
	apix.Route(r, http.MethodPost, "/cosign", apix.Write("account"), func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[beginIn](r, BeginMax)
		if err != nil {
			return err
		}
		iv, err := d.Cosign.Begin(r.Context(), in.Purpose, in.Target)
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, iv)
	})
}
