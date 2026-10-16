package conformance_test

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/RoaringBitmap/roaring/v2"
	"pgregory.net/rapid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/devseed"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/graph"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coretest"
)

// The world model: what the generator draws, what devseed loads (as fixture
// documents, plan §7 "existing synthetic data generators") and what the
// horizon oracle reads. Users are named by handle and posts by label; IDs come
// from the devseed fixture slots, so a base user has the same ID in every world.

// Fixture names select the ID slots of the base world and of the extras.
const (
	baseFixture  = "conformance-base"
	extraFixture = "conformance-extra"
)

// now is the clock of every world (coretest.Epoch, the wiretest clock).
var now = coretest.Epoch

type mUser struct {
	Handle        string
	Inviter       string // "" = genesis
	AgeDays       int
	Probation     bool
	NameInChains  bool
	Unsuggestible bool
	Unnamed       bool // named_in_likes off
	slot          string
	index         int
}

type mEdge struct {
	A, B    string
	AgeDays int
}

type mParent struct {
	Post     string
	Detached bool
	Shadow   bool
}

type mPost struct {
	Label   string
	Author  string
	Aud     core.Audience
	Pol     core.RefPolicy
	Forced  bool
	AgeMin  int
	Deleted bool
	Parents []mParent
	svc     bool // created through the posts service (not in a fixture)
}

type mLike struct{ User, Post string }

// model is a world: base facts plus extras (outside H(V)).
type model struct {
	Users  []mUser
	Edges  []mEdge
	Blocks [][2]string // blocker, blocked
	Posts  []mPost
	Likes  []mLike
}

func (m *model) clone() *model {
	c := &model{Users: slices.Clone(m.Users), Edges: slices.Clone(m.Edges), Blocks: slices.Clone(m.Blocks),
		Posts: make([]mPost, len(m.Posts)), Likes: slices.Clone(m.Likes)}
	for i, p := range m.Posts {
		p.Parents = slices.Clone(p.Parents)
		c.Posts[i] = p
	}
	return c
}

func (m *model) user(h string) (mUser, bool) {
	for _, u := range m.Users {
		if u.Handle == h {
			return u, true
		}
	}
	return mUser{}, false
}

func (m *model) post(label string) (mPost, bool) {
	for _, p := range m.Posts {
		if p.Label == label {
			return p, true
		}
	}
	return mPost{}, false
}

func (m *model) friends(a, b string) bool {
	for _, e := range m.Edges {
		if (e.A == a && e.B == b) || (e.A == b && e.B == a) {
			return true
		}
	}
	return false
}

func (m *model) blocks(a, b string) bool {
	for _, x := range m.Blocks {
		if x[0] == a && x[1] == b {
			return true
		}
	}
	return false
}

func (m *model) liked(u, p string) bool {
	for _, l := range m.Likes {
		if l.User == u && l.Post == p {
			return true
		}
	}
	return false
}

// slotBase mirrors devseed's fixture slots: IDs depend only on the fixture
// name and the user's position in it.
func slotBase(name string) core.UserID {
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	return devseed.FirstFixtureID + core.UserID(h.Sum32()%12_000)*100_000
}

func (u mUser) id() core.UserID { return slotBase(u.slot) + core.UserID(u.index) }

func (m *model) id(h string) core.UserID {
	u, ok := m.user(h)
	if !ok {
		panic("conformance: unknown user " + h)
	}
	return u.id()
}

func body(label string) string { return "txt_" + label + "_end" }

func displayName(h string) string { return "N_" + h + "_n" }

// dur renders a fixture age ("90m", "48h").
func dur(d time.Duration) string { return d.String() }

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// layer is one fixture load: the facts added on top of what is loaded already.
type layer struct {
	name   string
	users  []mUser
	edges  []mEdge
	blocks [][2]string
	posts  []mPost
	likes  []mLike
}

func (l *layer) hasPost(label string) bool {
	for _, p := range l.posts {
		if p.Label == label {
			return true
		}
	}
	return false
}

