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

// Person post orders (PersonPosts).
const (
	OrderNewest = "newest"
	OrderOldest = "oldest"
)

// personScan is how many of the author's posts one database read fetches
// while PersonPosts fills a page.
const personScan = 100

// PersonPosts returns the person's posts the caller can see, from their whole
// history, newest first or (order OrderOldest) oldest first. Every post passes
// the same checks as in the feed: Visibility.CheckPost (deleted, author active,
// distance, audience with the probation narrowing; blocks and hide-via act
// through the caller's search) and the caller's hides. Mutes do not apply: the
// caller came to this person's page.
//
// No count is returned and a page is cut only after a visible post: Next is
// set exactly when another visible post follows, and the cursor names the last
// post served (a ref, MACed to the caller, the person and the order), so the
// pages reveal nothing about posts the caller cannot see. A cursor carries its
// order; order is read only on the first page.
func (s *Service) PersonPosts(ctx context.Context, who gate.Ref, cursor, order string) (gate.Page[gate.PostView], error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return gate.Page[gate.PostView]{}, err
	}
	u, sr, err := s.visible(ctx, v.ID, who)
	if err != nil {
		return gate.Page[gate.PostView]{}, err
	}
	oldest := order == OrderOldest
	var after *core.PostMeta
	if cursor != "" {
		var m core.PostMeta
		if m, oldest, err = s.openPostsCursor(ctx, v.ID, u, cursor); err != nil {
			return gate.Page[gate.PostView]{}, err
		}
		after = &m
	} else if order != "" && order != OrderNewest && order != OrderOldest {
		return gate.Page[gate.PostView]{}, gate.NewProblem(gate.StatusUnprocessable, "order_invalid", "order")
	}
	hidden := s.d.Engage.HiddenSet(ctx, v.ID)
	var checked []core.Checked
	for len(checked) <= PostsPageSize {
		batch, err := s.d.Posts.ByAuthor(ctx, u, after, oldest, personScan)
		if err != nil {
			return gate.Page[gate.PostView]{}, err
		}
		ps := make([]core.PostMeta, 0, len(batch))
		for _, m := range batch {
			if _, h := hidden[m.ID]; !h && m.Author == u && !m.Deleted {
				ps = append(ps, m)
			}
		}
		checked = append(checked, s.d.Vis.CheckPosts(ctx, sr, ps)...)
		if len(batch) < personScan {
			break
		}
		after = &batch[len(batch)-1]
	}
	page := gate.Page[gate.PostView]{Items: []gate.PostView{}}
	if len(checked) == 0 {
		return page, nil
	}
	more := len(checked) > PostsPageSize
	checked = checked[:min(len(checked), PostsPageSize)]
	if page.Items, err = s.d.Views.Posts(ctx, sr, checked, core.ViewOpts{WithBody: true, WithContext: true}); err != nil {
		return gate.Page[gate.PostView]{}, err
	}
	if more {
		page.Next = s.postsCursor(v.ID, u, oldest, checked[len(checked)-1].Meta().ID)
	}
	return page, nil
}

// postsMAC binds a person-posts cursor to the caller, the person and the order.
func (s *Service) postsMAC(v, u core.UserID, oldest bool, ref string) []byte {
	m := hmac.New(sha256.New, s.d.Keys.Cursor)
	var b [9]byte
	binary.BigEndian.PutUint32(b[:4], uint32(v))
	binary.BigEndian.PutUint32(b[4:8], uint32(u))
	if oldest {
		b[8] = 1
	}
	m.Write([]byte("pcc/person-posts-cursor/v1\x00"))
	m.Write(b[:])
	m.Write([]byte(ref))
	return m.Sum(nil)[:16]
}

// postsCursor is order (1 byte) ‖ MAC (16) ‖ the caller's ref of the last post served.
func (s *Service) postsCursor(v, u core.UserID, oldest bool, last core.PostID) string {
	ref := string(s.d.Refs.Post(v, last))
	b := []byte{0}
	if oldest {
		b[0] = 1
	}
	b = append(b, s.postsMAC(v, u, oldest, ref)...)
	return base64.RawURLEncoding.EncodeToString(append(b, ref...))
}

// openPostsCursor returns the post a cursor resumes after and its order; a
// cursor that does not verify for this caller and person is gate.ErrNotFound.
func (s *Service) openPostsCursor(ctx context.Context, v, u core.UserID, c string) (core.PostMeta, bool, error) {
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil || len(b) < 18 || b[0] > 1 {
		return core.PostMeta{}, false, gate.ErrNotFound
	}
	oldest, ref := b[0] == 1, string(b[17:])
	if !hmac.Equal(b[1:17], s.postsMAC(v, u, oldest, ref)) {
		return core.PostMeta{}, false, gate.ErrNotFound
	}
	id, ok := s.d.Refs.OpenPost(v, gate.Ref(ref))
	if !ok {
		return core.PostMeta{}, false, gate.ErrNotFound
	}
	m, ok := s.d.Posts.Meta(ctx, id)
	if !ok || m.Author != u {
		return core.PostMeta{}, false, gate.ErrNotFound
	}
	return m, oldest, nil
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
