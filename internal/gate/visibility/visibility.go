// Package visibility implements the horizon rules (ARCHITECTURE §2.2, §4, §10.1.3):
// core.Visibility and core.Chains. Blocks are one-directional (D7).
//
// Owner: P0-W02 → P1-W03. Scaffold stub: delegates to coreref.MemVisibility and
// coreref.MemChains (which are exact).
package visibility

import (
	"context"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coreref"
)

// Service implements core.Visibility and core.Chains.
type Service struct {
	d      *core.Deps
	vis    *coreref.MemVisibility
	chains *coreref.MemChains
}

var (
	_ core.Visibility = (*Service)(nil)
	_ core.Chains     = (*Service)(nil)
)

// New returns the service.
func New(d *core.Deps) *Service {
	return &Service{d: d, vis: &coreref.MemVisibility{D: d}, chains: &coreref.MemChains{D: d}}
}

// Init does nothing yet.
func (s *Service) Init(context.Context) error { return nil }

// SearchFor runs the viewer's search with their own exclusions.
func (s *Service) SearchFor(ctx context.Context, v core.UserID) (core.Search, error) {
	return s.vis.SearchFor(ctx, v)
}

// CanSeeUser ⇔ finite distance.
func (s *Service) CanSeeUser(sr core.Search, u core.UserID) bool { return s.vis.CanSeeUser(sr, u) }

// CheckPost applies §4 rules 1, 4, 5.
func (s *Service) CheckPost(ctx context.Context, sr core.Search, p core.PostMeta) (core.Checked, bool) {
	return s.vis.CheckPost(ctx, sr, p)
}

// CheckPosts is an order-preserving filter.
func (s *Service) CheckPosts(ctx context.Context, sr core.Search, ps []core.PostMeta) []core.Checked {
	return s.vis.CheckPosts(ctx, sr, ps)
}

// CanSeeLink is §10.1.3.
func (s *Service) CanSeeLink(ctx context.Context, sr core.Search, l core.LinkMeta, child, parent core.PostMeta) bool {
	return s.vis.CanSeeLink(ctx, sr, l, child, parent)
}

// VisibleChildren returns visible child links (limit after filtering).
func (s *Service) VisibleChildren(ctx context.Context, sr core.Search, parent core.PostMeta, f core.ChildFilter) []core.VisibleLink {
	return s.vis.VisibleChildren(ctx, sr, parent, f)
}

// VisibleParents returns visible parent links.
func (s *Service) VisibleParents(ctx context.Context, sr core.Search, child core.PostMeta) []core.VisibleLink {
	return s.vis.VisibleParents(ctx, sr, child)
}

// CanReference applies the reference policy.
func (s *Service) CanReference(ctx context.Context, sr core.Search, parent core.PostMeta, kind core.LinkKind) bool {
	return s.vis.CanReference(ctx, sr, parent, kind)
}

// Chain builds the §4.5 chain.
func (s *Service) Chain(ctx context.Context, sr core.Search, u core.UserID) (gate.ChainView, error) {
	return s.chains.Chain(ctx, sr, u)
}
