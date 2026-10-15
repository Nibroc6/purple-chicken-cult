package devseed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"regexp"
	"strings"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/ids"
)

// Fixture is the document format of testdata/fixtures/<name>.json. Users are
// referred to by handle and posts by label everywhere; a handle or label not
// defined in the fixture must already exist (users by handle, posts by the
// label-derived ID ids.Named("post:"+label), the same IDs coretest.World uses).
// Times are given as ages: Go durations before the clock's now.
//
//	{
//	  "users":   [{"handle": "alice", "name": "Alice"},
//	              {"handle": "bob", "invited_by": "alice", "probation": "72h", "role": "moderator"}],
//	  "friends": [{"a": "alice", "b": "bob", "level": "friend_plus"}],
//	  "blocks":  [["bob", "carol"]],                      // [blocker, blocked]
//	  "mutes":   [["alice", "carol"]],                    // [user, muted author]
//	  "posts":   [{"label": "p1", "author": "alice", "body": "hi", "age": "2h", "audience": "d2"},
//	              {"label": "p2", "author": "bob", "body": "re", "parents": [{"post": "p1"}]}],
//	  "likes":   [{"user": "bob", "post": "p1"}]
//	}
type Fixture struct {
	Users   []FixtureUser `json:"users"`
	Friends []FixtureEdge `json:"friends"`
	Blocks  [][2]string   `json:"blocks"`
	Mutes   [][2]string   `json:"mutes"`
	Posts   []FixturePost `json:"posts"`
	Likes   []FixtureLike `json:"likes"`
}

// FixtureUser is one fixture user. Without invited_by the user is genesis.
type FixtureUser struct {
	Handle       string    `json:"handle"`
	Name         string    `json:"name"`
	InvitedBy    string    `json:"invited_by"`
	Role         string    `json:"role"`   // member (default) | moderator | admin
	Status       string    `json:"status"` // active (default) | suspended | deleted
	Age          *Duration `json:"age"`    // default 30 days
	Probation    *Duration `json:"probation"`
	NameInChains bool      `json:"name_in_chains"`
	Suggestible  *bool     `json:"suggestible"`    // default true
	NamedInLikes *bool     `json:"named_in_likes"` // default true
	Slippery     bool      `json:"slippery_default"`
	Preset       string    `json:"preset"` // latest | balanced (default) | top
	Locale       string    `json:"locale"` // default en
}

// FixtureEdge is one friendship.
type FixtureEdge struct {
	A     string    `json:"a"`
	B     string    `json:"b"`
	Level string    `json:"level"` // friend (default) | friend_plus
	Age   *Duration `json:"age"`   // default 30 days
}

// FixturePost is one post. Without age, posts are one minute apart in list
// order, the last one a minute old.
type FixturePost struct {
	Label          string          `json:"label"`
	Author         string          `json:"author"`
	Body           string          `json:"body"`
	Audience       string          `json:"audience"` // connected (default) | d1 | d2 | d3 | friend_plus
	AudienceForced bool            `json:"audience_forced"`
	RefPolicy      string          `json:"ref_policy"` // anyone (default) | two_hops | friends | nobody
	Lang           string          `json:"lang"`       // default en
	Age            *Duration       `json:"age"`
	Deleted        bool            `json:"deleted"`
	Parents        []FixtureParent `json:"parents"`
}

// FixtureParent is one parent link of a post.
type FixtureParent struct {
	Post     string `json:"post"`
	Kind     string `json:"kind"` // ref (default) | cont
	Detached bool   `json:"detached"`
	Shadow   bool   `json:"shadow"`
}

// FixtureLike is one like.
type FixtureLike struct {
	User string    `json:"user"`
	Post string    `json:"post"`
	Age  *Duration `json:"age"` // default: one second after the post
}

// Duration is a JSON Go duration string ("90m", "48h").
type Duration time.Duration

// UnmarshalJSON parses a duration string; negative ages are rejected.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("devseed: duration must be a string like \"48h\": %w", err)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("devseed: %w", err)
	}
	if v < 0 {
		return fmt.Errorf("devseed: negative duration %q", s)
	}
	*d = Duration(v)
	return nil
}