func (l *layer) json() []byte {
	type obj = map[string]any
	users, friends, ps, likes := []obj{}, []obj{}, []obj{}, []obj{}
	for _, u := range l.users {
		fu := obj{"handle": u.Handle, "name": displayName(u.Handle), "age": dur(time.Duration(u.AgeDays) * 24 * time.Hour),
			"name_in_chains": u.NameInChains, "suggestible": !u.Unsuggestible, "named_in_likes": !u.Unnamed}
		if u.Inviter != "" {
			fu["invited_by"] = u.Inviter
		}
		if u.Probation {
			fu["probation"] = dur(30 * 24 * time.Hour)
		}
		users = append(users, fu)
	}
	for _, e := range l.edges {
		friends = append(friends, obj{"a": e.A, "b": e.B, "age": dur(time.Duration(e.AgeDays) * 24 * time.Hour)})
	}
	for _, p := range l.posts {
		parents := []obj{}
		for _, pp := range p.Parents {
			parents = append(parents, obj{"post": pp.Post, "detached": pp.Detached, "shadow": pp.Shadow})
		}
		ps = append(ps, obj{"label": p.Label, "author": p.Author, "body": body(p.Label), "audience": p.Aud.String(),
			"audience_forced": p.Forced, "ref_policy": p.Pol.String(), "age": dur(time.Duration(p.AgeMin) * time.Minute),
			"deleted": p.Deleted, "parents": parents})
	}
	for _, lk := range l.likes {
		if l.hasPost(lk.Post) {
			likes = append(likes, obj{"user": lk.User, "post": lk.Post})
		}
	}
	blocks := l.blocks
	if blocks == nil {
		blocks = [][2]string{}
	}
	return mustJSON(obj{"users": users, "friends": friends, "blocks": blocks, "posts": ps, "likes": likes})
}

// ---- the horizon oracle ----

// adjacency is the model's graph with the fixture IDs and edge ages.
func (m *model) adjacency() graph.Adjacency {
	adj := graph.Adjacency{}
	for _, e := range m.Edges {
		a, b := m.id(e.A), m.id(e.B)
		since := now.Add(-time.Duration(e.AgeDays) * 24 * time.Hour)
		adj[a] = append(adj[a], core.Edge{To: b, Level: core.LevelFriend, Since: since})
		adj[b] = append(adj[b], core.Edge{To: a, Level: core.LevelFriend, Since: since})
	}
	return adj
}

// oracle is the independent reference view of V's world (plan §7): reference
// BFS and first hops over the model, and the §4 / §10.1.3 rules written
// directly over model facts.
type oracle struct {
	m     *model
	v     string
	vid   core.UserID
	adj   graph.Adjacency
	dist  map[core.UserID]int
	first map[core.UserID]core.UserID
	byID  map[core.UserID]mUser
}

func newOracle(m *model, v string) *oracle {
	o := &oracle{m: m, v: v, vid: m.id(v), adj: m.adjacency(), byID: map[core.UserID]mUser{}}
	ex := core.Exclusions{Blocked: roaring.New(), HiddenVia: roaring.New()}
	for _, b := range m.Blocks {
		if b[0] == v {
			ex.Blocked.Add(uint32(m.id(b[1])))
		}
	}
	o.dist = graph.ReferenceBFS(o.adj, o.vid, ex)
	o.first = graph.ReferenceFirstHop(o.adj, o.vid, ex)
	for _, u := range m.Users {
		o.byID[u.id()] = u
	}
	return o
}

func (o *oracle) distOf(h string) (int, bool) {
	d, ok := o.dist[o.m.id(h)]
	return d, ok
}

func effAudience(p mPost) core.Audience {
	if p.Forced && (p.Aud == core.AudConnected || p.Aud == core.AudD2 || p.Aud == core.AudD3) {
		return core.AudD1
	}
	return p.Aud
}

