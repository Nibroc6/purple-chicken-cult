// Package behavior runs the nightly behavioral-signal jobs (ARCHITECTURE §9, §8
// metrics): posting cadence, cross-account similarity (MinHash within invite
// subtrees; bodies never stored), invite-tree clustering ⇒ mod_queue.
// Cross-account correlation uses net_h only (HMAC of the /24 or /56 under a daily
// salt) — no IPs at rest. Signals never ban.
//
// Owner: P1-W06. Scaffold stub: no jobs registered.
package behavior

import (
	"context"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// Service is the behavioral-signals module.
type Service struct{ d *core.Deps }

// New returns the service.
func New(d *core.Deps) *Service { return &Service{d: d} }

// Init registers signals.nightly / signals.purge in P1-W06.
func (s *Service) Init(context.Context) error { return nil }
