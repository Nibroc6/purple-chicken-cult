// Package mediaapi serves /api/media/* (and RegisterPublic: the dev/test signed-media server /m/{key}) (plan §5.3).
//
// Owner: P1-W04. Scaffold: empty Register; the owning work package fills
// routes.go + handlers_*.go with apix.Route calls (flat patterns relative to /api).
package mediaapi

import (
	"github.com/go-chi/chi/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/api"
)

// Register registers the module routes on the /api router.
func Register(r chi.Router, d *api.Deps) {}

// RegisterPublic registers GET /m/{key} on the ROOT router (dev/test signed-media
// server; prod serves media from the CDN worker). Scaffold: nothing.
func RegisterPublic(r chi.Router, d *api.Deps) {}
