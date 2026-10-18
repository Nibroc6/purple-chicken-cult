package friends

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"slices"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// Page sizes.
const (
	FriendsPageSize = 50
	PostsPageSize   = 20
)

// Suggestion is a people-you-may-know candidate (mutual friend COUNT only).
type Suggestion struct {
	Person gate.PersonView `json:"person"`
	Mutual int             `json:"mutual"`
}

// ---- cursors ----

// Cursors are an offset bound to the viewer and the list they page through,
// MACed under the cursor key: another viewer's cursor, or one for another
// list, fails like a bad ref.
func (s *Service) mac(v core.UserID, list string, off uint32) []byte {
	m := hmac.New(sha256.New, s.d.Keys.Cursor)
	var b [8]byte
	binary.BigEndian.PutUint32(b[:4], uint32(v))
	binary.BigEndian.PutUint32(b[4:], off)
	m.Write([]byte("pcc/friends-cursor/v1\x00" + list + "\x00"))
	m.Write(b[:])
	return m.Sum(nil)[:16]
}

func (s *Service) cursor(v core.UserID, list string, off int) string {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(off))                                             //nolint:gosec // offsets are bounded by FriendCap / post counts
	return base64.RawURLEncoding.EncodeToString(append(b[:], s.mac(v, list, uint32(off))...)) //nolint:gosec // same
}

func (s *Service) offset(v core.UserID, list, c string) (int, error) {
	if c == "" {
		return 0, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil || len(b) != 20 {
		return 0, gate.ErrNotFound
	}
	off := binary.BigEndian.Uint32(b[:4])
	if !hmac.Equal(b[4:], s.mac(v, list, off)) {
		return 0, gate.ErrNotFound
	}
	return int(off), nil
}

// ---- handler-facing API ----

// List returns the caller's friends, friend+ first, then oldest friendship first.
func (s *Service) List(ctx context.Context, cursor string) (gate.Page[gate.PersonView], error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return gate.Page[gate.PersonView]{}, err
	}
	off, err := s.offset(v.ID, "friends", cursor)
	if err != nil {
		return gate.Page[gate.PersonView]{}, err
	}
	sr, err := s.d.Vis.SearchFor(ctx, v.ID)
	if err != nil {
		return gate.Page[gate.PersonView]{}, err
	}
	fs := s.d.Graph.Friends(v.ID)
	ids := make([]core.UserID, 0, len(fs))
	for _, f := range fs {
		ids = append(ids, f.To)
	}
	page := gate.Page[gate.PersonView]{Items: []gate.PersonView{}}
	if off >= len(ids) {
		return page, nil
	}
	end := min(off+FriendsPageSize, len(ids))
	if page.Items, err = s.people(ctx, sr, ids[off:end]); err != nil {
		return gate.Page[gate.PersonView]{}, err
	}
	if end < len(ids) {
		page.Next = s.cursor(v.ID, "friends", end)
	}
	return page, nil
}

// Person returns a person the caller can see, or someone the caller blocked.
func (s *Service) Person(ctx context.Context, who gate.Ref) (gate.PersonView, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return gate.PersonView{}, err
	}
	u, err := s.resolve(v.ID, who)
	if err != nil {
		return gate.PersonView{}, err
	}
	if s.d.Graph.Blocks(v.ID, u) {
		return s.blockedView(ctx, v.ID, u)
	}
	sr, err := s.d.Vis.SearchFor(ctx, v.ID)
	if err != nil {
		return gate.PersonView{}, err
	}
	return s.d.Views.Person(ctx, sr, u)
}

// PersonPosts returns a person's posts the caller can see, newest first.
// Phase 0 lists the feed window; older posts stay reachable through threads.
func (s *Service) PersonPosts(ctx context.Context, who gate.Ref, cursor string) (gate.Page[gate.PostView], error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return gate.Page[gate.PostView]{}, err
	}
	u, sr, err := s.visible(ctx, v.ID, who)
	if err != nil {
		return gate.Page[gate.PostView]{}, err
	}
	list := "posts:" + string(who)
	off, err := s.offset(v.ID, list, cursor)
	if err != nil {
		return gate.Page[gate.PostView]{}, err
	}
	ids := s.d.Posts.Recent(u)
	metas := s.d.Posts.MetaMany(ctx, ids)
	ps := make([]core.PostMeta, 0, len(ids))
	for _, id := range ids {
		if m, ok := metas[id]; ok && !m.Deleted {
			ps = append(ps, m)
		}
	}
	checked := s.d.Vis.CheckPosts(ctx, sr, ps)
	page := gate.Page[gate.PostView]{Items: []gate.PostView{}}
	if off >= len(checked) {
		return page, nil
	}
	end := min(off+PostsPageSize, len(checked))
	if page.Items, err = s.d.Views.Posts(ctx, sr, checked[off:end], core.ViewOpts{WithBody: true, WithContext: true}); err != nil {
		return gate.Page[gate.PostView]{}, err
	}
	if end < len(checked) {
		page.Next = s.cursor(v.ID, list, end)
	}
	return page, nil
}

// Suggestions returns people at distance 2 who share at least SuggestMinMutual
// friends with the caller through edges at least SuggestEdgeAge old, who allow
// suggestions and whom the caller has not blocked. Only the mutual COUNT is
// returned (§10.5).
func (s *Service) Suggestions(ctx context.Context) ([]Suggestion, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return nil, err
	}
	sr, err := s.d.Vis.SearchFor(ctx, v.ID)
	if err != nil {
		return nil, err
	}
	cutoff := s.d.Clock.Now().Add(-SuggestEdgeAge)
	mutual := map[core.UserID]int{}
	for _, f := range s.d.Graph.Friends(v.ID) {
		if f.Since.After(cutoff) {
			continue
		}
		if d, ok := sr.Dist(f.To); !ok || d != 1 {
			continue
		}
		for _, g := range s.d.Graph.Friends(f.To) {
			if g.To == v.ID || g.Since.After(cutoff) {
				continue
			}
			mutual[g.To]++
		}
	}
	cands := make([]core.UserID, 0, len(mutual))
	for u, n := range mutual {
		if d, ok := sr.Dist(u); n >= SuggestMinMutual && ok && d == 2 && !s.d.Graph.Blocks(v.ID, u) {
			cands = append(cands, u)
		}
	}
	slices.SortFunc(cands, func(a, b core.UserID) int {
		if mutual[a] != mutual[b] {
			return mutual[b] - mutual[a]
		}
		return int(int64(a) - int64(b))
	})
	metas, err := s.d.Users.GetMany(ctx, cands)
	if err != nil {
		return nil, err
	}
	out := []Suggestion{}
	for _, u := range cands {
		if len(out) == SuggestMax {
			break
		}
		if m, ok := metas[u]; !ok || !m.Suggestible || m.Status != core.StatusActive {
			continue
		}
		pv, err := s.d.Views.Person(ctx, sr, u)
		if err != nil {
			continue
		}
		out = append(out, Suggestion{Person: pv, Mutual: mutual[u]})
	}
	return out, nil
}