// ParseFixture decodes a fixture strictly (unknown fields are errors).
func ParseFixture(data []byte) (*Fixture, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var f Fixture
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("devseed: fixture: %w", err)
	}
	if dec.More() {
		return nil, errors.New("devseed: fixture: trailing data")
	}
	return &f, nil
}

// FirstFixtureID is where explicit fixture user IDs start (plan §3.1; the same
// high range coretest.World uses).
const FirstFixtureID core.UserID = 3_000_000_000

// fixtureSlots × slotSize stays below 2^32 - FirstFixtureID.
const (
	fixtureSlots = 12_000
	slotSize     = 100_000
)

// fixtureBase is the first user ID of the named fixture: IDs depend only on the
// fixture's name and the user's position in it, never on what else exists.
func fixtureBase(name string) core.UserID {
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	return FirstFixtureID + core.UserID(h.Sum32()%fixtureSlots)*slotSize
}

var handleRE = regexp.MustCompile(`^[A-Za-z0-9_]{3,20}$`)

const defaultAge = 30 * 24 * time.Hour

func age(d *Duration, def time.Duration) time.Duration {
	if d == nil {
		return def
	}
	return time.Duration(*d)
}

// build resolves the fixture against d into a batch.
func (f *Fixture) build(ctx context.Context, d *core.Deps, name string) (*batch, error) {
	if len(f.Users) > slotSize {
		return nil, fmt.Errorf("devseed: fixture has %d users (max %d)", len(f.Users), slotSize)
	}
	now := d.Clock.Now()
	b := &batch{}
	users := map[string]core.UserID{}
	base := fixtureBase(name)
	for i, u := range f.Users {
		if !handleRE.MatchString(u.Handle) {
			return nil, fmt.Errorf("devseed: invalid handle %q", u.Handle)
		}
		canon := strings.ToLower(u.Handle)
		if _, dup := users[canon]; dup {
			return nil, fmt.Errorf("devseed: duplicate user %q", u.Handle)
		}
		users[canon] = base + core.UserID(i)
	}
	user := func(h string) (core.UserID, error) {
		canon := strings.ToLower(strings.TrimPrefix(h, "@"))
		if id, ok := users[canon]; ok {
			return id, nil
		}
		if id, ok := d.Users.ByHandle(ctx, canon); ok {
			return id, nil
		}
		return 0, fmt.Errorf("devseed: unknown user %q", h)
	}
	for i, u := range f.Users {
		row, err := fixtureUser(u, base+core.UserID(i), now, user)
		if err != nil {
			return nil, err
		}
		b.users = append(b.users, row)
	}
	pairs := map[[2]core.UserID]bool{}
	for _, e := range f.Friends {
		a, err := user(e.A)
		if err != nil {
			return nil, err
		}
		c, err := user(e.B)
		if err != nil {
			return nil, err
		}
		key := [2]core.UserID{min(a, c), max(a, c)}
		if a == c || pairs[key] {
			return nil, fmt.Errorf("devseed: invalid or duplicate friendship %s–%s", e.A, e.B)
		}
		pairs[key] = true
		lvl := core.LevelFriend
		switch e.Level {
		case "", "friend":
		case "friend_plus":
			lvl = core.LevelFriendPlus
		default:
			return nil, fmt.Errorf("devseed: unknown level %q", e.Level)
		}
		b.edges = append(b.edges, edgeRow{a: a, b: c, level: lvl, since: now.Add(-age(e.Age, defaultAge))})
	}
	for _, t := range []struct {
		in  [][2]string
		out *[][2]core.UserID
	}{{f.Blocks, &b.blocks}, {f.Mutes, &b.mutes}} {
		for _, p := range t.in {
			a, err := user(p[0])
			if err != nil {
				return nil, err
			}
			c, err := user(p[1])
			if err != nil {
				return nil, err
			}
			if a == c {
				return nil, fmt.Errorf("devseed: %s cannot block or mute themself", p[0])
			}
			if t.out == &b.blocks && pairs[[2]core.UserID{min(a, c), max(a, c)}] {
				// A block always unfriends (P0-W03): a pair is never both.
				return nil, fmt.Errorf("devseed: %s and %s are friends and cannot also be blocked", p[0], p[1])
			}
			*t.out = append(*t.out, [2]core.UserID{a, c})
		}
	}
	if err := f.buildPosts(ctx, d, b, now, user); err != nil {
		return nil, err
	}
	return b, nil
}

