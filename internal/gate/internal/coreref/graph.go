// Package coreref holds correct, simple, in-memory reference implementations of
// every core interface (plan §4.5). They are production-safe (no `testing`
// import). Every scaffold domain stub delegates to its coreref counterpart until
// the owning work package replaces it, so the app and module tests are
// meaningful from the scaffold commit on. They are also the oracles that the
// real implementations are tested against.
package coreref

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/RoaringBitmap/roaring/v2"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// MemGraph is a map-based graph with a plain layered BFS implementing the D22
// first-hop rule and one-directional (reader-only) exclusions (D7).
type MemGraph struct {
	mu      sync.RWMutex
	adj     map[core.UserID]map[core.UserID]core.Edge
	blocks  map[core.UserID]map[core.UserID]struct{}
	hideVia map[core.UserID]map[core.UserID]struct{}
	epoch   atomic.Uint64

	// Loader, when set, is called by Load (e.g. LoadFrom over the database).
	Loader func(ctx context.Context, g *MemGraph) error
}

var _ core.Graph = (*MemGraph)(nil)

// NewMemGraph returns an empty graph.
func NewMemGraph() *MemGraph {
	return &MemGraph{
		adj:     map[core.UserID]map[core.UserID]core.Edge{},
		blocks:  map[core.UserID]map[core.UserID]struct{}{},
		hideVia: map[core.UserID]map[core.UserID]struct{}{},
	}
}

// Epoch is bumped by every removal, block add and hide-via add.
func (g *MemGraph) Epoch() uint64 { return g.epoch.Load() }

// Friends returns u's edges sorted by (level desc, since asc, id asc).
func (g *MemGraph) Friends(u core.UserID) []core.Edge {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]core.Edge, 0, len(g.adj[u]))
	for _, e := range g.adj[u] {
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, b core.Edge) int {
		if a.Level != b.Level {
			return int(b.Level) - int(a.Level)
		}
		if c := a.Since.Compare(b.Since); c != 0 {
			return c
		}
		return cmpID(a.To, b.To)
	})
	return out
}

func cmpID(a, b core.UserID) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// Level returns the level of the edge a–b.
func (g *MemGraph) Level(a, b core.UserID) (core.EdgeLevel, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	e, ok := g.adj[a][b]
	return e.Level, ok
}

// Degree returns u's number of edges.
func (g *MemGraph) Degree(u core.UserID) int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.adj[u])
}

// AddEdge adds or updates the edge a–b in both directions (no epoch bump).
func (g *MemGraph) AddEdge(a, b core.UserID, lvl core.EdgeLevel, since time.Time) {
	if a == b {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, p := range [][2]core.UserID{{a, b}, {b, a}} {
		m := g.adj[p[0]]
		if m == nil {
			m = map[core.UserID]core.Edge{}
			g.adj[p[0]] = m
		}
		if old, ok := m[p[1]]; ok {
			since = old.Since // an upgrade keeps the original edge age
		}
		m[p[1]] = core.Edge{To: p[1], Level: lvl, Since: since}
	}
}

// RemoveEdge removes a–b and bumps the epoch.
func (g *MemGraph) RemoveEdge(a, b core.UserID) {
	g.mu.Lock()
	delete(g.adj[a], b)
	delete(g.adj[b], a)
	g.mu.Unlock()
	g.epoch.Add(1)
}

func setRel(m map[core.UserID]map[core.UserID]struct{}, a, b core.UserID, on bool) bool {
	inner := m[a]
	if on {
		if inner == nil {
			inner = map[core.UserID]struct{}{}
			m[a] = inner
		}
		_, had := inner[b]
		inner[b] = struct{}{}
		return !had
	}
	delete(inner, b)
	return false
}

// SetBlock records that blocker blocked blocked (one direction); bumps the epoch on add.
func (g *MemGraph) SetBlock(blocker, blocked core.UserID, on bool) {
	g.mu.Lock()
	added := setRel(g.blocks, blocker, blocked, on)
	g.mu.Unlock()
	if added {
		g.epoch.Add(1)
	}
}

// SetHideVia records a hide-via (P1); bumps the epoch on add.
func (g *MemGraph) SetHideVia(user, via core.UserID, on bool) {
	g.mu.Lock()
	added := setRel(g.hideVia, user, via, on)
	g.mu.Unlock()
	if added {
		g.epoch.Add(1)
	}
}

// Exclusions returns v's own blocks and hide-vias.
func (g *MemGraph) Exclusions(v core.UserID) core.Exclusions {
	g.mu.RLock()
	defer g.mu.RUnlock()
	ex := core.Exclusions{Blocked: roaring.New(), HiddenVia: roaring.New()}
	for b := range g.blocks[v] {
		ex.Blocked.Add(uint32(b))
	}
	for h := range g.hideVia[v] {
		ex.HiddenVia.Add(uint32(h))
	}
	return ex
}

// Blocks reports whether a has blocked b.
func (g *MemGraph) Blocks(a, b core.UserID) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	_, ok := g.blocks[a][b]
	return ok
}

// Load calls Loader when set.
func (g *MemGraph) Load(ctx context.Context) error {
	if g.Loader == nil {
		return nil
	}
	return g.Loader(ctx, g)
}

