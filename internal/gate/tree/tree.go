// Package tree builds the family-tree view (ARCHITECTURE §10.1.4, §10.1.3):
// rows −2…+2 around a focal post, each ranked like a feed tier.
//
// Owner: P0-W08 → P1-W08. Scaffold stub: returns gate.ErrNotImplemented.
package tree

import (
	"context"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// Service is the tree module.
type Service struct{ d *core.Deps }

// New returns the service.
func New(d *core.Deps) *Service { return &Service{d: d} }

// Init does nothing yet.
func (s *Service) Init(context.Context) error { return nil }

// Open opens the tree around focal.
func (s *Service) Open(ctx context.Context, focal gate.Ref, preset string) (gate.TreeView, error) {
	return gate.TreeView{}, s.todo(ctx)
}

// Row re-centers / loads a row of a snapshot.
func (s *Service) Row(ctx context.Context, snapshot string, gen int, center gate.Ref) (gate.TreeRow, error) {
	return gate.TreeRow{}, s.todo(ctx)
}

// More pages through a row.
func (s *Service) More(ctx context.Context, snapshot string, gen int, center gate.Ref, cursor string) (gate.TreeRow, error) {
	return gate.TreeRow{}, s.todo(ctx)
}

func (s *Service) todo(ctx context.Context) error {
	if _, err := core.RequireUser(ctx); err != nil {
		return err
	}
	return gate.ErrNotImplemented
}
