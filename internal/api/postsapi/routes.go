// Package postsapi serves /api/posts (create, thread, get, edit, delete, ref-policy, detach) (plan §5.3).
//
// Owner: P0-W05 → P1-W12. Scaffold: empty Register; the owning work package fills
// routes.go + handlers_*.go with apix.Route calls (flat patterns relative to /api).
package postsapi

import (
	"github.com/go-chi/chi/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/api"
)

// Register registers the module routes on the /api router.
func Register(r chi.Router, d *api.Deps) {}