func fixtureUser(u FixtureUser, id core.UserID, now time.Time, user func(string) (core.UserID, error)) (userRow, error) {
	m := core.UserMeta{ID: id, ExtID: ids.Named("user:" + strings.ToLower(u.Handle)), Handle: u.Handle,
		DisplayName: u.Name, Genesis: u.InvitedBy == "", Status: core.StatusActive, CreatedAt: now.Add(-age(u.Age, defaultAge)),
		NameInChains: u.NameInChains, Suggestible: true, NamedInLikes: true, SlipperyDefault: u.Slippery,
		Preset: core.PresetBalanced, Locale: "en"}
	if len([]rune(u.Name)) > 50 {
		return userRow{}, fmt.Errorf("devseed: display name of %q is longer than 50", u.Handle)
	}
	if u.InvitedBy != "" {
		inv, err := user(u.InvitedBy)
		if err != nil {
			return userRow{}, err
		}
		if inv == id {
			return userRow{}, fmt.Errorf("devseed: %q cannot invite themself", u.Handle)
		}
		m.InvitedBy = inv
	}
	switch u.Role {
	case "", "member":
	case "moderator":
		m.Role = core.RoleModerator
	case "admin":
		m.Role = core.RoleAdmin
	default:
		return userRow{}, fmt.Errorf("devseed: unknown role %q", u.Role)
	}
	switch u.Status {
	case "", "active":
	case "suspended":
		m.Status = core.StatusSuspended
	case "deleted":
		m.Status = core.StatusDeleted
	default:
		return userRow{}, fmt.Errorf("devseed: unknown status %q", u.Status)
	}
	if u.Probation != nil {
		if m.Genesis {
			return userRow{}, fmt.Errorf("devseed: genesis user %q cannot be on probation", u.Handle)
		}
		until := now.Add(time.Duration(*u.Probation))
		m.ProbationUntil = &until
	}
	if u.Suggestible != nil {
		m.Suggestible = *u.Suggestible
	}
	if u.NamedInLikes != nil {
		m.NamedInLikes = *u.NamedInLikes
	}
	if u.Preset != "" {
		p, ok := core.ParsePreset(u.Preset)
		if !ok {
			return userRow{}, fmt.Errorf("devseed: unknown preset %q", u.Preset)
		}
		m.Preset = p
	}
	if u.Locale != "" {
		m.Locale = u.Locale
	}
	m.Languages = []string{m.Locale}
	return userRow{meta: m, webauth: placeholderHash("webauthn:" + m.ExtID.String())}, nil
}

func parseAudience(s string) (core.Audience, error) {
	for _, a := range []core.Audience{core.AudConnected, core.AudD1, core.AudD2, core.AudD3, core.AudFriendPlus} {
		if s == a.String() || (s == "" && a == core.AudConnected) {
			return a, nil
		}
	}
	return 0, fmt.Errorf("devseed: unknown audience %q", s)
}

func parseRefPolicy(s string) (core.RefPolicy, error) {
	for _, p := range []core.RefPolicy{core.PolAnyone, core.PolTwoHops, core.PolFriends, core.PolNobody} {
		if s == p.String() || (s == "" && p == core.PolAnyone) {
			return p, nil
		}
	}
	return 0, fmt.Errorf("devseed: unknown ref policy %q", s)
}

