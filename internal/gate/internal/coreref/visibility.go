package coreref

import (
	"context"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// MemVisibility implements §2.2, §4 rules 1/4/5 and §10.1.3 exactly, with
// one-directional blocks (D7): the reader's search excludes only the reader's own
// blocks and hide-vias. It uses D.Graph, D.Users and D.Posts.
type MemVisibility struct{ D *core.Deps }

var _ core.Visibility = (*MemVisibility)(nil)

// SearchFor runs Graph.Search with v's own exclusions.
func (m *MemVisibility) SearchFor(ctx context.Context, v core.UserID) (core.Search, error) {
	return m.D.Graph.Search(ctx, v, m.D.Graph.Exclusions(v))
}

// CanSeeUser ⇔ finite distance in s. Nothing else (D7).
func (m *MemVisibility) CanSeeUser(s core.Search, u core.UserID) bool {
	_, ok := s.Dist(u)
	return ok
}

// CheckPost ⇔ not deleted ∧ author active ∧ finite distance ∧ audience allows it.
// Fails closed on any lookup error.
func (m *MemVisibility) CheckPost(ctx context.Context, s core.Search, p core.PostMeta) (core.Checked, bool) {
	if p.Deleted {
		return core.Checked{}, false
	}
	d, ok := s.Dist(p.Author)
	if !ok {
		return core.Checked{}, false
	}
	author, err := m.D.Users.Get(ctx, p.Author)
	if err != nil || author.Status != core.StatusActive {
		return core.Checked{}, false
	}
	switch p.Audience {
	case core.AudConnected:
	case core.AudD1, core.AudD2, core.AudD3:
		if d > int(p.Audience) {
			return core.Checked{}, false
		}
	case core.AudFriendPlus:
		if d == 1 {
			lvl, ok := m.D.Graph.Level(s.Viewer(), p.Author)
			if !ok || lvl != core.LevelFriendPlus {
				return core.Checked{}, false
			}
		} else if d != 0 {
			return core.Checked{}, false
		}
	default:
		return core.Checked{}, false
	}
	return core.MintChecked(p, s.Viewer(), s.Epoch()), true
}

// CheckPosts is an order-preserving filter.
func (m *MemVisibility) CheckPosts(ctx context.Context, s core.Search, ps []core.PostMeta) []core.Checked {
	out := make([]core.Checked, 0, len(ps))
	for _, p := range ps {
		if c, ok := m.CheckPost(ctx, s, p); ok {
			out = append(out, c)
		}
	}
	return out
}

// CanSeeLink is §10.1.3: V sees C and P; not detached; not shadow unless V is C's
// author; and (P has no audience limit — forced counts — and the default
// ref_policy) or C's author is V, P's author, or one of V's friends.
func (m *MemVisibility) CanSeeLink(ctx context.Context, s core.Search, l core.LinkMeta, child, parent core.PostMeta) bool {
	if _, ok := m.CheckPost(ctx, s, child); !ok {
		return false
	}
	if _, ok := m.CheckPost(ctx, s, parent); !ok {
		return false
	}
	if l.Detached {
		return false
	}
	v := s.Viewer()
	if l.Shadow && child.Author != v {
		return false
	}
	open := parent.Audience == core.AudConnected && !parent.AudienceForced && parent.RefPolicy == core.PolAnyone
	if open || child.Author == v || child.Author == parent.Author {
		return true
	}
	d, ok := s.Dist(child.Author)
	return ok && d == 1
}

// VisibleChildren walks children newest-first and keeps a link only if it is
// visible and passes f; Limit applies to KEPT links (fidelity #2).
func (m *MemVisibility) VisibleChildren(ctx context.Context, s core.Search, parent core.PostMeta, f core.ChildFilter) []core.VisibleLink {
	var out []core.VisibleLink
	m.D.Posts.Children(ctx, parent.ID, func(l core.LinkMeta) bool {
		if f.RefOnly && l.Kind != core.LinkRef {
			return true
		}
		if f.NotSelf && l.ChildAuthor == l.ParentAuthor {
			return true
		}
		child, ok := m.D.Posts.Meta(ctx, l.Child)
		if !ok || !m.CanSeeLink(ctx, s, l, child, parent) {
			return true
		}
		out = append(out, core.VisibleLink{Link: l, Other: core.MintChecked(child, s.Viewer(), s.Epoch())})
		return f.Limit <= 0 || len(out) < f.Limit
	})
	return out
}

// VisibleParents returns the visible parent links of child.
func (m *MemVisibility) VisibleParents(ctx context.Context, s core.Search, child core.PostMeta) []core.VisibleLink {
	var out []core.VisibleLink
	for _, l := range m.D.Posts.Parents(ctx, child.ID) {
		parent, ok := m.D.Posts.Meta(ctx, l.Parent)
		if !ok || !m.CanSeeLink(ctx, s, l, child, parent) {
			continue
		}
		out = append(out, core.VisibleLink{Link: l, Other: core.MintChecked(parent, s.Viewer(), s.Epoch())})
	}
	return out
}

// CanReference: the parent must be visible; cont ⇒ only the parent's author; ref ⇒
// the author always, otherwise by policy (anyone / two_hops / friends / nobody).
func (m *MemVisibility) CanReference(ctx context.Context, s core.Search, parent core.PostMeta, kind core.LinkKind) bool {
	if _, ok := m.CheckPost(ctx, s, parent); !ok {
		return false
	}
	v := s.Viewer()
	if kind == core.LinkCont {
		return parent.Author == v
	}
	if parent.Author == v {
		return true
	}
	d, _ := s.Dist(parent.Author)
	switch parent.RefPolicy {
	case core.PolAnyone:
		return true
	case core.PolTwoHops:
		return d <= 2
	case core.PolFriends:
		return d <= 1
	}
	return false
}

// MemChains builds §4.5 chains: first hop and author always; middle hops named
// only if every named edge has both endpoints opted in (name_in_chains).
type MemChains struct{ D *core.Deps }

var _ core.Chains = (*MemChains)(nil)

// Chain returns the chain from s's viewer to u, or gate.ErrNotFound.
func (m *MemChains) Chain(ctx context.Context, s core.Search, u core.UserID) (gate.ChainView, error) {
	d, ok := s.Dist(u)
	if !ok {
		return gate.ChainView{}, gate.ErrNotFound
	}
	cv := gate.ChainView{Dist: d, FirstHopPlus: s.FirstHopPlus(u)}
	if d <= 1 {
		return cv, nil
	}
	path := s.Path(u)
	if len(path) != d+1 {
		return gate.ChainView{}, gate.ErrNotFound
	}
	v := s.Viewer()
	named := path[1:] // first hop … author: endpoints of every named middle edge
	metas, err := m.D.Users.GetMany(ctx, named)
	if err != nil {
		return gate.ChainView{}, err
	}
	first := metas[path[1]]
	cv.FirstHop = &gate.ChainHop{Ref: m.D.Refs.User(v, path[1]), Handle: first.Handle}
	optIn := true
	for _, id := range named {
		if !metas[id].NameInChains {
			optIn = false
			break
		}
	}
	middle := path[2:d]
	if optIn {
		for _, id := range middle {
			cv.Middle = append(cv.Middle, gate.ChainHop{Ref: m.D.Refs.User(v, id), Handle: metas[id].Handle})
		}
	} else {
		cv.HiddenMiddle = len(middle)
	}
	return cv, nil
}
