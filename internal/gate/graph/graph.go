// Package graph is the in-memory friendship graph (ARCHITECTURE §4.1, §4.5, §4.6,
// §12). It implements core.Graph.
//
// Layout (plan §4.7): users are mapped to dense indices (append-only), and each
// dense index owns one adjacency row (neighbour indices sorted ascending, with
// parallel level / since arrays). Mutations replace the touched rows
// (copy-on-write: a published row is never written in place) under a
// sync.RWMutex; every BFS holds the read lock for its duration and keeps nothing a
// later mutation could change.
//
// The global epoch (atomic) is bumped, under the write lock, on every edge
// removal, block add, hide-via add, level downgrade and Load, so per-reader
// caches keyed by (viewer, epoch) never serve a horizon wider than the current
// one. Additions do not bump it: a missed new path only delays content (§4.1).
//
// Search is a layered BFS from the reader that skips the reader's OWN blocked
// users entirely and treats the reader's hide-via users as transit-forbidden
// (reachable, never routed through) — D7. The first hop of every node at
// distance ≥ 2 follows the D22 rule (fidelity #10); see search.go. With
// PCC_LAZY_BFS=1 the search is lazy and cached per reader (lazy.go, P1).
//
// Owner: P0-W02 → P1-W03.
package graph

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

// Adjacency is a plain snapshot of the graph: every user's edges (both
// directions are present). It is the input of the reference oracles.
type Adjacency map[core.UserID][]core.Edge

// row is one user's adjacency: neighbours (dense indices, ascending) with their
// edge levels and ages. Rows are immutable once published.
type row struct {
	to    []int32
	level []core.EdgeLevel
	since []time.Time
}

func (r row) find(j int32) (int, bool) { return slices.BinarySearch(r.to, j) }

// with returns a copy of r with j set to lvl; an existing entry keeps its
// original since (an upgrade keeps the edge age). It also returns the previous
// level and whether the entry existed.
func (r row) with(j int32, lvl core.EdgeLevel, since time.Time) (row, core.EdgeLevel, bool) {
	i, ok := r.find(j)
	if ok {
		old := r.level[i]
		nr := row{to: r.to, level: slices.Clone(r.level), since: r.since}
		nr.level[i] = lvl
		return nr, old, true
	}
	return row{
		to:    slices.Insert(slices.Clone(r.to), i, j),
		level: slices.Insert(slices.Clone(r.level), i, lvl),
		since: slices.Insert(slices.Clone(r.since), i, since),
	}, 0, false
}

// without returns a copy of r without j.
func (r row) without(j int32) (row, bool) {
	i, ok := r.find(j)
	if !ok {
		return r, false
	}
	return row{
		to:    slices.Delete(slices.Clone(r.to), i, i+1),
		level: slices.Delete(slices.Clone(r.level), i, i+1),
		since: slices.Delete(slices.Clone(r.since), i, i+1),
	}, true
}

// Graph implements core.Graph.
type Graph struct {
	d *core.Deps

	mu      sync.RWMutex // rows, blocks, hideVia (and interning, which also takes imu)
	rows    []row
	blocks  map[core.UserID]*roaring.Bitmap // blocker → blocked (bitmaps are copy-on-write)
	hideVia map[core.UserID]*roaring.Bitmap // user → via (copy-on-write)

	imu sync.RWMutex // idx, ids: append-only dense numbering
	idx map[core.UserID]int32
	ids []core.UserID

	// order lists every dense index sorted by user ID (guarded by mu), so a
	// search emits ID-sorted layers in one linear pass.
	order []int32

	epoch atomic.Uint64

	// The lazy search (lazy.go): per-reader cache and deep-tier release.
	cmu          sync.Mutex
	cache        map[core.UserID]*cacheEntry
	lazyOverride atomic.Int32 // 1 on, -1 off, 0 = PCC_LAZY_BFS
	deepBucket   time.Duration
	sleep        func(time.Duration)
}

var _ core.Graph = (*Graph)(nil)

