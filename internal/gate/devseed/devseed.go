// Package devseed creates synthetic graphs/posts for dev and perf tests and loads
// the named e2e fixtures testdata/fixtures/<wp-id>.json. It refuses unless
// cfg.DevLike(). It may import gate/graph (Synth; same owner, archtest A8).
//
// Owner: P0-W02 → P1-W03. Scaffold stub: returns gate.ErrNotImplemented.
package devseed

import (
	"context"
	"errors"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// ErrNotDev is returned outside dev/test.
var ErrNotDev = errors.New("devseed: refused outside PCC_ENV=dev|test")

// Service is the devseed module.
type Service struct{ d *core.Deps }

// New returns the service.
func New(d *core.Deps) *Service { return &Service{d: d} }

// Init does nothing.
func (s *Service) Init(context.Context) error { return nil }

// Synthetic seeds n users with a "ba" (Barabási–Albert) or "ws" (Watts–Strogatz) graph.
func (s *Service) Synthetic(ctx context.Context, n int, model string, seed int64) error {
	if !s.d.Cfg.DevLike() {
		return ErrNotDev
	}
	return gate.ErrNotImplemented
}

// LoadFixture loads testdata/fixtures/<name>.json.
func (s *Service) LoadFixture(ctx context.Context, name string) error {
	if !s.d.Cfg.DevLike() {
		return ErrNotDev
	}
	return gate.ErrNotImplemented
}
