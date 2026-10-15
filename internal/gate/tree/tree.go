// Package tree builds the family-tree view (ARCHITECTURE §10.1.4, §10.1.3):
// rows −2…+2 around a focal post, each ranked like a feed tier.
//
// Owner: P0-W08 → P1-W08.
//
// Rows. −1 lists the focal post's visible parents; 0 lists the children of the
// primary parent (the best-ranked visible parent) with the focal post first,
// or the focal post alone when it has no visible parent; +1 lists the focal
// post's visible children; +2 the children of row +1's first post; −2 the
// parents of row −1's first post. Row(gen, center) lists center's children
// (gen ≥ 0) or parents (gen < 0): it re-centers a row after a swipe and loads
// rows beyond ±2. Every row ranks all its visible candidates by distance tier,
// then rank.Less under the reader's preset (the feed's comparator and
// tie-breaks), and serves them RowPage at a time with bodies; More pages on.
//
// Horizon (§10.1.3). Rows are built only from Visibility.VisibleChildren /
// VisibleParents, so a link the reader may not see contributes nothing: no
// placeholder, no count, no position. Traversal never passes through an
// invisible post: a row's center must be visible and must have been served in
// the adjacent row of the same snapshot. A post whose parents are all hidden
// looks exactly like a root.
//
// Snapshots (D5). A row is ranked once per snapshot and kept in memory, keyed
// by (viewer, 128-bit random id), 30 minutes after its last use; every Row and
// More re-runs the reader's search and re-checks what it serves, so posts that
// became invisible disappear (ordering never changes) and More counts only what
// is still visible. Snapshot ids and cursors are MAC'd with the viewer: a
// forged one or another viewer's is gate.ErrNotFound; the reader's own expired
// snapshot is Problem tree_snapshot_expired (409) and the client reopens.
//
// Each post appears once: a post already served in a row above gets ShownAbove
// (a "↑ shown above" stub without its body). Extra visible parents of a child
// become AlsoRepliesTo / AlsoParents chips that refocus the view.
package tree

import (
	"context"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// Row and snapshot limits (§10.1.4, D5).
const (
	// RowPage is the number of posts per row page; bodies are loaded for them.
	RowPage = 10
	// SnapshotTTL is how long an unused snapshot is kept.
	SnapshotTTL = 30 * time.Minute
	// SnapshotsPerUser bounds the live snapshots of one viewer; the oldest goes first.
	SnapshotsPerUser = 10
	// MaxSnapshots bounds the snapshots kept in memory overall.
	MaxSnapshots = 50_000
	// MaxGen bounds how far a row may sit from the focal row.
	MaxGen = 1000
	// maxRows bounds the ranked rows one snapshot keeps (rows outside the
	// current view go first).
	maxRows = 64
)

// Row labels (gate.TreeRow.Label).
const (
	LabelEarlier          = "earlier"
	LabelReplyingTo       = "replying_to"
	LabelThisPost         = "this_post"
	LabelReplies          = "replies"
	LabelRepliesToReplies = "replies_to_replies"
	LabelGenerationsDown  = "generations_down"
	LabelGenerationsUp    = "generations_up"
)

// Problem codes.
const (
	CodeSnapshotExpired   = "tree_snapshot_expired"
	CodePresetUnavailable = "preset_unavailable"
)

// MetricOpened counts opened trees (gate/metrics closed list).
const MetricOpened = "tree.opened"

// Service is the tree module.
type Service struct {
	d     *core.Deps
	store *snapStore
}

// New returns the service.
func New(d *core.Deps) *Service {
	return &Service{d: d, store: newSnapStore(MaxSnapshots, SnapshotsPerUser, SnapshotTTL)}
}

// Init drops every snapshot of a deleted account.
func (s *Service) Init(context.Context) error {
	if s.d.Bus != nil {
		core.Subscribe(s.d.Bus, func(e core.AccountDeleted) { s.store.dropViewer(e.ID) })
	}
	return nil
}

// Reset forgets every snapshot (test hooks: the database was wiped).
func (s *Service) Reset() { s.store.reset() }

// Label is the section header of row gen.
func Label(gen int) string {
	switch {
	case gen <= -3:
		return LabelGenerationsUp
	case gen == -2:
		return LabelEarlier
	case gen == -1:
		return LabelReplyingTo
	case gen == 0:
		return LabelThisPost
	case gen == 1:
		return LabelReplies
	case gen == 2:
		return LabelRepliesToReplies
	}
	return LabelGenerationsDown
}

// Open opens the tree around focal with preset ("" = the reader's saved
// preset): the focal row plus up to two rows above and two below (empty rows
// are left out).
func (s *Service) Open(ctx context.Context, focal gate.Ref, preset string) (gate.TreeView, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return gate.TreeView{}, err
	}
	sr, err := s.d.Vis.SearchFor(ctx, v.ID)
	if err != nil {
		return gate.TreeView{}, err
	}
	fc, err := s.resolve(ctx, sr, v.ID, focal)
	if err != nil {
		return gate.TreeView{}, err
	}
	p, err := s.preset(ctx, v.ID, preset)
	if err != nil {
		return gate.TreeView{}, err
	}
	snap, err := newSnapshot(v.ID, p, fc.Meta().ID)
	if err != nil {
		return gate.TreeView{}, err
	}
	snap.showLess = s.d.Engage.ShowLessCounts(ctx, v.ID)
	snap.hops = s.hops(v.ID)
	s.store.put(snap, s.now())

	snap.mu.Lock()
	defer snap.mu.Unlock()
	view, err := s.build(ctx, sr, snap, fc)
	if err != nil {
		return gate.TreeView{}, err
	}
	if s.d.Metrics != nil {
		s.d.Metrics.Inc(MetricOpened, "", 1)
	}
	return view, nil
}

