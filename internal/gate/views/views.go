// Package views builds every PostView / PersonView / AuthorView from core.Checked
// input only (plan §4.7 views). It implements core.Views.
//
//   - Chains come from core.Chains; Tier = Author.Chain.Dist.
//   - Context rows come from Visibility.VisibleParents only; the excerpt is the
//     first 80 runes of the parent body with Markdown stripped.
//   - Likes: InNetwork = |likers ∩ (L1 ∪ L2)| plus the viewer's own like; Names =
//     the viewer's friends among the likers who allow naming (named_in_likes),
//     at most 3, oldest edge first; Others = InNetwork − Names − the viewer.
//   - ReplyCount = the number of visible ref children (no limit).
//   - Thread = the position over the VISIBLE parts of a cont thread (1-based).
//   - Previews (link_previews, read-only) only when status = ok ∧ now ≥ ready_at.
//   - Mentions carry a Ref only if the viewer can see the target.
//   - AudienceForced only on the author's own view.
//
// Owner: P0-W02 → P1-W12.
package views

import (
	"context"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/urlx"
)

// ExcerptRunes is the length of a context-row excerpt.
const ExcerptRunes = 80

// maxNames is the number of friends named in a like summary.
const maxNames = 3

// maxThreadWalk bounds a thread walk (threads have ≤ 25 parts; this only guards
// against corrupt data).
const maxThreadWalk = 1000

// Service implements core.Views.
type Service struct{ d *core.Deps }

var _ core.Views = (*Service)(nil)

// New returns the service.
func New(d *core.Deps) *Service { return &Service{d: d} }

// Init does nothing: views are built on demand from the other modules.
func (s *Service) Init(context.Context) error { return nil }

// Local adapters until contract request p0-w02-1 lands (core.MentionIndex and
// core.RequestStates): implementers are found by type assertion and their
// absence degrades to "no mentions" / "no request" (fail closed).
type mentionIndex interface {
	MentionTargets(ctx context.Context, p core.PostID) map[int]core.UserID
}

type requestStates interface {
	RequestState(ctx context.Context, v, u core.UserID) string
}

func (s *Service) mentionIndex() mentionIndex {
	if m, ok := s.d.Posts.(mentionIndex); ok {
		return m
	}
	return nil
}

func (s *Service) requestStates() requestStates {
	for _, x := range []any{s.d.Edges, s.d.Mutes} {
		if r, ok := x.(requestStates); ok {
			return r
		}
	}
	return nil
}

// ---- people ----

// Author returns the compact author view of u, or gate.ErrNotFound unless the
// viewer can see u.
func (s *Service) Author(ctx context.Context, sr core.Search, u core.UserID) (gate.AuthorView, error) {
	if sr == nil || !s.d.Vis.CanSeeUser(sr, u) {
		return gate.AuthorView{}, gate.ErrNotFound
	}
	meta, err := s.d.Users.Get(ctx, u)
	if err != nil {
		return gate.AuthorView{}, err
	}
	label, err := s.d.Users.InviterLabel(ctx, u)
	if err != nil {
		return gate.AuthorView{}, err
	}
	chain, err := s.d.Chains.Chain(ctx, sr, u)
	if err != nil {
		return gate.AuthorView{}, err
	}
	return gate.AuthorView{Ref: s.d.Refs.User(sr.Viewer(), u), Handle: meta.Handle, DisplayName: meta.DisplayName,
		Inviter: label, Chain: chain}, nil
}

// Person returns the person view of u, or gate.ErrNotFound unless the viewer
// can see u.
func (s *Service) Person(ctx context.Context, sr core.Search, u core.UserID) (gate.PersonView, error) {
	a, err := s.Author(ctx, sr, u)
	if err != nil {
		return gate.PersonView{}, err
	}
	v := sr.Viewer()
	pv := gate.PersonView{Ref: a.Ref, Handle: a.Handle, DisplayName: a.DisplayName, Inviter: a.Inviter, Chain: a.Chain,
		Relation: "connected"}
	switch lvl, ok := s.d.Graph.Level(v, u); {
	case u == v:
		pv.Relation = "self"
		return pv, nil
	case ok && lvl == core.LevelFriendPlus:
		pv.Relation = "friend_plus"
	case ok:
		pv.Relation = "friend"
	}
	pv.Blocked = s.d.Graph.Blocks(v, u) // only ever the viewer's own block
	if hv := s.d.Graph.Exclusions(v).HiddenVia; hv != nil && hv.Contains(uint32(u)) {
		pv.HiddenVia = true
	}
	if s.d.Mutes != nil {
		if _, ok := s.d.Mutes.MutedAuthors(ctx, v)[u]; ok {
			pv.Muted = true
		}
	}
	if pv.Relation == "connected" {
		if rs := s.requestStates(); rs != nil {
			switch st := rs.RequestState(ctx, v, u); st {
			case "outgoing", "incoming":
				pv.Request = st
			}
		}
	}
	return pv, nil
}