// New returns an empty graph; Init loads it from the database when d.DB is set.
func New(d *core.Deps) *Graph {
	return &Graph{
		d: d, idx: map[core.UserID]int32{},
		blocks: map[core.UserID]*roaring.Bitmap{}, hideVia: map[core.UserID]*roaring.Bitmap{},
		cache: map[core.UserID]*cacheEntry{}, deepBucket: DeepBucket, sleep: time.Sleep,
	}
}

// Init subscribes to AccountDeleted and loads the graph.
func (g *Graph) Init(ctx context.Context) error {
	if g.d != nil && g.d.Bus != nil {
		core.Subscribe(g.d.Bus, func(e core.AccountDeleted) { g.forget(e.ID) })
	}
	return g.Load(ctx)
}

// forget drops every edge, block and hide-via involving u (account deletion;
// the table owners delete the rows themselves). It bumps the epoch.
func (g *Graph) forget(u core.UserID) {
	i, ok := g.lookup(u)
	g.mu.Lock()
	defer g.mu.Unlock()
	if ok {
		for _, j := range g.rows[i].to {
			if r, had := g.rows[j].without(i); had {
				g.rows[j] = r
			}
		}
		g.rows[i] = row{}
	}
	delete(g.blocks, u)
	delete(g.hideVia, u)
	for _, m := range []map[core.UserID]*roaring.Bitmap{g.blocks, g.hideVia} {
		for a, bm := range m {
			if bm.Contains(uint32(u)) {
				setRel(m, a, u, false)
			}
		}
	}
	g.bump()
}

// Epoch returns the global removal epoch.
func (g *Graph) Epoch() uint64 { return g.epoch.Load() }

// lookup returns u's dense index.
func (g *Graph) lookup(u core.UserID) (int32, bool) {
	g.imu.RLock()
	i, ok := g.idx[u]
	g.imu.RUnlock()
	return i, ok
}

// intern returns u's dense index, assigning one (with an empty row) if needed
// and keeping order sorted. Callers hold g.mu for writing.
func (g *Graph) intern(u core.UserID) int32 {
	i, added := g.internUnordered(u)
	if added {
		pos, _ := slices.BinarySearchFunc(g.order, u, func(j int32, u core.UserID) int { return cmpID(g.ids[j], u) })
		g.order = slices.Insert(g.order, pos, i)
	}
	return i
}

// internUnordered is intern without maintaining order (bulk loads re-sort once).
func (g *Graph) internUnordered(u core.UserID) (int32, bool) {
	g.imu.Lock()
	defer g.imu.Unlock()
	if i, ok := g.idx[u]; ok {
		return i, false
	}
	i := int32(len(g.ids))
	g.idx[u] = i
	g.ids = append(g.ids, u)
	g.rows = append(g.rows, row{})
	return i, true
}

// Friends returns u's edges sorted by (level desc, since asc, id asc).
func (g *Graph) Friends(u core.UserID) []core.Edge {
	i, ok := g.lookup(u)
	if !ok {
		return []core.Edge{}
	}
	g.mu.RLock()
	r := g.rows[i]
	g.mu.RUnlock()
	g.imu.RLock()
	out := make([]core.Edge, len(r.to))
	for k, j := range r.to {
		out[k] = core.Edge{To: g.ids[j], Level: r.level[k], Since: r.since[k]}
	}
	g.imu.RUnlock()
	slices.SortFunc(out, compareEdges)
	return out
}

