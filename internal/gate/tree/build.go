package tree

import (
	"context"
	"slices"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/rank"
)

// links are the visible links that make up row k now: center's children
// (gen ≥ 0) or parents (gen < 0); for the focal row of a post without visible
// parents, nothing (the focal post alone). Only Visibility's link walks are
// used, so an invisible post or link never contributes (§10.1.3).
func (s *Service) links(ctx context.Context, sr core.Search, sn *snapshot, k rowKey, center core.PostMeta) []core.VisibleLink {
	switch {
	case sn.alone(k):
		return nil
	case k.gen >= 0:
		return s.d.Vis.VisibleChildren(ctx, sr, center, core.ChildFilter{})
	default:
		return s.d.Vis.VisibleParents(ctx, sr, center)
	}
}

// filters are the reader's personal filters, read fresh on every request like
// the feed's (D32): posts the reader hid and posts by authors the reader muted
// are left out of every row, its More count and its paging. The focal post is
// the one exception: the reader opened it explicitly.
type filters struct {
	focal  core.PostID
	hidden map[core.PostID]struct{}
	muted  map[core.UserID]struct{}
}

func (s *Service) filters(ctx context.Context, sn *snapshot) filters {
	f := filters{focal: sn.focal}
	if s.d.Engage != nil {
		f.hidden = s.d.Engage.HiddenSet(ctx, sn.viewer)
	}
	if s.d.Mutes != nil {
		f.muted = s.d.Mutes.MutedAuthors(ctx, sn.viewer)
	}
	return f
}

// drop reports whether the filters leave m out.
func (f filters) drop(m core.PostMeta) bool {
	if m.ID == f.focal {
		return false
	}
	if _, ok := f.hidden[m.ID]; ok {
		return true
	}
	_, ok := f.muted[m.Author]
	return ok
}

// visible is the set of posts row k may serve now, each re-checked against the
// reader's current search (fidelity #4) and filters (D32). The center itself
// must be visible.
func (s *Service) visible(ctx context.Context, sr core.Search, sn *snapshot, k rowKey) (map[core.PostID]core.Checked, bool) {
	cm, ok := s.d.Posts.Meta(ctx, k.center)
	if !ok {
		return nil, false
	}
	cc, ok := s.d.Vis.CheckPost(ctx, sr, cm)
	if !ok {
		return nil, false
	}
	out := map[core.PostID]core.Checked{}
	if sn.alone(k) {
		out[k.center] = cc
		return out, true
	}
	f := s.filters(ctx, sn)
	for _, l := range s.links(ctx, sr, sn, k, cm) {
		if !f.drop(l.Other.Meta()) {
			out[l.Other.Meta().ID] = l.Other
		}
	}
	return out, true
}

// row returns the ranked row k, ranking it the first time the snapshot needs it.
func (s *Service) row(ctx context.Context, sr core.Search, sn *snapshot, k rowKey) *rowSnap {
	if rs, ok := sn.rows[k]; ok {
		return rs
	}
	rs := s.rank(ctx, sr, sn, k)
	sn.keep(k, rs)
	return rs
}

// ranked is a candidate of a row with its sort keys.
type ranked struct {
	id   core.PostID
	tier int
	sc   rank.Scored
}

// rank orders every visible candidate of row k by (distance tier asc, then
// rank.Less under the snapshot's preset): the feed's comparator, so equal t_eff
// falls to a friend+ first hop, then the older first-hop edge, then the lower
// first-hop ID, then the post ID (§10.1.4 rule 1). The focal row puts the focal
// post first.
func (s *Service) rank(ctx context.Context, sr core.Search, sn *snapshot, k rowKey) *rowSnap {
	vis, ok := s.visible(ctx, sr, sn, k)
	if !ok {
		return &rowSnap{}
	}
	rs := &rowSnap{entries: make([]core.PostID, 0, len(vis))}
	if k.gen == 0 {
		if _, ok := vis[sn.focal]; ok {
			rs.entries = append(rs.entries, sn.focal)
		}
	}
	for _, id := range s.order(ctx, sr, sn, vis) {
		if k.gen == 0 && id == sn.focal {
			continue
		}
		rs.entries = append(rs.entries, id)
	}
	return rs
}

// order sorts posts like a row: distance tier first, then rank.Less under the
// snapshot's preset.
func (s *Service) order(ctx context.Context, sr core.Search, sn *snapshot, vis map[core.PostID]core.Checked) []core.PostID {
	params := rank.ParamsFor(sn.preset)
	now := s.now()
	cands := make([]ranked, 0, len(vis))
	for id, c := range vis {
		tier, _ := sr.Dist(c.Meta().Author)
		cands = append(cands, ranked{id: id, tier: tier, sc: rank.Score(params, now, s.candidate(ctx, sr, sn, c))})
	}
	slices.SortFunc(cands, func(a, b ranked) int {
		switch {
		case a.tier != b.tier:
			return a.tier - b.tier
		case rank.Less(a.sc, b.sc):
			return -1
		case rank.Less(b.sc, a.sc):
			return 1
		}
		return 0
	})
	out := make([]core.PostID, len(cands))
	for i, c := range cands {
		out[i] = c.id
	}
	return out
}

