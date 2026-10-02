// Package visibility implements the horizon rules (ARCHITECTURE §2.2, §4, §10.1.3):
// core.Visibility and core.Chains. Blocks are one-directional (D7): a reader's
// search excludes only the people the READER blocked (and hid-via). Being
// blocked by an author never hides that author's posts from you; that is what
// lets a blocked person's reference become a shadow link and keeps the block
// invisible to them.
//
// It is the only gate module (besides coreref) that mints core.Checked and
// reads raw links (archtest A10). Every check fails closed: a lookup error, an
// unknown audience or an unknown author means "not visible".
//
// Owner: P0-W02 → P1-W03.
package visibility

import (
	"context"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// Service implements core.Visibility and core.Chains.
type Service struct{ d *core.Deps }

var (
	_ core.Visibility = (*Service)(nil)
	_ core.Chains     = (*Service)(nil)
)

// New returns the service.
func New(d *core.Deps) *Service { return &Service{d: d} }

// Init does nothing: visibility is stateless over Graph, Users and PostIndex.
func (s *Service) Init(context.Context) error { return nil }

// SearchFor runs the viewer's search with the viewer's OWN exclusions only (D7).
func (s *Service) SearchFor(ctx context.Context, v core.UserID) (core.Search, error) {
	if v == 0 {
		return nil, gate.ErrNotFound
	}
	return s.d.Graph.Search(ctx, v, s.d.Graph.Exclusions(v))
}

// CanSeeUser ⇔ a finite distance in sr. Nothing else.
func (s *Service) CanSeeUser(sr core.Search, u core.UserID) bool {
	if sr == nil || u == 0 {
		return false
	}
	_, ok := sr.Dist(u)
	return ok
}

// authorCache memoizes author status lookups within one call.
type authorCache struct {
	s  *Service
	ok map[core.UserID]bool
}

func (s *Service) authors() *authorCache { return &authorCache{s: s, ok: map[core.UserID]bool{}} }

// active reports whether u exists and is active; any lookup error ⇒ false.
func (c *authorCache) active(ctx context.Context, u core.UserID) bool {
	if v, ok := c.ok[u]; ok {
		return v
	}
	m, err := c.s.d.Users.Get(ctx, u)
	v := err == nil && m.ID == u && m.Status == core.StatusActive
	c.ok[u] = v
	return v
}

// effectiveAudience folds the probation flag into the audience: a forced post is
// stored as d1 (D6), and a forced flag on anything wider is narrowed to d1.
func effectiveAudience(p core.PostMeta) core.Audience {
	if p.AudienceForced {
		switch p.Audience {
		case core.AudConnected, core.AudD2, core.AudD3:
			return core.AudD1
		}
	}
	return p.Audience
}

// check is CheckPost with a shared author cache.
func (s *Service) check(ctx context.Context, sr core.Search, p core.PostMeta, ac *authorCache) bool {
	if sr == nil || p.Deleted || p.Author == 0 {
		return false
	}
	d, ok := sr.Dist(p.Author)
	if !ok {
		return false
	}
	switch effectiveAudience(p) {
	case core.AudConnected:
	case core.AudD1:
		if d > 1 {
			return false
		}
	case core.AudD2:
		if d > 2 {
			return false
		}
	case core.AudD3:
		if d > 3 {
			return false
		}
	case core.AudFriendPlus:
		switch d {
		case 0:
		case 1:
			lvl, ok := s.d.Graph.Level(sr.Viewer(), p.Author)
			if !ok || lvl != core.LevelFriendPlus {
				return false
			}
		default:
			return false
		}
	default:
		return false // unknown audience: fail closed
	}
	return ac.active(ctx, p.Author)
}

// CheckPost ⇔ not deleted ∧ author active ∧ dist(V, author) finite ∧ the
// audience allows it (§4 rules 1, 4, 5). It mints the Checked capability.
func (s *Service) CheckPost(ctx context.Context, sr core.Search, p core.PostMeta) (core.Checked, bool) {
	if !s.check(ctx, sr, p, s.authors()) {
		return core.Checked{}, false
	}
	return core.MintChecked(p, sr.Viewer(), sr.Epoch()), true
}

// CheckPosts is an order-preserving filter.
func (s *Service) CheckPosts(ctx context.Context, sr core.Search, ps []core.PostMeta) []core.Checked {
	out := make([]core.Checked, 0, len(ps))
	ac := s.authors()
	for _, p := range ps {
		if s.check(ctx, sr, p, ac) {
			out = append(out, core.MintChecked(p, sr.Viewer(), sr.Epoch()))
		}
	}
	return out
}

// linkShown is §10.1.3 given that V already sees both C and P.
func linkShown(sr core.Search, l core.LinkMeta, child, parent core.PostMeta) bool {
	if l.Detached || l.Child != child.ID || l.Parent != parent.ID {
		return false
	}
	v := sr.Viewer()
	if l.Shadow && child.Author != v {
		return false
	}
	open := parent.Audience == core.AudConnected && !parent.AudienceForced && parent.RefPolicy == core.PolAnyone
	if open || child.Author == v || child.Author == parent.Author {
		return true
	}
	d, ok := sr.Dist(child.Author)
	return ok && d == 1
}

// CanSeeLink is §10.1.3 exactly: V sees C and P; the link is not detached; not
// shadow unless V is C's author; and (P has no audience limit — a forced one
// counts — and the default ref_policy) or C's author is V, P's author, or one of
// V's friends.
func (s *Service) CanSeeLink(ctx context.Context, sr core.Search, l core.LinkMeta, child, parent core.PostMeta) bool {
	ac := s.authors()
	return s.check(ctx, sr, child, ac) && s.check(ctx, sr, parent, ac) && linkShown(sr, l, child, parent)
}

// VisibleChildren walks PostIndex.Children newest-first and keeps a link only if
// the filter and CanSeeLink hold; Limit counts KEPT links, so invisible children
// can never push visible ones out of the window (fidelity #2).
func (s *Service) VisibleChildren(ctx context.Context, sr core.Search, parent core.PostMeta, f core.ChildFilter) []core.VisibleLink {
	ac := s.authors()
	if !s.check(ctx, sr, parent, ac) {
		return nil
	}
	var out []core.VisibleLink
	s.d.Posts.Children(ctx, parent.ID, func(l core.LinkMeta) bool {
		if ctx.Err() != nil {
			return false
		}
		if f.RefOnly && l.Kind != core.LinkRef {
			return true
		}
		if f.NotSelf && l.ChildAuthor == l.ParentAuthor {
			return true
		}
		child, ok := s.d.Posts.Meta(ctx, l.Child)
		if !ok || !s.check(ctx, sr, child, ac) || !linkShown(sr, l, child, parent) {
			return true
		}
		out = append(out, core.VisibleLink{Link: l, Other: core.MintChecked(child, sr.Viewer(), sr.Epoch())})
		return f.Limit <= 0 || len(out) < f.Limit
	})
	return out
}

// VisibleParents returns child's visible parent links, in stored order.
func (s *Service) VisibleParents(ctx context.Context, sr core.Search, child core.PostMeta) []core.VisibleLink {
	ac := s.authors()
	if !s.check(ctx, sr, child, ac) {
		return nil
	}
	var out []core.VisibleLink
	for _, l := range s.d.Posts.Parents(ctx, child.ID) {
		parent, ok := s.d.Posts.Meta(ctx, l.Parent)
		if !ok || !s.check(ctx, sr, parent, ac) || !linkShown(sr, l, child, parent) {
			continue
		}
		out = append(out, core.VisibleLink{Link: l, Other: core.MintChecked(parent, sr.Viewer(), sr.Epoch())})
	}
	return out
}

// CanReference: the parent must be visible. cont ⇒ only the parent's author,
// whatever the policy. ref ⇒ the parent's author always; otherwise anyone ⇒
// yes, two_hops ⇒ dist ≤ 2, friends ⇒ dist ≤ 1, nobody (slippery) ⇒ no
// (fidelity #27).
func (s *Service) CanReference(ctx context.Context, sr core.Search, parent core.PostMeta, kind core.LinkKind) bool {
	if !s.check(ctx, sr, parent, s.authors()) {
		return false
	}
	v := sr.Viewer()
	switch kind {
	case core.LinkCont:
		return parent.Author == v
	case core.LinkRef:
	default:
		return false
	}
	if parent.Author == v {
		return true
	}
	d, ok := sr.Dist(parent.Author)
	if !ok {
		return false
	}
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

// Chain builds the §4.5 chain from sr's viewer to u: the first hop and the
// author are always shown; the middle hops are named only if every named edge
// has both endpoints opted in (name_in_chains), else they collapse into
// HiddenMiddle = dist − 2. gate.ErrNotFound unless u is visible.
func (s *Service) Chain(ctx context.Context, sr core.Search, u core.UserID) (gate.ChainView, error) {
	if !s.CanSeeUser(sr, u) {
		return gate.ChainView{}, gate.ErrNotFound
	}
	d, _ := sr.Dist(u)
	cv := gate.ChainView{Dist: d, FirstHopPlus: sr.FirstHopPlus(u)}
	if d <= 1 {
		return cv, nil
	}
	path := sr.Path(u)
	if len(path) != d+1 || path[0] != sr.Viewer() || path[d] != u || path[1] != sr.FirstHop(u) {
		return gate.ChainView{}, gate.ErrNotFound // inconsistent search: fail closed
	}
	v := sr.Viewer()
	named := path[1:] // first hop … author: the endpoints of every named middle edge
	metas, err := s.d.Users.GetMany(ctx, named)
	if err != nil {
		return gate.ChainView{}, err
	}
	first, ok := metas[path[1]]
	if !ok {
		return gate.ChainView{}, gate.ErrNotFound
	}
	cv.FirstHop = &gate.ChainHop{Ref: s.d.Refs.User(v, path[1]), Handle: first.Handle}
	middle := path[2:d]
	optIn := true
	for _, id := range named {
		if m, ok := metas[id]; !ok || !m.NameInChains || m.Status != core.StatusActive {
			optIn = false
			break
		}
	}
	if optIn && len(middle) > 0 {
		cv.Middle = make([]gate.ChainHop, 0, len(middle))
		for _, id := range middle {
			cv.Middle = append(cv.Middle, gate.ChainHop{Ref: s.d.Refs.User(v, id), Handle: metas[id].Handle})
		}
	} else {
		cv.HiddenMiddle = len(middle)
	}
	return cv, nil
}