// visible is §4 rules 1, 4, 5: not deleted, author reachable, audience allows.
func (o *oracle) visible(p mPost) bool {
	if p.Deleted {
		return false
	}
	d, ok := o.distOf(p.Author)
	if !ok {
		return false
	}
	switch effAudience(p) {
	case core.AudConnected:
		return true
	case core.AudD1:
		return d <= 1
	case core.AudD2:
		return d <= 2
	case core.AudD3:
		return d <= 3
	}
	return false
}

// linkShown is §10.1.3 for a link child → parent (both posts in the model).
func (o *oracle) linkShown(child mPost, pp mParent) bool {
	parent, ok := o.m.post(pp.Post)
	if !ok || !o.visible(child) || !o.visible(parent) || pp.Detached {
		return false
	}
	if pp.Shadow && child.Author != o.v {
		return false
	}
	open := parent.Aud == core.AudConnected && !parent.Forced && parent.Pol == core.PolAnyone
	if open || child.Author == o.v || child.Author == parent.Author {
		return true
	}
	d, ok := o.distOf(child.Author)
	return ok && d == 1
}

// horizon renders every fact §2.2 puts inside H(V) (plus the inputs plan §7
// lists as held fixed) as a canonical string: two worlds with equal horizons
// must give V identical responses.
func (o *oracle) horizon() string {
	var lines []string
	add := func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }
	m := o.m
	userLine := func(u mUser) string {
		inv := "genesis"
		if u.Inviter != "" {
			inv = "user:" + u.Inviter
		}
		return fmt.Sprintf("%s inv=%s age=%d prob=%t chains=%t nosugg=%t unnamed=%t", u.Handle, inv, u.AgeDays,
			u.Probation, u.NameInChains, u.Unsuggestible, u.Unnamed)
	}
	// (2) users V can see: existence, distance, first hop, label and settings.
	for id, d := range o.dist {
		u := o.byID[id]
		fh := ""
		if f := o.first[id]; f != 0 {
			fh = o.byID[f].Handle
		}
		add("U %s d=%d fh=%s", userLine(u), d, fh)
	}
	// (1)+(3) V's edges and every edge on some shortest path (layer-crossing
	// edges): they decide first hops, named middles and mutual counts.
	for _, e := range m.Edges {
		da, oka := o.distOf(e.A)
		db, okb := o.distOf(e.B)
		if oka && okb && (da-db == 1 || db-da == 1) {
			a, b := min(e.A, e.B), max(e.A, e.B)
			add("E %s %s age=%d", a, b, e.AgeDays)
		}
	}
	// (1) V's own blocks and invite relations.
	for _, b := range m.Blocks {
		if b[0] == o.v {
			add("B %s", b[1])
		}
	}
	for _, u := range m.Users {
		if u.Inviter == o.v {
			add("I %s", userLine(u))
		}
	}
	// Posts V can see, and (7) the links §10.1.3 shows.
	for _, p := range m.Posts {
		if !o.visible(p) {
			continue
		}
		add("P %s by=%s aud=%d pol=%d forced=%t age=%d svc=%t", p.Label, p.Author, p.Aud, p.Pol, p.Forced, p.AgeMin, p.svc)
		for i, pp := range p.Parents {
			if o.linkShown(p, pp) {
				add("L %s -> %s ord=%d shadow=%t", p.Label, pp.Post, i, pp.Shadow)
			}
		}
	}
	// (6) likes on visible posts from within two hops (and V's own likes).
	for _, l := range m.Likes {
		p, ok := m.post(l.Post)
		if !ok {
			continue
		}
		if l.User == o.v {
			add("K %s %s", l.Post, l.User)
			continue
		}
		if d, ok := o.distOf(l.User); ok && d <= 2 && o.visible(p) {
			add("K %s %s", l.Post, l.User)
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// vAction is one of V's own graph changes during the replay.
type vAction struct{ Kind, Who string } // Kind: block | unfriend

// horizons is V's horizon in m and after each of V's own graph changes
// (applied in order, the way the friends service does: only a person V can
// see is blocked and the block unfriends; only a friend is unfriended). V's
// own changes move people between layers, so a mutation must keep H(V) fixed
// in every state V will be in, not just the first one.
func horizons(m *model, v string, acts []vAction) string {
	o := newOracle(m, v)
	hs := []string{o.horizon()}
	cur := m
	for _, a := range acts {
		next := cur.clone()
		switch a.Kind {
		case "block":
			if _, ok := o.distOf(a.Who); !ok || a.Who == v || cur.blocks(v, a.Who) {
				continue
			}
			next.Blocks = append(next.Blocks, [2]string{v, a.Who})
		case "unfriend":
			if !cur.friends(v, a.Who) {
				continue
			}
		}
		next.Edges = slices.DeleteFunc(next.Edges, func(e mEdge) bool {
			return (e.A == v && e.B == a.Who) || (e.A == a.Who && e.B == v)
		})
		cur = next
		o = newOracle(cur, v)
		hs = append(hs, o.horizon())
	}
	return strings.Join(hs, "\n====\n")
}

// ---- generators ----

// genBase draws component A (V = "va00" and its graph), a separate component B,
// a zero-edge bot, posts, likes and blocks.
func genBase(t *rapid.T) *model {
	m := &model{}
	nA := rapid.IntRange(3, 10).Draw(t, "nA")
	nB := rapid.IntRange(1, 4).Draw(t, "nB")
	var hs []string
	for i := range nA {
		hs = append(hs, fmt.Sprintf("va%02d", i))
	}
	for i := range nB {
		hs = append(hs, fmt.Sprintf("vb%02d", i))
	}
	hs = append(hs, "vbot")
	for i, h := range hs {
		u := mUser{Handle: h, AgeDays: rapid.SampledFrom([]int{30, 90, 200}).Draw(t, "age"),
			NameInChains: rapid.Bool().Draw(t, "chains"), Unsuggestible: rapid.IntRange(0, 4).Draw(t, "sugg") == 0,
			Unnamed: rapid.IntRange(0, 4).Draw(t, "named") == 0, slot: baseFixture, index: i}
		if h == "vbot" {
			u.AgeDays = 1
		}
		m.Users = append(m.Users, u)
	}
	// Inviters may cross components (an inviter label is an accepted disclosure).
	for i := range m.Users {
		if m.Users[i].Handle == "vbot" || rapid.IntRange(0, 3).Draw(t, "genesis") == 0 {
			continue
		}
		j := rapid.IntRange(0, len(m.Users)-2).Draw(t, "inviter")
		if j >= i {
			j++
		}
		if m.Users[j].Handle == "vbot" {
			continue
		}
		m.Users[i].Inviter = m.Users[j].Handle
		m.Users[i].Probation = rapid.IntRange(0, 4).Draw(t, "probation") == 0
	}
	edgeAge := func() int { return rapid.SampledFrom([]int{3, 60, 120, 365}).Draw(t, "edgeAge") }
	// A: a random tree plus extra edges; B likewise.
	for _, grp := range [][]string{hs[:nA], hs[nA : nA+nB]} {
		for i := 1; i < len(grp); i++ {
			j := rapid.IntRange(0, i-1).Draw(t, "parent")
			m.Edges = append(m.Edges, mEdge{A: grp[j], B: grp[i], AgeDays: edgeAge()})
		}
		extra := rapid.IntRange(0, len(grp)).Draw(t, "extraEdges")
		for range extra {
			a := rapid.SampledFrom(grp).Draw(t, "ea")
			b := rapid.SampledFrom(grp).Draw(t, "eb")
			if a != b && !m.friends(a, b) {
				m.Edges = append(m.Edges, mEdge{A: a, B: b, AgeDays: edgeAge()})
			}
		}
	}
	// V's blocks (never a friend: a block always unfriends) and others' blocks.
	nBlocks := rapid.IntRange(0, 3).Draw(t, "nBlocks")
	for range nBlocks {
		a := rapid.SampledFrom(hs).Draw(t, "blocker")
		if rapid.IntRange(0, 1).Draw(t, "vBlocks") == 0 {
			a = "va00"
		}
		b := rapid.SampledFrom(hs).Draw(t, "blocked")
		if a != b && !m.friends(a, b) && !m.blocks(a, b) {
			m.Blocks = append(m.Blocks, [2]string{a, b})
		}
	}
	// Posts, oldest first so parents precede children.
	nPosts := rapid.IntRange(2, 3*len(hs)).Draw(t, "nPosts")
	ages := make([]int, nPosts)
	for i := range ages {
		ages[i] = rapid.IntRange(1, 6*24*60).Draw(t, "postAge")
	}
	sort.Sort(sort.Reverse(sort.IntSlice(ages)))
	for i, age := range ages {
		p := genPost(t, m, fmt.Sprintf("p%02d", i), rapid.SampledFrom(hs).Draw(t, "author"), age)
		m.Posts = append(m.Posts, p)
	}
	for range 2 * nPosts {
		u := rapid.SampledFrom(hs).Draw(t, "liker")
		p := m.Posts[rapid.IntRange(0, len(m.Posts)-1).Draw(t, "liked")].Label
		if !m.liked(u, p) {
			m.Likes = append(m.Likes, mLike{User: u, Post: p})
		}
	}
	return m
}

// genPost draws a post by author with up to two parents among m's posts (all
// older: callers add posts oldest first).
func genPost(t *rapid.T, m *model, label, author string, ageMin int) mPost {
	return genPostReplying(t, m, label, author, ageMin, 33)
}

// genPostReplying is genPost replying with probability replyPct %.
func genPostReplying(t *rapid.T, m *model, label, author string, ageMin, replyPct int) mPost {
	p := mPost{Label: label, Author: author, AgeMin: ageMin,
		Aud: rapid.SampledFrom([]core.Audience{core.AudConnected, core.AudConnected, core.AudConnected, core.AudD1,
			core.AudD2, core.AudD3}).Draw(t, "aud"),
		Pol: rapid.SampledFrom([]core.RefPolicy{core.PolAnyone, core.PolAnyone, core.PolAnyone, core.PolTwoHops,
			core.PolFriends, core.PolNobody}).Draw(t, "pol"),
		Deleted: rapid.IntRange(0, 19).Draw(t, "deleted") == 0}
	if u, ok := m.user(author); ok && u.Probation {
		p.Forced = true
		if p.Aud == core.AudConnected || p.Aud == core.AudD2 || p.Aud == core.AudD3 {
			p.Aud = core.AudD1 // a forced post is stored as d1 (D6)
		}
	}
	var older []mPost
	for _, q := range m.Posts {
		if q.AgeMin > ageMin && !q.svc {
			older = append(older, q)
		}
	}
	if len(older) > 0 && rapid.IntRange(0, 99).Draw(t, "reply") < replyPct {
		n := rapid.IntRange(1, min(2, len(older))).Draw(t, "nParents")
		used := map[string]bool{}
		for range n {
			q := rapid.SampledFrom(older).Draw(t, "parent")
			if used[q.Label] {
				continue
			}
			used[q.Label] = true
			p.Parents = append(p.Parents, mParent{Post: q.Label, Detached: rapid.IntRange(0, 9).Draw(t, "detached") == 0,
				Shadow: m.blocks(q.Author, author)})
		}
	}
	return p
}

// genExtras draws mutations of base that leave H(V) unchanged: every proposed
// fact is kept only if the oracle's horizon is still the base one. Users and
// posts get the prefix (distinct per world, so the two worlds' extras never
// collide by name).
func genExtras(t *rapid.T, base *model, v, prefix string, vActs []vAction) (*model, *layer) {
	want := horizons(base, v, vActs)
	m := base.clone()
	l := &layer{name: extraFixture}
	keep := func(try *model) bool { return horizons(try, v, vActs) == want }
	handles := func() []string {
		var hs []string
		for _, u := range m.Users {
			hs = append(hs, u.Handle)
		}
		return hs
	}
	n := rapid.IntRange(4, 30).Draw(t, prefix+"n")
	nUsers, nPosts := 0, 0
	for range n {
		try := m.clone()
		switch rapid.IntRange(0, 4).Draw(t, prefix+"kind") {
		case 0: // a new user, maybe with edges and an inviter anywhere
			h := fmt.Sprintf("%su%02d", prefix, nUsers)
			u := mUser{Handle: h, AgeDays: rapid.SampledFrom([]int{5, 90}).Draw(t, "age"), slot: extraFixture,
				index: len(l.users), NameInChains: rapid.Bool().Draw(t, "chains")}
			if rapid.Bool().Draw(t, "invited") {
				u.Inviter = rapid.SampledFrom(handles()).Draw(t, "inviter")
				u.Probation = rapid.Bool().Draw(t, "probation")
			}
			try.Users = append(try.Users, u)
			var es []mEdge
			for range rapid.IntRange(0, 2).Draw(t, "nEdges") {
				b := rapid.SampledFrom(handles()).Draw(t, "to")
				if !try.friends(h, b) && !try.blocks(h, b) && !try.blocks(b, h) {
					e := mEdge{A: h, B: b, AgeDays: 60}
					try.Edges = append(try.Edges, e)
					es = append(es, e)
				}
			}
			if keep(try) {
				m = try
				nUsers++
				l.users = append(l.users, u)
				l.edges = append(l.edges, es...)
			}
		case 1: // a new edge between existing users
			a := rapid.SampledFrom(handles()).Draw(t, "a")
			b := rapid.SampledFrom(handles()).Draw(t, "b")
			if a == b || try.friends(a, b) || try.blocks(a, b) || try.blocks(b, a) {
				continue
			}
			e := mEdge{A: a, B: b, AgeDays: rapid.SampledFrom([]int{3, 60}).Draw(t, "age")}
			try.Edges = append(try.Edges, e)
			if keep(try) {
				m = try
				l.edges = append(l.edges, e)
			}
		case 2: // a new block (never by V; possibly OF V)
			a := rapid.SampledFrom(handles()).Draw(t, "blocker")
			b := rapid.SampledFrom(handles()).Draw(t, "blocked")
			if a == v || a == b || try.friends(a, b) || try.blocks(a, b) {
				continue
			}
			try.Blocks = append(try.Blocks, [2]string{a, b})
			if keep(try) {
				m = try
				l.blocks = append(l.blocks, [2]string{a, b})
			}
		case 3: // a new post (often a child of a base post) by anyone
			author := rapid.SampledFrom(handles()).Draw(t, "author")
			if author == v {
				continue // V's own posts, even deleted ones, are V's own history (e.g. ref_cap)
			}
			p := genPostReplying(t, try, fmt.Sprintf("%sp%02d", prefix, nPosts), author,
				rapid.IntRange(1, 600).Draw(t, "age"), 80)
			try.Posts = append(try.Posts, p)
			if keep(try) {
				m = try
				nPosts++
				l.posts = append(l.posts, p)
			}
		case 4: // a new like by anyone
			u := rapid.SampledFrom(handles()).Draw(t, "liker")
			p := try.Posts[rapid.IntRange(0, len(try.Posts)-1).Draw(t, "post")].Label
			if try.liked(u, p) {
				continue
			}
			try.Likes = append(try.Likes, mLike{User: u, Post: p})
			if keep(try) {
				m = try
				l.likes = append(l.likes, mLike{User: u, Post: p})
			}
		}
	}
	return m, l
}

// baseLayer is the whole base model as one fixture load.
func baseLayer(m *model) *layer {
	return &layer{name: baseFixture, users: m.Users, edges: m.Edges, blocks: m.Blocks, posts: m.Posts, likes: m.Likes}
}
