// Package feed builds the tiered feed (ARCHITECTURE §4.2, §4.3, §4.4, §5.6,
// §10.1.2, §10.1.7): per-tier candidate planning (author scan vs time scan),
// whole-tier ranking with rank.RankTier, viewer-keyed snapshots with HMAC'd
// cursors re-checked on every page, tier markers and "caught up".
//
// Owner: P0-W07 → P1-W08. Scaffold stub: returns gate.ErrNotImplemented.
package feed

import (
	"context"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// PageSize is the number of posts per feed page.
const PageSize = 20

// Service is the feed module.
type Service struct{ d *core.Deps }

// New returns the service.
func New(d *core.Deps) *Service { return &Service{d: d} }

// Init does nothing yet.
func (s *Service) Init(context.Context) error { return nil }

// Page returns a feed page; cursor "" starts a new session. Preset "top" is
// Problem preset_unavailable in Phase 0.
func (s *Service) Page(ctx context.Context, preset string, cursor string) (gate.FeedPage, error) {
	return gate.FeedPage{}, s.todo(ctx)
}

// Expand expands a collapsed group.
func (s *Service) Expand(ctx context.Context, token string) ([]gate.FeedItem, error) {
	return nil, s.todo(ctx)
}

// NewAbove counts new posts in closer tiers (polled every 60 s).
func (s *Service) NewAbove(ctx context.Context, session string) (int, int, error) {
	return 0, 0, s.todo(ctx)
}

// Why explains a post's position.
func (s *Service) Why(ctx context.Context, p gate.Ref) (gate.WhyView, error) {
	return gate.WhyView{}, s.todo(ctx)
}

func (s *Service) todo(ctx context.Context) error {
	if _, err := core.RequireUser(ctx); err != nil {
		return err
	}
	return gate.ErrNotImplemented
}