// ---- posts ----

// postCtx carries per-call caches.
type postCtx struct {
	s       *Service
	sr      core.Search
	authors map[core.UserID]gate.AuthorView
	near    func(core.UserID) bool
	friends []core.Edge // the viewer's visible friends, oldest edge first
}

func (pc *postCtx) author(ctx context.Context, u core.UserID) (gate.AuthorView, error) {
	if a, ok := pc.authors[u]; ok {
		return a, nil
	}
	a, err := pc.s.Author(ctx, pc.sr, u)
	if err != nil {
		return gate.AuthorView{}, err
	}
	pc.authors[u] = a
	return a, nil
}

// Posts builds PostViews for checked posts, in order. Every input must have
// been checked for this viewer; anything else is refused (gate.ErrNotFound).
func (s *Service) Posts(ctx context.Context, sr core.Search, ps []core.Checked, o core.ViewOpts) ([]gate.PostView, error) {
	if sr == nil {
		return nil, gate.ErrNotFound
	}
	v := sr.Viewer()
	ids := make([]core.PostID, len(ps))
	for i, c := range ps {
		if c.Viewer() != v || c.Meta().ID == uuid.Nil {
			return nil, gate.ErrNotFound
		}
		ids[i] = c.Meta().ID
	}
	out := make([]gate.PostView, 0, len(ps))
	if len(ps) == 0 {
		return out, nil
	}
	l1, l2 := sr.LayerBitmap(1), sr.LayerBitmap(2)
	pc := &postCtx{s: s, sr: sr, authors: map[core.UserID]gate.AuthorView{},
		near: func(u core.UserID) bool { return l1.Contains(uint32(u)) || l2.Contains(uint32(u)) }}
	for _, e := range s.d.Graph.Friends(v) {
		if l1.Contains(uint32(e.To)) {
			pc.friends = append(pc.friends, e)
		}
	}
	slices.SortStableFunc(pc.friends, func(a, b core.Edge) int {
		if c := a.Since.Compare(b.Since); c != 0 {
			return c
		}
		return cmpID(a.To, b.To)
	})

	var bodies map[core.PostID]string
	if o.WithBody {
		bodies = s.d.Posts.Bodies(ctx, ids)
	}
	liked := s.d.Engage.LikedByMe(ctx, v, ids)
	previews, err := s.previews(ctx, ids)
	if err != nil {
		return nil, err
	}
	mi := s.mentionIndex()

	for _, c := range ps {
		pm := c.Meta()
		author, err := pc.author(ctx, pm.Author)
		if err != nil {
			return nil, err
		}
		pv := gate.PostView{
			Ref: s.d.Refs.Post(v, pm.ID), Author: author, Tier: author.Chain.Dist, Body: bodies[pm.ID],
			Lang: pm.Lang, CreatedAt: pm.CreatedAt, EditedAt: pm.EditedAt, Audience: pm.Audience.String(),
			RefPolicy: pm.RefPolicy.String(), Mine: pm.Author == v,
			CanReference: s.d.Vis.CanReference(ctx, sr, pm, core.LinkRef),
			Previews:     previews[pm.ID],
		}
		if pv.Lang == "" {
			pv.Lang = "und"
		}
		if pv.Mine {
			pv.AudienceForced = pm.AudienceForced
		}
		pv.Likes = pc.likes(ctx, pm.ID, liked[pm.ID])
		if saved := s.d.Engage.SaverSet(ctx, pm.ID); saved != nil && saved.Contains(uint32(v)) {
			pv.SavedByMe = true
		}
		pv.ReplyCount = len(s.d.Vis.VisibleChildren(ctx, sr, pm, core.ChildFilter{RefOnly: true}))
		pv.Thread = s.thread(ctx, sr, pm)
		if mi != nil {
			pv.Mentions = s.mentions(ctx, sr, mi.MentionTargets(ctx, pm.ID))
		}
		if o.WithContext {
			if pv.Context, err = s.context(ctx, sr, pm); err != nil {
				return nil, err
			}
		}
		out = append(out, pv)
	}
	return out, nil
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

// likes builds the in-network like summary of p (§5.3).
func (pc *postCtx) likes(ctx context.Context, p core.PostID, byMe bool) gate.LikeSummary {
	v := pc.sr.Viewer()
	ls := gate.LikeSummary{ByMe: byMe}
	likers := pc.s.d.Engage.LikerSet(ctx, p)
	if likers == nil {
		if byMe {
			ls.InNetwork = 1
		}
		return ls
	}
	likers.Iterate(func(x uint32) bool {
		if pc.near(core.UserID(x)) {
			ls.InNetwork++
		}
		return true
	})
	if likers.Contains(uint32(v)) {
		ls.InNetwork++
		ls.ByMe = true
	} else if byMe {
		ls.InNetwork++
	}
	for _, e := range pc.friends {
		if len(ls.Names) == maxNames {
			break
		}
		if !likers.Contains(uint32(e.To)) {
			continue
		}
		m, err := pc.s.d.Users.Get(ctx, e.To)
		if err != nil || !m.NamedInLikes || m.Status != core.StatusActive {
			continue
		}
		ls.Names = append(ls.Names, gate.ChainHop{Ref: pc.s.d.Refs.User(v, e.To), Handle: m.Handle})
	}
	ls.Others = ls.InNetwork - len(ls.Names)
	if ls.ByMe {
		ls.Others--
	}
	ls.Others = max(ls.Others, 0)
	return ls
}

// context builds the visible parent rows of pm.
func (s *Service) context(ctx context.Context, sr core.Search, pm core.PostMeta) ([]gate.ContextRow, error) {
	links := s.d.Vis.VisibleParents(ctx, sr, pm)
	if len(links) == 0 {
		return nil, nil
	}
	ids := make([]core.PostID, len(links))
	for i, l := range links {
		ids[i] = l.Other.Meta().ID
	}
	bodies := s.d.Posts.Bodies(ctx, ids)
	rows := make([]gate.ContextRow, 0, len(links))
	for _, l := range links {
		parent := l.Other.Meta()
		pa, err := s.d.Users.Get(ctx, parent.Author)
		if err != nil {
			return nil, err
		}
		rows = append(rows, gate.ContextRow{Ref: s.d.Refs.Post(sr.Viewer(), parent.ID), Kind: l.Link.Kind.String(),
			AuthorHandle: pa.Handle, Excerpt: Excerpt(bodies[parent.ID], ExcerptRunes)})
	}
	return rows, nil
}

// thread returns pm's 1-based position over the visible parts of its cont
// thread, or nil when pm is not part of a thread with another visible part.
// The walk stops at the first part the viewer cannot see (§10.1.3: traversal
// stops at hidden posts).
func (s *Service) thread(ctx context.Context, sr core.Search, pm core.PostMeta) *gate.ThreadPos {
	if pm.ContParent == uuid.Nil && pm.ContChild == uuid.Nil {
		return nil
	}
	before := 0
	for cur := pm; before < maxThreadWalk; before++ {
		next, ok := s.contParent(ctx, sr, cur)
		if !ok {
			break
		}
		cur = next
	}
	after := 0
	for cur := pm; after < maxThreadWalk && cur.ContChild != uuid.Nil; after++ {
		child, ok := s.d.Posts.Meta(ctx, cur.ContChild)
		if !ok {
			break
		}
		if p, ok := s.contParent(ctx, sr, child); !ok || p.ID != cur.ID {
			break
		}
		cur = child
	}
	if before+after == 0 {
		return nil
	}
	return &gate.ThreadPos{Index: before + 1, Total: before + after + 1}
}

// contParent returns the visible cont parent of child, if any.
func (s *Service) contParent(ctx context.Context, sr core.Search, child core.PostMeta) (core.PostMeta, bool) {
	if child.ContParent == uuid.Nil {
		return core.PostMeta{}, false
	}
	for _, l := range s.d.Vis.VisibleParents(ctx, sr, child) {
		if l.Link.Kind == core.LinkCont {
			return l.Other.Meta(), true
		}
	}
	return core.PostMeta{}, false
}

// mentions renders stored mentions: a Ref only if the viewer can see the target.
func (s *Service) mentions(_ context.Context, sr core.Search, targets map[int]core.UserID) []gate.MentionView {
	if len(targets) == 0 {
		return nil
	}
	ords := make([]int, 0, len(targets))
	for ord := range targets {
		ords = append(ords, ord)
	}
	slices.Sort(ords)
	out := make([]gate.MentionView, 0, len(ords))
	for _, ord := range ords {
		mv := gate.MentionView{Ord: ord}
		if t := targets[ord]; s.d.Vis.CanSeeUser(sr, t) {
			mv.Ref = s.d.Refs.User(sr.Viewer(), t)
		}
		out = append(out, mv)
	}
	return out
}

// previews reads ready, successful link previews (link_previews is read-only
// here; P0-W09 writes it). Without a database there are none.
func (s *Service) previews(ctx context.Context, ids []core.PostID) (map[core.PostID][]gate.PreviewView, error) {
	out := map[core.PostID][]gate.PreviewView{}
	if s.d.DB == nil || len(ids) == 0 {
		return out, nil
	}
	rows, err := s.d.DB.Q().Query(ctx, `SELECT post_id, url_canon, site_host, COALESCE(title, ''), COALESCE(description, '')
		FROM link_previews WHERE post_id = ANY($1) AND status = 1 AND ready_at <= $2 ORDER BY post_id, ord`,
		ids, s.now())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id                      core.PostID
			raw, host, title, descr string
		)
		if err := rows.Scan(&id, &raw, &host, &title, &descr); err != nil {
			return nil, err
		}
		pv := gate.PreviewView{URL: raw, Host: host, HostUnicode: host, Lookalike: true, Title: title, Description: descr}
		if u, err := url.Parse(raw); err == nil && u.Hostname() != "" {
			pv.HostUnicode, pv.Lookalike = urlx.DisplayHost(u)
		}
		out[id] = append(out[id], pv)
	}
	return out, rows.Err()
}