func compareEdges(a, b core.Edge) int {
	if a.Level != b.Level {
		return int(b.Level) - int(a.Level)
	}
	if c := a.Since.Compare(b.Since); c != 0 {
		return c
	}
	return cmpID(a.To, b.To)
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

// Level returns the level of a–b.
func (g *Graph) Level(a, b core.UserID) (core.EdgeLevel, bool) {
	ia, ok := g.lookup(a)
	if !ok {
		return 0, false
	}
	ib, ok := g.lookup(b)
	if !ok {
		return 0, false
	}
	g.mu.RLock()
	r := g.rows[ia]
	g.mu.RUnlock()
	k, ok := r.find(ib)
	if !ok {
		return 0, false
	}
	return r.level[k], true
}

// Degree returns u's edge count.
func (g *Graph) Degree(u core.UserID) int {
	i, ok := g.lookup(u)
	if !ok {
		return 0
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.rows[i].to)
}

// AddEdge adds or updates a–b in both directions (called after commit). An
// existing edge keeps its since; a level downgrade bumps the epoch because it
// narrows friend+ audiences.
func (g *Graph) AddEdge(a, b core.UserID, lvl core.EdgeLevel, since time.Time) {
	if a == b || a == 0 || b == 0 {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	ia, ib := g.intern(a), g.intern(b)
	ra, old, existed := g.rows[ia].with(ib, lvl, since)
	rb, _, _ := g.rows[ib].with(ia, lvl, since)
	g.rows[ia], g.rows[ib] = ra, rb
	if existed && lvl < old {
		g.bump()
	} else {
		g.drop(a, b) // the readers' own edges changed: their next search sees it
	}
}

// RemoveEdge removes a–b and bumps the epoch (always: removals only narrow).
func (g *Graph) RemoveEdge(a, b core.UserID) {
	ia, okA := g.lookup(a)
	ib, okB := g.lookup(b)
	g.mu.Lock()
	defer g.mu.Unlock()
	if okA && okB {
		if r, ok := g.rows[ia].without(ib); ok {
			g.rows[ia] = r
		}
		if r, ok := g.rows[ib].without(ia); ok {
			g.rows[ib] = r
		}
	}
	g.bump()
}

// setRel sets or clears a→b in m, replacing (never mutating) the bitmap so
// that bitmaps handed out earlier stay valid.
func setRel(m map[core.UserID]*roaring.Bitmap, a, b core.UserID, on bool) {
	bm := m[a]
	if on {
		if bm == nil {
			bm = roaring.New()
		} else {
			bm = bm.Clone()
		}
		bm.Add(uint32(b))
		m[a] = bm
		return
	}
	if bm == nil || !bm.Contains(uint32(b)) {
		return
	}
	bm = bm.Clone()
	bm.Remove(uint32(b))
	if bm.IsEmpty() {
		delete(m, a)
	} else {
		m[a] = bm
	}
}

// SetBlock records that blocker blocked blocked (one direction, D7). Every add
// bumps the epoch.
func (g *Graph) SetBlock(blocker, blocked core.UserID, on bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	setRel(g.blocks, blocker, blocked, on)
	if on {
		g.bump()
	} else {
		g.drop(blocker)
	}
}

// SetHideVia records a hide-via (P1). Every add bumps the epoch.
func (g *Graph) SetHideVia(user, via core.UserID, on bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	setRel(g.hideVia, user, via, on)
	if on {
		g.bump()
	} else {
		g.drop(user)
	}
}

// Exclusions returns v's own blocks and hide-vias as fresh bitmaps.
func (g *Graph) Exclusions(v core.UserID) core.Exclusions {
	g.mu.RLock()
	defer g.mu.RUnlock()
	ex := core.Exclusions{Blocked: roaring.New(), HiddenVia: roaring.New()}
	if b := g.blocks[v]; b != nil {
		ex.Blocked.Or(b)
	}
	if h := g.hideVia[v]; h != nil {
		ex.HiddenVia.Or(h)
	}
	return ex
}

// Blocks reports whether a blocked b (one direction).
func (g *Graph) Blocks(a, b core.UserID) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	bm := g.blocks[a]
	return bm != nil && bm.Contains(uint32(b))
}

// Load (re)loads the graph from edges / blocks / hide_via and bumps the epoch.
// Without a database it does nothing.
func (g *Graph) Load(ctx context.Context) error {
	if g.d == nil || g.d.DB == nil {
		return nil
	}
	return g.LoadFrom(ctx, g.d.DB.Q())
}

type loadedEdge struct {
	a, b  core.UserID
	level core.EdgeLevel
	since time.Time
}

// LoadFrom replaces the whole graph with the rows read through q.
func (g *Graph) LoadFrom(ctx context.Context, q store.Q) error {
	var edges []loadedEdge
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
		edges = append(edges, loadedEdge{core.UserID(a), core.UserID(b), core.EdgeLevel(lvl), since})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	blocks := map[core.UserID]*roaring.Bitmap{}
	hide := map[core.UserID]*roaring.Bitmap{}
	for _, t := range []struct {
		sql string
		m   map[core.UserID]*roaring.Bitmap
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
			bm := t.m[core.UserID(a)]
			if bm == nil {
				bm = roaring.New()
				t.m[core.UserID(a)] = bm
			}
			bm.Add(uint32(b))
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}
	g.replace(edges, blocks, hide)
	return nil
}

// LoadAdjacency replaces the whole graph with adj (no blocks, no hide-vias) and
// bumps the epoch. Used by synthetic data and tests; one-sided entries are
// mirrored, so adj need not be symmetric.
func (g *Graph) LoadAdjacency(adj Adjacency) {
	var edges []loadedEdge
	for u, es := range adj {
		for _, e := range es {
			edges = append(edges, loadedEdge{u, e.To, e.Level, e.Since}, loadedEdge{e.To, u, e.Level, e.Since})
		}
	}
	g.replace(edges, map[core.UserID]*roaring.Bitmap{}, map[core.UserID]*roaring.Bitmap{})
}

// replace swaps in a whole new adjacency (one direction per loadedEdge) and
// bumps the epoch. Dense indices are kept; duplicates keep their first entry.
func (g *Graph) replace(edges []loadedEdge, blocks, hide map[core.UserID]*roaring.Bitmap) {
	valid := func(e loadedEdge) bool { return e.a != e.b && e.a != 0 && e.b != 0 }
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, e := range edges {
		if valid(e) {
			g.internUnordered(e.a)
			g.internUnordered(e.b)
		}
	}
	if len(g.order) != len(g.ids) {
		g.order = make([]int32, len(g.ids))
		for i := range g.order {
			g.order[i] = int32(i)
		}
		slices.SortFunc(g.order, func(a, b int32) int { return cmpID(g.ids[a], g.ids[b]) })
	}
	g.imu.RLock()
	accs := make([][]int, len(g.ids)) // dense → indices into edges
	for k, e := range edges {
		if valid(e) {
			ia := g.idx[e.a]
			accs[ia] = append(accs[ia], k)
		}
	}
	rows := make([]row, len(accs))
	for i, ks := range accs {
		slices.SortStableFunc(ks, func(x, y int) int { return int(g.idx[edges[x].b]) - int(g.idx[edges[y].b]) })
		r := row{to: make([]int32, 0, len(ks)), level: make([]core.EdgeLevel, 0, len(ks)),
			since: make([]time.Time, 0, len(ks))}
		for _, k := range ks {
			j := g.idx[edges[k].b]
			if n := len(r.to); n > 0 && r.to[n-1] == j {
				continue // duplicate: keep the first
			}
			r.to = append(r.to, j)
			r.level = append(r.level, edges[k].level)
			r.since = append(r.since, edges[k].since)
		}
		rows[i] = r
	}
	g.imu.RUnlock()
	g.rows, g.blocks, g.hideVia = rows, blocks, hide
	g.bump()
}

// Adjacency returns a snapshot of the whole graph (reference oracles, tests).
func (g *Graph) Adjacency() Adjacency {
	g.mu.RLock()
	defer g.mu.RUnlock()
	g.imu.RLock()
	defer g.imu.RUnlock()
	adj := make(Adjacency, len(g.ids))
	for i, r := range g.rows {
		if len(r.to) == 0 {
			continue
		}
		es := make([]core.Edge, len(r.to))
		for k, j := range r.to {
			es[k] = core.Edge{To: g.ids[j], Level: r.level[k], Since: r.since[k]}
		}
		adj[g.ids[i]] = es
	}
	return adj
}
