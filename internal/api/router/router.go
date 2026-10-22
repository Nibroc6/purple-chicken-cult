// Package router assembles the HTTP stack once (plan §5.2). Modules never edit it:
// each registers its own flat routes in internal/api/<module>api/routes.go.
//
// Owner: SCAFFOLD (edited once more by SCAFFOLD-1 for the P1 modules, which are
// already listed here).
package router

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/api"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/accountapi"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/apix"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/authapi"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/cosignapi"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/engageapi"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/feedapi"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/friendsapi"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/invitesapi"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/mediaapi"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/messagesapi"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/modapi"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/notifyapi"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/postsapi"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/pushapi"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/signalsapi"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/staticapi"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/testhooks"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/themeapi"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/treeapi"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/httpx"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/secheaders"
)

// MaxBody is the default request body limit; routes that need more decode with
// their own limit (posts 96 KiB, thread 2.5 MiB, WebAuthn 64 KiB, media 15 MiB).
const MaxBody = 16 << 20

// Modules lists every module Register in mount order (SCAFFOLD-1 appended P1).
var Modules = []func(chi.Router, *api.Deps){
	authapi.Register, invitesapi.Register, friendsapi.Register, postsapi.Register, engageapi.Register,
	feedapi.Register, treeapi.Register, notifyapi.Register, accountapi.Register, modapi.Register,
	testhooks.Register, // no-op unless Env == test && TestHooks
	// Phase 1 (SCAFFOLD-1).
	mediaapi.Register, pushapi.Register, signalsapi.Register, cosignapi.Register, themeapi.Register,
	// Direct messages (contract request dm-1).
	messagesapi.Register,
}

// New builds the HTTP handler with every module.
func New(d *api.Deps) http.Handler { return NewWith(d, Modules...) }

// NewWith builds the full middleware stack with only the given modules mounted
// under /api (apitest.Service mounts one module).
func NewWith(d *api.Deps, modules ...func(chi.Router, *api.Deps)) http.Handler {
	r := chi.NewRouter()
	r.Use(httpx.RequestID, httpx.RealIP(d.Cfg.TrustedProxies), httpx.AccessLog, httpx.Recover,
		secheaders.Middleware(secheaders.Options{Origin: d.Cfg.Origin, MediaOrigin: originOf(d.Cfg.MediaBaseURL), Prod: d.Cfg.Prod()}),
		httpx.MaxBytes(MaxBody), d.Auth.Middleware)
	staticapi.Register(r, d) // /robots.txt, /ai.txt, /.well-known/tdmrep.json, SPA fallback
	mediaapi.RegisterPublic(r, d)
	r.Route("/api", func(r chi.Router) {
		r.Use(d.Auth.APIGuard, apix.NoStore, apix.WithCharger(d.Limits), d.Automation.Middleware)
		for _, register := range modules {
			register(r, d)
		}
		r.NotFound(apix.NotFound) // unknown /api path ⇒ the same uniform 404
		r.MethodNotAllowed(apix.NotFound)
	})
	return r
}

// originOf returns scheme://host of u ("" when u is empty).
func originOf(u string) string {
	for i := 0; i+2 < len(u); i++ {
		if u[i] == ':' && u[i+1] == '/' && u[i+2] == '/' {
			for j := i + 3; j < len(u); j++ {
				if u[j] == '/' {
					return u[:j]
				}
			}
			return u
		}
	}
	return ""
}