func (f *Fixture) buildPosts(ctx context.Context, d *core.Deps, b *batch, now time.Time,
	user func(string) (core.UserID, error)) error {
	idx := map[string]int{} // label → index in b.posts
	for i, p := range f.Posts {
		if p.Label == "" {
			return errors.New("devseed: post without label")
		}
		if _, dup := idx[p.Label]; dup {
			return fmt.Errorf("devseed: duplicate post label %q", p.Label)
		}
		author, err := user(p.Author)
		if err != nil {
			return err
		}
		m := core.PostMeta{ID: ids.Named("post:" + p.Label), Author: author, Version: 1, Lang: "en", Deleted: p.Deleted,
			AudienceForced: p.AudienceForced, CreatedAt: now.Add(-age(p.Age, time.Duration(len(f.Posts)-i)*time.Minute))}
		if p.Lang != "" {
			m.Lang = p.Lang
		}
		if m.Audience, err = parseAudience(p.Audience); err != nil {
			return err
		}
		if m.RefPolicy, err = parseRefPolicy(p.RefPolicy); err != nil {
			return err
		}
		if len([]rune(p.Body)) > 20000 {
			return fmt.Errorf("devseed: post %q is too long", p.Label)
		}
		if len(p.Parents) > 3 {
			return fmt.Errorf("devseed: post %q has more than 3 parents", p.Label)
		}
		seen := map[core.PostID]bool{}
		for _, pp := range p.Parents {
			link, err := f.parentLink(ctx, d, b, idx, pp, m)
			if err != nil {
				return fmt.Errorf("devseed: post %q: %w", p.Label, err)
			}
			if seen[link.Parent] {
				return fmt.Errorf("devseed: post %q: duplicate parent %q", p.Label, pp.Post)
			}
			seen[link.Parent] = true
			if link.Kind == core.LinkCont {
				if m.ContParent != [16]byte{} {
					return fmt.Errorf("devseed: post %q has two cont parents", p.Label)
				}
				m.ContParent = link.Parent
			} else {
				m.IsReply = true
			}
			b.links = append(b.links, link)
		}
		idx[p.Label] = len(b.posts)
		b.posts = append(b.posts, postRow{meta: m, body: p.Body})
	}
	for _, l := range f.Likes {
		u, err := user(l.User)
		if err != nil {
			return err
		}
		i, ok := idx[l.Post]
		if !ok {
			return fmt.Errorf("devseed: like of unknown post %q", l.Post)
		}
		at := b.posts[i].meta.CreatedAt.Add(time.Second)
		if l.Age != nil {
			at = now.Add(-time.Duration(*l.Age))
		}
		b.likes = append(b.likes, likeRow{user: u, post: b.posts[i].meta.ID, at: at})
	}
	return nil
}

// parentLink resolves one parent of child (a post earlier in the fixture, or an
// existing post by label-derived ID) and records a cont link on the parent.
func (f *Fixture) parentLink(ctx context.Context, d *core.Deps, b *batch, idx map[string]int, pp FixtureParent,
	child core.PostMeta) (core.LinkMeta, error) {
	kind := core.LinkRef
	switch pp.Kind {
	case "", "ref":
	case "cont":
		kind = core.LinkCont
	default:
		return core.LinkMeta{}, fmt.Errorf("unknown link kind %q", pp.Kind)
	}
	var parent *core.PostMeta
	if i, ok := idx[pp.Post]; ok {
		parent = &b.posts[i].meta
	} else if m, ok := d.Posts.Meta(ctx, ids.Named("post:"+pp.Post)); ok {
		parent = &m
	} else {
		return core.LinkMeta{}, fmt.Errorf("unknown parent %q (parents must come first)", pp.Post)
	}
	if kind == core.LinkCont {
		if parent.Author != child.Author {
			return core.LinkMeta{}, fmt.Errorf("cont parent %q has another author", pp.Post)
		}
		if parent.ContChild != [16]byte{} {
			return core.LinkMeta{}, fmt.Errorf("parent %q already has a continuation", pp.Post)
		}
		parent.ContChild = child.ID
		if _, inFixture := idx[pp.Post]; !inFixture {
			b.touched = append(b.touched, *parent)
		}
	}
	return core.LinkMeta{Child: child.ID, Parent: parent.ID, Kind: kind, ChildAuthor: child.Author,
		ParentAuthor: parent.Author, Detached: pp.Detached, Shadow: pp.Shadow, CreatedAt: child.CreatedAt}, nil
}
