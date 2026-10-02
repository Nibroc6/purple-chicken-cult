// Package views builds every PostView / PersonView / AuthorView from core.Checked
// input only (plan §4.7 views). It implements core.Views.
//
// Owner: P0-W02 → P1-W12. Scaffold stub: delegates to coreref.MemViews (minimal
// but correct; no previews, mentions or media yet).
package views

import (
	"context"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coreref"
)

// Service implements core.Views.
type Service struct {
	d   *core.Deps
	ref *coreref.MemViews
}

var _ core.Views = (*Service)(nil)

// New returns the service.
func New(d *core.Deps) *Service { return &Service{d: d, ref: &coreref.MemViews{D: d}} }

// Init does nothing yet.
func (s *Service) Init(context.Context) error { return nil }

// Posts builds post views for checked posts.
func (s *Service) Posts(ctx context.Context, sr core.Search, ps []core.Checked, o core.ViewOpts) ([]gate.PostView, error) {
	return s.ref.Posts(ctx, sr, ps, o)
}

// Person builds a person view (ErrNotFound unless visible).
func (s *Service) Person(ctx context.Context, sr core.Search, u core.UserID) (gate.PersonView, error) {
	return s.ref.Person(ctx, sr, u)
}

// Author builds a compact author view.
func (s *Service) Author(ctx context.Context, sr core.Search, u core.UserID) (gate.AuthorView, error) {
	return s.ref.Author(ctx, sr, u)
}