// LoadFrom replaces the graph with the edges, blocks and hide-vias in the database.
func (g *MemGraph) LoadFrom(ctx context.Context, q store.Q) error {
	adj := map[core.UserID]map[core.UserID]core.Edge{}
	blocks := map[core.UserID]map[core.UserID]struct{}{}
	hide := map[core.UserID]map[core.UserID]struct{}{}

	rows, err := q.Query(ctx, "SELECT user_id, friend_id, level, since FROM edges")
	if err != nil {
		return err
	}
	for rows.Next() {
		var a, b int64
		var lvl int16
		var since time.Time
		if err := rows.Scan(&a, &b, &lvl, &since); err != nil {
			rows.Close()
			return err
		}
		m := adj[core.UserID(a)]
		if m == nil {
			m = map[core.UserID]core.Edge{}
			adj[core.UserID(a)] = m
		}
		m[core.UserID(b)] = core.Edge{To: core.UserID(b), Level: core.EdgeLevel(lvl), Since: since}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, t := range []struct {
		sql string
		m   map[core.UserID]map[core.UserID]struct{}
	}{
		{"SELECT blocker, blocked FROM blocks", blocks},
		{"SELECT user_id, via_id FROM hide_via", hide},
	} {
		rows, err := q.Query(ctx, t.sql)
		if err != nil {
			return err
		}
		for rows.Next() {
			var a, b int64
			if err := rows.Scan(&a, &b); err != nil {
				rows.Close()
				return err
			}
			setRel(t.m, core.UserID(a), core.UserID(b), true)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}
	g.mu.Lock()
	g.adj, g.blocks, g.hideVia = adj, blocks, hide
	g.mu.Unlock()
	g.epoch.Add(1)
	return nil
}

// Search runs a full layered BFS from v (Phase 0 behavior).
//
// D22: a node w in layer k+1 reachable from several predecessors keeps the first
// hop with the best rank (friend+ first, then the reader's oldest edge, then lower
// ID); among predecessors with the same first hop the lower predecessor ID wins.
// This is order-independent. Blocked users (the reader's own blocks) are never
// visited; hidden-via users are reached but never routed through.
func (g *MemGraph) Search(ctx context.Context, v core.UserID, ex core.Exclusions) (core.Search, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	s := &memSearch{
		viewer: v, epoch: g.epoch.Load(),
		dist: map[core.UserID]int{v: 0}, first: map[core.UserID]core.UserID{}, parent: map[core.UserID]core.UserID{},
		own: map[core.UserID]core.Edge{},
	}
	for f, e := range g.adj[v] {
		s.own[f] = e
	}
	blocked := func(u core.UserID) bool { return ex.Blocked != nil && ex.Blocked.Contains(uint32(u)) }
	hidden := func(u core.UserID) bool { return ex.HiddenVia != nil && ex.HiddenVia.Contains(uint32(u)) }

	layer := []core.UserID{v}
	s.layers = append(s.layers, layer)
	for k := 0; len(layer) > 0; k++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var next []core.UserID
		for _, u := range layer {
			if k > 0 && hidden(u) {
				continue // transit only: reachable, never routed through
			}
			for w := range g.adj[u] {
				if w == v || blocked(w) {
					continue
				}
				d, seen := s.dist[w]
				switch {
				case !seen:
					s.dist[w] = k + 1
					next = append(next, w)
					if k == 0 {
						s.first[w], s.parent[w] = w, v
					} else {
						s.first[w], s.parent[w] = s.first[u], u
					}
				case d == k+1 && k > 0:
					fu, fw := s.first[u], s.first[w]
					if s.betterFirst(fu, fw) {
						s.first[w], s.parent[w] = fu, u
					} else if fu == fw && u < s.parent[w] {
						s.parent[w] = u
					}
				}
			}
		}
		slices.Sort(next)
		layer = next
		if len(next) > 0 {
			s.layers = append(s.layers, next)
		}
	}
	return s, nil
}

type memSearch struct {
	viewer core.UserID
	epoch  uint64
	dist   map[core.UserID]int
	first  map[core.UserID]core.UserID
	parent map[core.UserID]core.UserID
	own    map[core.UserID]core.Edge // the reader's own edges
	layers [][]core.UserID
}

// betterFirst ranks first hops by the reader's own edge only (D22).
func (s *memSearch) betterFirst(a, b core.UserID) bool {
	ea, eb := s.own[a], s.own[b]
	ra, rb := 1, 1
	if ea.Level == core.LevelFriendPlus {
		ra = 0
	}
	if eb.Level == core.LevelFriendPlus {
		rb = 0
	}
	if ra != rb {
		return ra < rb
	}
	if !ea.Since.Equal(eb.Since) {
		return ea.Since.Before(eb.Since)
	}
	return a < b
}

func (s *memSearch) Viewer() core.UserID { return s.viewer }
func (s *memSearch) Epoch() uint64       { return s.epoch }

func (s *memSearch) Dist(u core.UserID) (int, bool) {
	d, ok := s.dist[u]
	return d, ok
}

func (s *memSearch) FirstHop(u core.UserID) core.UserID {
	if d, ok := s.dist[u]; !ok || d <= 1 {
		return 0
	}
	return s.first[u]
}

func (s *memSearch) FirstHopPlus(u core.UserID) bool {
	d, ok := s.dist[u]
	if !ok || d == 0 {
		return false
	}
	return s.own[s.first[u]].Level == core.LevelFriendPlus
}

func (s *memSearch) Path(u core.UserID) []core.UserID {
	if _, ok := s.dist[u]; !ok {
		return nil
	}
	var p []core.UserID
	for cur := u; cur != s.viewer; cur = s.parent[cur] {
		p = append(p, cur)
	}
	p = append(p, s.viewer)
	slices.Reverse(p)
	return p
}

func (s *memSearch) Layer(k int) []core.UserID {
	if k < 0 || k >= len(s.layers) {
		return nil
	}
	return slices.Clone(s.layers[k])
}

func (s *memSearch) LayerBitmap(k int) *roaring.Bitmap {
	b := roaring.New()
	for _, u := range s.Layer(k) {
		b.Add(uint32(u))
	}
	return b
}

func (s *memSearch) MaxLayer() int { return len(s.layers) - 1 }
