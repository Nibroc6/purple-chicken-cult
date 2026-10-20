// Package automation scores automation signals (ARCHITECTURE §17.2(4)): client
// reports (webdriver, CDP hint, input-event COUNTS only) and request patterns
// (sustained rate without idle, reads without seen events, BFS-like tree walks,
// chain expansion speed). Crossing a threshold sets sessions.stepup_required
// (core.StepUp) ⇒ gate.ErrStepUp. Signals never ban, and screen-reader /
// keyboard-only profiles are never scored.
//
// Owner: P1-W07. Scaffold stub: accepts and drops client reports.
package automation

import (
	"context"
	"net/http"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// Service is the automation-signals module.
type Service struct{ d *core.Deps }

// New returns the service.
func New(d *core.Deps) *Service { return &Service{d: d} }

// Init does nothing yet.
func (s *Service) Init(context.Context) error { return nil }

// ClientSignals is POST /api/signals (counts only, never content).
type ClientSignals struct {
	Webdriver    bool  `json:"webdriver"`
	CDP          bool  `json:"cdp"`
	InputsPerMin []int `json:"inputs_per_min"`
	VisibleMs    int64 `json:"visible_ms"`
}

// Client records a client signal report.
func (s *Service) Client(ctx context.Context, r ClientSignals) error {
	if _, err := core.RequireUser(ctx); err != nil {
		return err
	}
	return nil
}

// Reset forgets every in-memory scoring window (test hooks; contract request
// p1-w07-1). Stub: nothing is held yet.
func (s *Service) Reset() {}

// Middleware feeds request patterns to the scorer (P1-W07). Scaffold: pass-through.
func (s *Service) Middleware(next http.Handler) http.Handler { return next }
