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

// visible is the set of posts row k may serve now, each re-checked against the
// reader's current search (fidelity #4). The center itself must be visible.
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
	for _, l := range s.links(ctx, sr, sn, k, cm) {
		out[l.Other.Meta().ID] = l.Other
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
	rs := &rowSnap{entries: make([]core.PostID, 0, len(cands))}
	if k.gen == 0 {
		if _, ok := vis[sn.focal]; ok {
			rs.entries = append(rs.entries, sn.focal)
		}
	}
	for _, c := range cands {
		if k.gen == 0 && c.id == sn.focal {
			continue
		}
		rs.entries = append(rs.entries, c.id)
	}
	return rs
}

// candidate is the rank input of c, exactly as the feed builds it.
func (s *Service) candidate(ctx context.Context, sr core.Search, sn *snapshot, c core.Checked) rank.Candidate {
	m := c.Meta()
	fh := sr.FirstHop(m.Author)
	if fh == 0 {
		fh = m.Author // a friend (or the reader): the first hop is the author
	}
	return rank.Candidate{
		Key: [16]byte(m.ID), Author: uint32(m.Author), CreatedAt: m.CreatedAt, IsReply: m.IsReply,
		N: s.d.Prefer.N(ctx, sr, c, sn.memo), KidsN: s.d.Prefer.KidsN(ctx, sr, c, sn.memo, 1),
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
	if vr, ok := sn.view[-2]; ok && len(vr.served) > 0 {
		view.HasEarlier = s.hasParents(ctx, sr, vr.served[0])
	}
	if vr, ok := sn.view[2]; ok && len(vr.served) > 0 {
		view.HasLater = s.hasChildren(ctx, sr, vr.served[0])
	}
	return view, nil
}

func (s *Service) hasParents(ctx context.Context, sr core.Search, id core.PostID) bool {
	m, ok := s.d.Posts.Meta(ctx, id)
	return ok && len(s.d.Vis.VisibleParents(ctx, sr, m)) > 0
}

func (s *Service) hasChildren(ctx context.Context, sr core.Search, id core.PostID) bool {
	m, ok := s.d.Posts.Meta(ctx, id)
	return ok && len(s.d.Vis.VisibleChildren(ctx, sr, m, core.ChildFilter{Limit: 1})) > 0
}
