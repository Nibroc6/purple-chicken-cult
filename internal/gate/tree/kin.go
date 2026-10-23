package tree

import (
	"context"
	"slices"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// Relatives (§10.1.4). While the direct line leaves room on the map, the view
// grows outward by kinship: the unshown replies of each post on the map, nearest
// to the focal post first. A grandparent's other replies are aunts and uncles,
// a sibling's replies nieces and nephews, an aunt's replies cousins, and so on.
// A post whose replies already make up a row (the row's center) adds none: that
// row and its More cover them. Relatives never go below the view's last row
// (or row 2 if the rows stop sooner) and pass the same checks and filters as a
// row.
const (
	// MaxNodes bounds the posts on the opened map, rows and relatives together.
	MaxNodes = 40
	// KinPerParent bounds the relatives hanging from one post.
	KinPerParent = 3
)

// kinNode is a post on the map waiting to add its replies: its generation and
// kinship distance from the focal post.
type kinNode struct {
	id   core.PostID
	gen  int
	dist int
}

func (s *Service) kin(ctx context.Context, sr core.Search, sn *snapshot) ([]gate.TreeKin, error) {
	gens := make([]int, 0, len(sn.view))
	for g := range sn.view {
		gens = append(gens, g)
	}
	slices.Sort(gens)
	if len(gens) == 0 {
		return nil, nil
	}
	// Relatives reach as far down as the rows do, and at least as far as a
	// full view (two rows of replies), so a post without replies still shows
	// its nieces and nephews.
	last := max(gens[len(gens)-1], 2)

	shown := map[core.PostID]bool{}
	var queue []kinNode
	for _, g := range gens {
		for _, id := range sn.view[g].served {
			if shown[id] {
				continue
			}
			shown[id] = true
			d := abs(g)
			if g == 0 && id != sn.focal {
				d = 2 // a sibling: up to the parent and down again
			}
			queue = append(queue, kinNode{id: id, gen: g, dist: d})
		}
	}
	slices.SortStableFunc(queue, func(a, b kinNode) int { return a.dist - b.dist })
	room := MaxNodes - len(shown)
	if room <= 0 {
		return nil, nil
	}

	f := s.filters(ctx, sn)
	type found struct {
		c      core.Checked
		parent core.PostID
		gen    int
		dist   int
	}
	var out []found
	for len(queue) > 0 && room > 0 {
		n := queue[0]
		queue = queue[1:]
		if n.gen+1 > last {
			continue
		}
		if vr, ok := sn.view[n.gen+1]; ok && vr.center == n.id {
			continue
		}
		m, ok := s.d.Posts.Meta(ctx, n.id)
		if !ok {
			continue
		}
		vis := map[core.PostID]core.Checked{}
		for _, l := range s.d.Vis.VisibleChildren(ctx, sr, m, core.ChildFilter{}) {
			om := l.Other.Meta()
			if !shown[om.ID] && !f.drop(om) {
				vis[om.ID] = l.Other
			}
		}
		taken := 0
		for _, id := range s.order(ctx, sr, sn, vis) {
			if taken == KinPerParent || room == 0 {
				break
			}
			shown[id] = true
			taken++
			room--
			out = append(out, found{c: vis[id], parent: n.id, gen: n.gen + 1, dist: n.dist + 1})
			// Nearest first: after every queued post as near or nearer.
			k := kinNode{id: id, gen: n.gen + 1, dist: n.dist + 1}
			at := slices.IndexFunc(queue, func(q kinNode) bool { return q.dist > k.dist })
			if at < 0 {
				at = len(queue)
			}
			queue = slices.Insert(queue, at, k)
		}
	}
	if len(out) == 0 {
		return nil, nil
	}

	cs := make([]core.Checked, len(out))
	for i, k := range out {
		cs[i] = k.c
	}
	views, err := s.views(ctx, sr, cs, true)
	if err != nil {
		return nil, err
	}
	kin := make([]gate.TreeKin, len(out))
	for i, k := range out {
		_, also := s.chips(ctx, sr, sn.viewer, k.c, k.parent)
		kin[i] = gate.TreeKin{
			Post: views[i], Parent: s.d.Refs.Post(sn.viewer, k.parent), Gen: k.gen, Degree: k.dist, AlsoParents: also,
		}
	}
	return kin, nil
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