// ---- excerpts ----

var (
	mdImage    = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
	mdLink     = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	mdAutolink = regexp.MustCompile(`<((?:https?|mailto):[^>\s]*)>`)
	mdLineHead = regexp.MustCompile(`(?m)^[ \t]*(?:#{1,6}[ \t]+|>+[ \t]?|[-*+][ \t]+|\d{1,9}[.)][ \t]+)`)
	mdFence    = regexp.MustCompile("(?m)^[ \t]*(?:```|~~~)[^\n]*$")
	mdRule     = regexp.MustCompile(`(?m)^[ \t]*(?:[-*_][ \t]*){3,}$`)
	mdMarks    = strings.NewReplacer("**", "", "__", "", "~~", "", "*", "", "_", "", "`", "", "\\", "")
)

// Excerpt returns the first n runes of a Markdown body as plain text: links and
// images keep their text, block markers, emphasis and code markers are dropped,
// and whitespace collapses to single spaces.
func Excerpt(body string, n int) string {
	if n <= 0 || body == "" {
		return ""
	}
	t := mdFence.ReplaceAllString(body, " ")
	t = mdRule.ReplaceAllString(t, " ")
	t = mdImage.ReplaceAllString(t, "$1")
	t = mdLink.ReplaceAllString(t, "$1")
	t = mdAutolink.ReplaceAllString(t, "$1")
	t = mdLineHead.ReplaceAllString(t, "")
	t = mdMarks.Replace(t)
	t = strings.Join(strings.Fields(t), " ")
	if utf8.RuneCountInString(t) <= n {
		return t
	}
	return strings.TrimRight(string([]rune(t)[:n]), " ")
}

// now is the clock reading used for preview readiness.
func (s *Service) now() time.Time {
	if s.d.Clock == nil {
		return time.Now().UTC()
	}
	return s.d.Clock.Now()
}
