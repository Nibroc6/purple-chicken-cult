// Package cosignapi serves POST /api/cosign (plan §5.3): it opens a co-sign
// intent for one of the signed-in person's graph or account changes and
// returns the record to show, the intent ref and the WebAuthn get options.
// The assertion itself travels with the change's own request
// ({intent, assertion}) and is verified there through core.Cosign.Require.
//
// Owner: P1-W01.
package cosignapi

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/api"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/apix"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
)

// A co-sign intent carries on a change the person already started (often right
// after that change's own request), so it is a step: no minimum spacing.
var sAccount = apix.Step("account")

// maxBody bounds POST /api/cosign.
const maxBody = 2 << 10

// BeginIn is POST /api/cosign.
type BeginIn struct {
	Purpose string   `json:"purpose"`
	Target  gate.Ref `json:"target,omitempty"`
}

// IntentOut is the answer of POST /api/cosign.
type IntentOut struct {
	Intent  gate.Ref        `json:"intent"`
	Record  json.RawMessage `json:"record"`
	Options json.RawMessage `json:"options"`
}

// Register registers the module routes on the /api router.
func Register(r chi.Router, d *api.Deps) {
	apix.Route(r, http.MethodPost, "/cosign", sAccount, func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[BeginIn](r, maxBody)
		if err != nil {
			return err
		}
		iv, err := d.Cosign.Begin(r.Context(), in.Purpose, in.Target)
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, IntentOut{Intent: iv.Intent, Record: iv.Record, Options: iv.Options})
	})
}
