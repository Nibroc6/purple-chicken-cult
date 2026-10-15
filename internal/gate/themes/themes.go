// Package themes stores token-only themes (ARCHITECTURE §15.2 A), validated
// server-side: WCAG AA for integrity elements, a size floor, a font allowlist.
//
// Owner: P1-W09. Scaffold stub: returns gate.ErrNotImplemented.
package themes

import (
	"context"
	"encoding/json"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// Service is the themes module.
type Service struct{ d *core.Deps }

// New returns the service.
func New(d *core.Deps) *Service { return &Service{d: d} }

// Init does nothing yet.
func (s *Service) Init(context.Context) error { return nil }

// Set validates and stores the viewer's theme tokens. Problems: theme_contrast,
// theme_font, theme_invalid.
func (s *Service) Set(ctx context.Context, tokens json.RawMessage) (json.RawMessage, error) {
	if _, err := core.RequireUser(ctx); err != nil {
		return nil, err
	}
	return nil, gate.ErrNotImplemented
}

// Clear resets the viewer's theme to the default.
func (s *Service) Clear(ctx context.Context) error {
	if _, err := core.RequireUser(ctx); err != nil {
		return err
	}
	return gate.ErrNotImplemented
}
