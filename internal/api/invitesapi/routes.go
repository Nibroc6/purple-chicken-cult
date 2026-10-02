// Package invitesapi serves /api/invites/* and /api/join/* (plan §5.3).
//
// Owner: P0-W04 → P1-W02. Scaffold: empty Register; the owning work package fills
// routes.go + handlers_*.go with apix.Route calls (flat patterns relative to /api).
package invitesapi

import (
	"github.com/go-chi/chi/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/api"
)

// Register registers the module routes on the /api router.
func Register(r chi.Router, d *api.Deps) {}
