// Package graph is the in-memory friendship graph (ARCHITECTURE §4.1, §4.5, §4.6,
// §12). It implements core.Graph.
//
// Owner: P0-W02 → P1-W03. Scaffold stub: delegates to coreref.MemGraph loaded from
// edges / blocks / hide_via, so the app runs end-to-end before W02 lands.
package graph

import (
	"context"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coreref"
)

// Graph implements core.Graph.
type Graph struct {
	d   *core.Deps
	ref *coreref.MemGraph
}

var _ core.Graph = (*Graph)(nil)

// New returns the graph.
func New(d *core.Deps) *Graph {
	g := &Graph{d: d, ref: coreref.NewMemGraph()}
	if d.DB != nil {
		g.ref.Loader = func(ctx context.Context, m *coreref.MemGraph) error { return m.LoadFrom(ctx, d.DB.Q()) }
	}
	return g
}

// Init loads the graph.
func (g *Graph) Init(ctx context.Context) error { return g.Load(ctx) }

// Epoch returns the global removal epoch.
func (g *Graph) Epoch() uint64 { return g.ref.Epoch() }

// Friends returns u's edges sorted by (level desc, since asc, id asc).
func (g *Graph) Friends(u core.UserID) []core.Edge { return g.ref.Friends(u) }

// Level returns the level of a–b.
func (g *Graph) Level(a, b core.UserID) (core.EdgeLevel, bool) { return g.ref.Level(a, b) }

// Degree returns u's edge count.
func (g *Graph) Degree(u core.UserID) int { return g.ref.Degree(u) }

// Search runs a full BFS from v avoiding ex (Phase 0).
func (g *Graph) Search(ctx context.Context, v core.UserID, ex core.Exclusions) (core.Search, error) {
	return g.ref.Search(ctx, v, ex)
}

// AddEdge adds a–b (after commit).
func (g *Graph) AddEdge(a, b core.UserID, lvl core.EdgeLevel, since time.Time) {
	g.ref.AddEdge(a, b, lvl, since)
}

// RemoveEdge removes a–b and bumps the epoch.
func (g *Graph) RemoveEdge(a, b core.UserID) { g.ref.RemoveEdge(a, b) }

// SetBlock records a block (bumps the epoch on add).
func (g *Graph) SetBlock(blocker, blocked core.UserID, on bool) { g.ref.SetBlock(blocker, blocked, on) }

// SetHideVia records a hide-via (P1).
func (g *Graph) SetHideVia(user, via core.UserID, on bool) { g.ref.SetHideVia(user, via, on) }

// Exclusions returns v's own blocks and hide-vias.
func (g *Graph) Exclusions(v core.UserID) core.Exclusions { return g.ref.Exclusions(v) }

// Blocks reports whether a blocked b.
func (g *Graph) Blocks(a, b core.UserID) bool { return g.ref.Blocks(a, b) }

// Load (re)loads the graph from the database.
func (g *Graph) Load(ctx context.Context) error { return g.ref.Load(ctx) }