// Row serves row gen of snapshot listing center's children (gen ≥ 0) or
// parents (gen < 0), ranked once per snapshot. center must be visible and must
// have been served in the adjacent row closer to the focal row (for gen 0: a
// parent of the focal post).
func (s *Service) Row(ctx context.Context, snapshot string, gen int, center gate.Ref) (gate.TreeRow, error) {
	_, sr, snap, cc, err := s.enter(ctx, snapshot, gen, center)
	if err != nil {
		return gate.TreeRow{}, err
	}
	defer snap.mu.Unlock()
	k := rowKey{gen: gen, center: cc.Meta().ID}
	if !snap.adjacentServed(k) {
		return gate.TreeRow{}, gate.ErrNotFound
	}
	rs := s.row(ctx, sr, snap, k)
	row, served, err := s.serve(ctx, sr, snap, k, rs, 0)
	if err != nil {
		return gate.TreeRow{}, err
	}
	snap.setRow(k, served)
	return row, nil
}

// More serves the next page of a row from its cursor.
func (s *Service) More(ctx context.Context, snapshot string, gen int, center gate.Ref, cursor string) (gate.TreeRow, error) {
	v, sr, snap, cc, err := s.enter(ctx, snapshot, gen, center)
	if err != nil {
		return gate.TreeRow{}, err
	}
	defer snap.mu.Unlock()
	k := rowKey{gen: gen, center: cc.Meta().ID}
	c, err := s.openCursor(v, cursor)
	if err != nil || c.id != snap.id || c.key != k {
		return gate.TreeRow{}, gate.ErrNotFound
	}
	rs, ok := snap.rows[k]
	if !ok {
		return gate.TreeRow{}, gate.ErrNotFound
	}
	row, served, err := s.serve(ctx, sr, snap, k, rs, c.offset)
	if err != nil {
		return gate.TreeRow{}, err
	}
	snap.addServed(k, served)
	return row, nil
}

// enter verifies the caller's snapshot id and center (uniform not-found for
// anything that does not resolve), then finds the snapshot (Problem
// tree_snapshot_expired for the caller's own snapshot that is gone) and locks it.
func (s *Service) enter(ctx context.Context, snapshot string, gen int, center gate.Ref) (core.UserID, core.Search, *snapshot, core.Checked, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return 0, nil, nil, core.Checked{}, err
	}
	id, err := s.openSnapshot(v.ID, snapshot)
	if err != nil || gen < -MaxGen || gen > MaxGen {
		return 0, nil, nil, core.Checked{}, gate.ErrNotFound
	}
	sr, err := s.d.Vis.SearchFor(ctx, v.ID)
	if err != nil {
		return 0, nil, nil, core.Checked{}, err
	}
	cc, err := s.resolve(ctx, sr, v.ID, center)
	if err != nil {
		return 0, nil, nil, core.Checked{}, err
	}
	snap, ok := s.store.get(v.ID, id, s.now())
	if !ok {
		return 0, nil, nil, core.Checked{}, gate.NewProblem(gate.StatusConflict, "tree_snapshot_expired")
	}
	snap.mu.Lock()
	return v.ID, sr, snap, cc, nil
}

// resolve opens a post ref of v's and checks the post is visible to v.
func (s *Service) resolve(ctx context.Context, sr core.Search, v core.UserID, ref gate.Ref) (core.Checked, error) {
	id, ok := s.d.Refs.OpenPost(v, ref)
	if !ok {
		return core.Checked{}, gate.ErrNotFound
	}
	m, ok := s.d.Posts.Meta(ctx, id)
	if !ok {
		return core.Checked{}, gate.ErrNotFound
	}
	c, ok := s.d.Vis.CheckPost(ctx, sr, m)
	if !ok {
		return core.Checked{}, gate.ErrNotFound
	}
	return c, nil
}

// preset resolves the requested preset: "" is the reader's saved preset (Top
// reads as Balanced in Phase 0); top and unknown names are Problem
// preset_unavailable.
func (s *Service) preset(ctx context.Context, v core.UserID, name string) (core.Preset, error) {
	if name == "" {
		me, err := s.d.Users.Get(ctx, v)
		if err != nil {
			return 0, err
		}
		if me.Preset == core.PresetTop {
			return core.PresetBalanced, nil
		}
		return me.Preset, nil
	}
	p, ok := core.ParsePreset(name)
	if !ok || p == core.PresetTop {
		return 0, gate.NewProblem(gate.StatusUnprocessable, "preset_unavailable", "preset")
	}
	return p, nil
}

// hops maps each of v's friends to the age of v's edge to them (rank ties).
func (s *Service) hops(v core.UserID) map[core.UserID]time.Time {
	out := map[core.UserID]time.Time{}
	for _, e := range s.d.Graph.Friends(v) {
		out[e.To] = e.Since
	}
	return out
}

func (s *Service) now() time.Time { return s.d.Clock.Now() }
