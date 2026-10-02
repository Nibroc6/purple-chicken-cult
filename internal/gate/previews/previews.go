// Package previews builds text-only link previews server-side, per post, after a
// fixed delay bucket (ARCHITECTURE §10.1.6): job preview.fetch over
// platform/{mdscan,urlx,safefetch}, link_fetch_cache, per-host limiter.
//
// Owner: P0-W09 → P1-W11. Scaffold stub: no job registered yet.
package previews

import (
	"context"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// MaxPerPost is the number of previews per post.
const MaxPerPost = 3

// Service is the previews module.
type Service struct{ d *core.Deps }

// New returns the service.
func New(d *core.Deps) *Service { return &Service{d: d} }

// Init registers preview.fetch and the PostDeleted subscription in W09.
func (s *Service) Init(context.Context) error { return nil }