// candidate is the rank input of c, exactly as the feed builds it.
func (s *Service) candidate(ctx context.Context, sr core.Search, sn *snapshot, c core.Checked) rank.Candidate {
	m := c.Meta()
	fh := sr.FirstHop(m.Author)
	if fh == 0 {
		fh = m.Author // a friend (or the reader): the first hop is the author
	}
	return rank.Candidate{
		Key: [16]byte(m.ID), Author: uint32(m.Author), CreatedAt: m.CreatedAt, IsReply: s.visibleReply(ctx, sr, m),
		N: s.d.Prefer.N(ctx, sr, c, sn.memo), KidsN: s.d.Prefer.KidsN(ctx, sr, c, sn.memo, kidsGens),
		FirstHopPlus: sr.FirstHopPlus(m.Author), FirstHopSince: sn.hops[fh], FirstHopID: uint32(fh),
		ShowLess: sn.showLess[m.Author],
	}
}

// serve builds one page of row k from the raw offset: the next RowPage
// entries that pass the re-check, a tier divider where the tier changes, "shown
// above" for posts already served in a row above (no body), the extra visible
// parents of each child as chips, More = the remaining visible entries and a
// cursor when there are any. It returns the IDs it served.
func (s *Service) serve(ctx context.Context, sr core.Search, sn *snapshot, k rowKey, rs *rowSnap, offset int) (gate.TreeRow, []core.PostID, error) {
	row := gate.TreeRow{Gen: k.gen, Label: Label(k.gen), Center: s.d.Refs.Post(sn.viewer, k.center), Items: []gate.TreeItem{}}
	vis, ok := s.visible(ctx, sr, sn, k)
	if !ok {
		return gate.TreeRow{}, nil, gate.ErrNotFound
	}
	prevTier, hasPrev := 0, false
	for i := 0; i < offset && i < len(rs.entries); i++ {
		if c, ok := vis[rs.entries[i]]; ok {
			prevTier, hasPrev = s.tier(sr, c), true
		}
	}
	var page []core.Checked
	next := offset
	for i := offset; i < len(rs.entries); i++ {
		c, ok := vis[rs.entries[i]]
		if !ok {
			continue
		}
		if len(page) == RowPage {
			row.More++
			continue
		}
		page = append(page, c)
		next = i + 1
	}
	if row.More > 0 {
		row.Cursor = s.cursorString(sn, cursor{id: sn.id, key: k, offset: next})
	}

	above := make([]bool, len(page))
	var full, stub []core.Checked
	for i, c := range page {
		if above[i] = sn.shownAbove(k.gen, c.Meta().ID); above[i] {
			stub = append(stub, c)
		} else {
			full = append(full, c)
		}
	}
	fullViews, err := s.views(ctx, sr, full, true)
	if err != nil {
		return gate.TreeRow{}, nil, err
	}
	stubViews, err := s.views(ctx, sr, stub, false)
	if err != nil {
		return gate.TreeRow{}, nil, err
	}

	served := make([]core.PostID, 0, len(page))
	for i, c := range page {
		var it gate.TreeItem
		if above[i] {
			it = gate.TreeItem{Post: stubViews[0], ShownAbove: true}
			stubViews = stubViews[1:]
		} else {
			it = gate.TreeItem{Post: fullViews[0]}
			fullViews = fullViews[1:]
			if k.gen >= 0 && !sn.alone(k) {
				it.AlsoRepliesTo, it.AlsoParents = s.chips(ctx, sr, sn.viewer, c, k.center)
			}
		}
		tier := s.tier(sr, c)
		it.TierDivider = hasPrev && tier != prevTier
		prevTier, hasPrev = tier, true
		row.Items = append(row.Items, it)
		served = append(served, c.Meta().ID)
	}
	return row, served, nil
}

// tier is the reader's distance to the post's author (0 for the reader).
func (s *Service) tier(sr core.Search, c core.Checked) int {
	d, _ := sr.Dist(c.Meta().Author)
	return d
}

// views builds the post views of ps (with or without bodies; never context
// rows: the rows themselves show what a post replies to).
func (s *Service) views(ctx context.Context, sr core.Search, ps []core.Checked, body bool) ([]gate.PostView, error) {
	if len(ps) == 0 {
		return nil, nil
	}
	return s.d.Views.Posts(ctx, sr, ps, core.ViewOpts{WithBody: body})
}

