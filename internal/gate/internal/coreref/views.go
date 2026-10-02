package coreref

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// MemViews builds minimal but correct views (plan §4.7 views) from Checked
// input only. It uses D.Users, D.Chains, D.Vis, D.Posts, D.Engage, D.Graph and D.Refs.
// It renders no previews, mentions or media (those are P0-W09/P1-W12 additions).
type MemViews struct{ D *core.Deps }

var _ core.Views = (*MemViews)(nil)

// Author returns the compact author view of u, or gate.ErrNotFound.
func (m *MemViews) Author(ctx context.Context, s core.Search, u core.UserID) (gate.AuthorView, error) {
	if !m.D.Vis.CanSeeUser(s, u) {
		return gate.AuthorView{}, gate.ErrNotFound
	}
	meta, err := m.D.Users.Get(ctx, u)
	if err != nil {
		return gate.AuthorView{}, gate.ErrNotFound
	}
	label, err := m.D.Users.InviterLabel(ctx, u)
	if err != nil {
		return gate.AuthorView{}, err
	}
	chain, err := m.D.Chains.Chain(ctx, s, u)
	if err != nil {
		return gate.AuthorView{}, err
	}
	return gate.AuthorView{Ref: m.D.Refs.User(s.Viewer(), u), Handle: meta.Handle, DisplayName: meta.DisplayName,
		Inviter: label, Chain: chain}, nil
}

// Person returns the person view of u, or gate.ErrNotFound unless CanSeeUser.
func (m *MemViews) Person(ctx context.Context, s core.Search, u core.UserID) (gate.PersonView, error) {
	a, err := m.Author(ctx, s, u)
	if err != nil {
		return gate.PersonView{}, err
	}
	pv := gate.PersonView{Ref: a.Ref, Handle: a.Handle, DisplayName: a.DisplayName, Inviter: a.Inviter, Chain: a.Chain,
		Relation: "connected"}
	v := s.Viewer()
	if u == v {
		pv.Relation = "self"
	} else if lvl, ok := m.D.Graph.Level(v, u); ok {
		pv.Relation = "friend"
		if lvl == core.LevelFriendPlus {
			pv.Relation = "friend_plus"
		}
	}
	if m.D.Graph.Blocks(v, u) {
		pv.Blocked = true
	}
	if m.D.Mutes != nil {
		if _, ok := m.D.Mutes.MutedAuthors(ctx, v)[u]; ok {
			pv.Muted = true
		}
	}
	return pv, nil
}

// Posts builds PostViews for checked posts, in order.
func (m *MemViews) Posts(ctx context.Context, s core.Search, ps []core.Checked, o core.ViewOpts) ([]gate.PostView, error) {
	v := s.Viewer()
	ids := make([]core.PostID, len(ps))
	for i, c := range ps {
		ids[i] = c.Meta().ID
	}
	var bodies map[core.PostID]string
	if o.WithBody {
		bodies = m.D.Posts.Bodies(ctx, ids)
	}
	liked := m.D.Engage.LikedByMe(ctx, v, ids)
	near := s.LayerBitmap(1)
	near.Or(s.LayerBitmap(2))
	out := make([]gate.PostView, 0, len(ps))
	for _, c := range ps {
		pm := c.Meta()
		author, err := m.Author(ctx, s, pm.Author)
		if err != nil {
			return nil, err
		}
		pv := gate.PostView{
			Ref: m.D.Refs.Post(v, pm.ID), Author: author, Tier: author.Chain.Dist, Body: bodies[pm.ID],
			Lang: pm.Lang, CreatedAt: pm.CreatedAt, EditedAt: pm.EditedAt, Audience: pm.Audience.String(),
			RefPolicy: pm.RefPolicy.String(), Mine: pm.Author == v,
			CanReference: m.D.Vis.CanReference(ctx, s, pm, core.LinkRef),
		}
		if pv.Lang == "" {
			pv.Lang = "und"
		}
		if pv.Mine {
			pv.AudienceForced = pm.AudienceForced
		}
		// Likes: likers within 2 hops (plus the viewer), named friends who allow it.
		likers := m.D.Engage.LikerSet(ctx, pm.ID)
		inNet := likers.Clone()
		inNet.And(near)
		pv.Likes.InNetwork = int(inNet.GetCardinality())
		if likers.Contains(uint32(v)) {
			pv.Likes.InNetwork++
		}
		pv.Likes.ByMe = liked[pm.ID]
		friends := s.LayerBitmap(1)
		friends.And(likers)
		named := 0
		friends.Iterate(func(x uint32) bool {
			meta, err := m.D.Users.Get(ctx, core.UserID(x))
			if err == nil && meta.NamedInLikes && named < 3 {
				pv.Likes.Names = append(pv.Likes.Names, gate.ChainHop{Ref: m.D.Refs.User(v, meta.ID), Handle: meta.Handle})
				named++
			}
			return true
		})
		pv.Likes.Others = pv.Likes.InNetwork - named
		if pv.Likes.ByMe {
			pv.Likes.Others--
		}
		if pv.Likes.Others < 0 {
			pv.Likes.Others = 0
		}
		pv.ReplyCount = len(m.D.Vis.VisibleChildren(ctx, s, pm, core.ChildFilter{}))
		if o.WithContext {
			for _, l := range m.D.Vis.VisibleParents(ctx, s, pm) {
				parent := l.Other.Meta()
				pa, err := m.D.Users.Get(ctx, parent.Author)
				if err != nil {
					continue
				}
				body := m.D.Posts.Bodies(ctx, []core.PostID{parent.ID})[parent.ID]
				pv.Context = append(pv.Context, gate.ContextRow{Ref: m.D.Refs.Post(v, parent.ID), Kind: l.Link.Kind.String(),
					AuthorHandle: pa.Handle, Excerpt: Excerpt(body, 80)})
			}
		}
		out = append(out, pv)
	}
	return out, nil
}

// Excerpt returns the first n runes of body with simple Markdown markers stripped.
func Excerpt(body string, n int) string {
	r := strings.NewReplacer("#", "", "*", "", "_", "", "~", "", "`", "", ">", "", "\n", " ", "\r", " ")
	s := strings.Join(strings.Fields(r.Replace(body)), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	rs := []rune(s)
	return string(rs[:n])
}
