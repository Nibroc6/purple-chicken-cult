// Package modapi serves /api/reports, /api/blame, /api/mod/* and /api/csp-report (plan §5.3).
//
// Owner: P0-W12 → P1-W06. Scaffold: empty Register; the owning work package fills
// routes.go + handlers_*.go with apix.Route calls (flat patterns relative to /api).
package modapi

import (
	"github.com/go-chi/chi/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/api"
)

// Register registers the module routes on the /api router.
func Register(r chi.Router, d *api.Deps) {}