// chips are c's visible parents other than the row's center ("also replies to
// @b"): the parents' authors and the parent posts, in stored order.
func (s *Service) chips(ctx context.Context, sr core.Search, v core.UserID, c core.Checked, center core.PostID) ([]gate.ChainHop, []gate.Ref) {
	var hops []gate.ChainHop
	var refs []gate.Ref
	var authors []core.UserID
	var posts []core.PostID
	for _, l := range s.d.Vis.VisibleParents(ctx, sr, c.Meta()) {
		pm := l.Other.Meta()
		if pm.ID == center {
			continue
		}
		authors = append(authors, pm.Author)
		posts = append(posts, pm.ID)
	}
	if len(posts) == 0 {
		return nil, nil
	}
	metas, _ := s.d.Users.GetMany(ctx, authors)
	for i, a := range authors {
		hops = append(hops, gate.ChainHop{Ref: s.d.Refs.User(v, a), Handle: metas[a].Handle})
		refs = append(refs, s.d.Refs.Post(v, posts[i]))
	}
	return hops, refs
}

// build builds the rows of a new snapshot around the focal post fc, in display
// order (−2, −1, 0, +1, +2) so a post served twice is "shown above" at its
// lower spot, and records them as the current view.
func (s *Service) build(ctx context.Context, sr core.Search, sn *snapshot, fc core.Checked) (gate.TreeView, error) {
	focal := fc.Meta().ID
	first := func(k rowKey) (core.PostID, bool) {
		rs := s.row(ctx, sr, sn, k)
		if len(rs.entries) == 0 {
			return core.PostID{}, false
		}
		return rs.entries[0], true
	}
	keys := make([]rowKey, 0, 5)
	primary, hasParent := first(rowKey{gen: -1, center: focal})
	if hasParent {
		if _, ok := first(rowKey{gen: -2, center: primary}); ok {
			keys = append(keys, rowKey{gen: -2, center: primary})
		}
		keys = append(keys, rowKey{gen: -1, center: focal}, rowKey{gen: 0, center: primary})
	} else {
		keys = append(keys, rowKey{gen: 0, center: focal})
	}
	child, hasChild := first(rowKey{gen: 1, center: focal})
	if hasChild {
		keys = append(keys, rowKey{gen: 1, center: focal})
		if _, ok := first(rowKey{gen: 2, center: child}); ok {
			keys = append(keys, rowKey{gen: 2, center: child})
		}
	}

	view := gate.TreeView{Snapshot: s.snapshotString(sn), Focal: s.d.Refs.Post(sn.viewer, focal), Rows: []gate.TreeRow{}}
	for _, k := range keys {
		row, served, err := s.serve(ctx, sr, sn, k, s.row(ctx, sr, sn, k), 0)
		if err != nil {
			return gate.TreeView{}, err
		}
		if len(row.Items) == 0 {
			continue
		}
		sn.view[k.gen] = &viewRow{center: k.center, served: served}
		view.Rows = append(view.Rows, row)
	}
	kin, err := s.kin(ctx, sr, sn)
	if err != nil {
		return gate.TreeView{}, err
	}
	view.Kin = kin
	if vr, ok := sn.view[-2]; ok && len(vr.served) > 0 {
		view.HasEarlier = s.hasParents(ctx, sr, sn, vr.served[0])
	}
	if vr, ok := sn.view[2]; ok && len(vr.served) > 0 {
		view.HasLater = s.hasChildren(ctx, sr, sn, vr.served[0])
	}
	return view, nil
}

// hasParents reports whether id has a parent a row would serve.
func (s *Service) hasParents(ctx context.Context, sr core.Search, sn *snapshot, id core.PostID) bool {
	m, ok := s.d.Posts.Meta(ctx, id)
	return ok && s.anyKept(s.filters(ctx, sn), s.d.Vis.VisibleParents(ctx, sr, m))
}

// hasChildren reports whether id has a child a row would serve.
func (s *Service) hasChildren(ctx context.Context, sr core.Search, sn *snapshot, id core.PostID) bool {
	m, ok := s.d.Posts.Meta(ctx, id)
	return ok && s.anyKept(s.filters(ctx, sn), s.d.Vis.VisibleChildren(ctx, sr, m, core.ChildFilter{}))
}

func (s *Service) anyKept(f filters, ls []core.VisibleLink) bool {
	for _, l := range ls {
		if !f.drop(l.Other.Meta()) {
			return true
		}
	}
	return false
}

// visibleReply is is_reply as the reader sees it: a ref parent the reader can
// see (link included, §10.1.3). A post whose parents are all hidden looks
// exactly like a root, to the reply penalty too (INT-0 conformance finding).
func (s *Service) visibleReply(ctx context.Context, sr core.Search, m core.PostMeta) bool {
	if !m.IsReply {
		return false
	}
	for _, l := range s.d.Vis.VisibleParents(ctx, sr, m) {
		if l.Link.Kind == core.LinkRef {
			return true
		}
	}
	return false
}
