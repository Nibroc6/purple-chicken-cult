package tree_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coreref"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coretest"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/posts"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/tree"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire/wiretest"
)

type env struct {
	t   *testing.T
	a   *wire.App
	d   *core.Deps
	tr  *tree.Service
	w   *wiretest.TestWorld
	met *coreref.MemMetrics
	ctx context.Context
}

// newEnv: alice – bob – carol – dave (a chain), fran a second friend of alice,
// xavier in another component. Every account is 90 days old.
func newEnv(t *testing.T) *env {
	t.Helper()
	a := wiretest.NewApp(t)
	d := a.Deps()
	met := &coreref.MemMetrics{}
	d.Metrics = met
	e := &env{t: t, a: a, d: d, tr: a.Tree, w: wiretest.World(t, a), met: met, ctx: context.Background()}
	for _, u := range []string{"alice", "bob", "carol", "dave", "fran", "xavier"} {
		e.w.User(u)
	}
	e.w.Friends("alice", "bob")
	e.w.Friends("alice", "fran")
	e.w.Friends("bob", "carol")
	e.w.Friends("carol", "dave")
	e.reloadUsers()
	e.advance(90 * 24 * time.Hour)
	return e
}

func (e *env) id(h string) core.UserID { return e.w.ID(h) }

func (e *env) as(h string) context.Context {
	return core.WithViewer(e.ctx, core.Viewer{ID: e.id(h), Kind: core.SessUser, AuthAt: e.d.Clock.Now()})
}

func (e *env) ref(viewer string, p core.PostID) gate.Ref { return e.d.Refs.Post(e.id(viewer), p) }

func (e *env) advance(d time.Duration) {
	e.t.Helper()
	if err := e.a.Hooks().AdvanceClock(d); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) reloadUsers() {
	e.t.Helper()
	if err := e.a.Users.Reload(e.ctx); err != nil {
		e.t.Fatal(err)
	}
}

// post publishes one minute after the previous post (distinct timestamps).
func (e *env) post(h, body string, parents ...core.PostID) core.PostID {
	e.t.Helper()
	return e.postDraft(h, posts.Draft{Body: body, Lang: "en"}, parents...)
}

func (e *env) postDraft(h string, d posts.Draft, parents ...core.PostID) core.PostID {
	e.t.Helper()
	for _, p := range parents {
		d.Parents = append(d.Parents, posts.ParentIn{Ref: e.ref(h, p), Kind: "ref"})
	}
	e.advance(time.Minute)
	pv, err := e.a.Posts.Create(e.as(h), d)
	if err != nil {
		e.t.Fatalf("create by %s: %v", h, err)
	}
	id, ok := e.d.Refs.OpenPost(e.id(h), pv.Ref)
	if !ok {
		e.t.Fatal("ref does not open")
	}
	return id
}

func (e *env) open(h string, focal core.PostID, preset string) gate.TreeView {
	e.t.Helper()
	tv, err := e.tr.Open(e.as(h), e.ref(h, focal), preset)
	if err != nil {
		e.t.Fatalf("open for %s: %v", h, err)
	}
	return tv
}

func (e *env) row(h string, tv gate.TreeView, gen int, center core.PostID) gate.TreeRow {
	e.t.Helper()
	r, err := e.tr.Row(e.as(h), tv.Snapshot, gen, e.ref(h, center))
	if err != nil {
		e.t.Fatalf("row %d for %s: %v", gen, h, err)
	}
	return r
}

// shape renders a tree as "gen:label[body body …]" rows; a "shown above"
// stub reads "^ref" with its author, a tier divider "|".
func shape(tv gate.TreeView) string {
	var out []string
	for _, r := range tv.Rows {
		out = append(out, rowShape(r))
	}
	return strings.Join(out, " ")
}

func rowShape(r gate.TreeRow) string {
	var items []string
	for _, it := range r.Items {
		s := it.Post.Body
		if it.ShownAbove {
			s = "^" + it.Post.Author.Handle
		}
		if it.TierDivider {
			s = "|" + s
		}
		items = append(items, s)
	}
	s := fmt.Sprintf("%d:%s[%s]", r.Gen, r.Label, strings.Join(items, ","))
	if r.More > 0 {
		s += fmt.Sprintf("+%d", r.More)
	}
	return s
}

// normalized is the JSON of v with the opaque per-open tokens (snapshot ids,
// cursors) replaced by their first-seen index (plan §7, buildability D1).
func normalized(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var x any
	if err := json.Unmarshal(b, &x); err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	var walk func(any) any
	walk = func(x any) any {
		switch v := x.(type) {
		case map[string]any:
			for k, c := range v {
				if s, ok := c.(string); ok && (k == "snapshot" || k == "cursor") {
					if _, ok := seen[s]; !ok {
						seen[s] = len(seen) + 1
					}
					v[k] = fmt.Sprintf("<%s#%d>", k, seen[s])
					continue
				}
				v[k] = walk(c)
			}
		case []any:
			for i := range v {
				v[i] = walk(v[i])
			}
		}
		return x
	}
	out, err := json.Marshal(walk(x))
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestLabels(t *testing.T) {
	want := map[int]string{-4: "generations_up", -3: "generations_up", -2: "earlier", -1: "replying_to", 0: "this_post",
		1: "replies", 2: "replies_to_replies", 3: "generations_down", 7: "generations_down"}
	for g, l := range want {
		if got := tree.Label(g); got != l {
			t.Errorf("Label(%d) = %q, want %q", g, got, l)
		}
	}
}

// The view opens with the focal post plus two rows above and two below: its
// parents, the siblings under the primary parent (focal first), its replies
// and the replies to the first reply; earlier and later rows are announced.
func TestOpenRowsAroundFocal(t *testing.T) {
	e := newEnv(t)
	top := e.post("carol", "top")
	g := e.post("bob", "grand", top)
	p2 := e.post("fran", "parent two")
	p1 := e.post("bob", "parent one", g)
	s1 := e.post("fran", "sibling", p1)
	f := e.post("alice", "focal", p1, p2)
	e.post("bob", "child one", f)
	c2 := e.post("fran", "child two", f)
	gc := e.post("fran", "grandchild", c2)
	e.post("bob", "great grandchild", gc)

	tv := e.open("alice", f, "latest")
	// Latest: newest first within a tier; the reader's own post is tier 0, so
	// the sibling (tier 1) starts a new tier inside the focal row.
	want := "-2:earlier[grand] -1:replying_to[parent one,parent two] 0:this_post[focal,|sibling] " +
		"1:replies[child two,child one] 2:replies_to_replies[grandchild]"
	if got := shape(tv); got != want {
		t.Fatalf("shape\n got %s\nwant %s", got, want)
	}
	if tv.Focal != e.ref("alice", f) || tv.Snapshot == "" || !tv.HasEarlier || !tv.HasLater {
		t.Fatalf("view %+v", tv)
	}
	// Centers: the row −1 lists the focal post's parents, the focal row the
	// children of the primary (best-ranked) parent, row −2 that parent's
	// parents, row +2 the children of row +1's first post.
	centers := []core.PostID{p1, f, p1, f, c2}
	for i, r := range tv.Rows {
		if r.Center != e.ref("alice", centers[i]) {
			t.Fatalf("row %d center", r.Gen)
		}
	}
	if tv.Rows[2].Items[1].Post.Ref != e.ref("alice", s1) {
		t.Fatalf("sibling row %s", rowShape(tv.Rows[2]))
	}
	// Bodies for every served post; the focal post's other parent is a chip.
	focal := tv.Rows[2].Items[0]
	if focal.Post.Body != "focal" || len(focal.AlsoParents) != 1 || focal.AlsoParents[0] != e.ref("alice", p2) ||
		focal.AlsoRepliesTo[0].Handle != "fran" || focal.AlsoRepliesTo[0].Ref != e.d.Refs.User(e.id("alice"), e.id("fran")) {
		t.Fatalf("focal item %+v", focal)
	}
	if e.met.Get(tree.MetricOpened, "") != 1 {
		t.Fatal("tree.opened not counted")
	}

	// A root post: the focal row alone, centered on itself, nothing earlier.
	root := e.open("alice", top, "latest")
	if got := shape(root); got != "0:this_post[top] 1:replies[grand] 2:replies_to_replies[parent one]" || root.HasEarlier || !root.HasLater {
		t.Fatalf("root %s", got)
	}
	if root.Rows[0].Center != e.ref("alice", top) {
		t.Fatal("a root's focal row is centered on itself")
	}
}

// Acceptance (tree noninterference): adding, detaching or deleting a child the
// reader cannot see — outside its audience, by someone the reader blocked, or
// on a post whose ref policy hides the link — leaves every answer identical.
func TestNoninterference(t *testing.T) {
	e := newEnv(t)
	e.w.User("erin")
	e.w.Friends("carol", "erin")
	e.w.Block("alice", "erin")
	e.reloadUsers()
	root := e.post("carol", "root")
	strict := e.postDraft("carol", posts.Draft{Body: "friends only", Lang: "en", RefPolicy: "friends"})
	bobReply := e.post("bob", "bob replies", root)
	e.post("alice", "alice replies", bobReply)

	snap := func() string {
		var parts []string
		for _, f := range []core.PostID{root, bobReply, strict} {
			tv := e.open("alice", f, "balanced")
			parts = append(parts, normalized(t, tv))
			if len(tv.Rows) > 0 {
				r := e.row("alice", tv, tv.Rows[len(tv.Rows)-1].Gen+1, mustOpen(t, e, tv.Rows[len(tv.Rows)-1].Items[0].Post.Ref))
				parts = append(parts, normalized(t, r))
			}
		}
		return strings.Join(parts, "\n")
	}
	before := snap()

	// Outside the audience: dave (three steps from alice) replies for his friends only.
	hidden := e.postDraft("dave", posts.Draft{Body: "for carol only", Lang: "en", Audience: "d1"}, root)
	e.postDraft("dave", posts.Draft{Body: "deeper, for carol only", Lang: "en", Audience: "d1"}, bobReply)
	// Someone alice blocked replies; dave replies to the friends-only post (the
	// link reveals he is within carol's limit, so it is hidden from alice).
	blocked := e.post("erin", "erin replies", root)
	e.post("dave", "dave on the strict post", strict)
	if got := snap(); got != before {
		t.Fatalf("adding invisible children changed the answers\nbefore %s\nafter  %s", before, got)
	}
	if err := e.a.Posts.Detach(e.as("carol"), e.ref("carol", root), e.ref("carol", hidden)); err != nil {
		t.Fatal(err)
	}
	if err := e.a.Posts.Delete(e.as("erin"), e.ref("erin", blocked)); err != nil {
		t.Fatal(err)
	}
	if got := snap(); got != before {
		t.Fatalf("detaching / deleting invisible children changed the answers\nbefore %s\nafter  %s", before, got)
	}
	// The visible child is still there (the comparison is not vacuous).
	if !strings.Contains(before, "bob replies") || !strings.Contains(before, "alice replies") {
		t.Fatalf("fixture %s", before)
	}
}

// mustOpen opens alice's post ref (test helper for refs taken from a view).
func mustOpen(t *testing.T, e *env, r gate.Ref) core.PostID {
	t.Helper()
	id, ok := e.d.Refs.OpenPost(e.id("alice"), r)
	if !ok {
		t.Fatal("ref does not open")
	}
	return id
}

// Acceptance: no placeholder for hidden parents. A post whose parents are all
// hidden (outside their audience, or the link hidden by a ref policy) looks
// exactly like a root post.
func TestHiddenParentsLookLikeRoot(t *testing.T) {
	e := newEnv(t)
	secret := e.postDraft("dave", posts.Draft{Body: "dave for friends", Lang: "en", Audience: "d1"})
	strict := e.postDraft("carol", posts.Draft{Body: "carol strict", Lang: "en", RefPolicy: "friends"})
	under := e.post("carol", "carol under a hidden parent", secret)
	onStrict := e.post("dave", "dave on a strict post", strict)
	root := e.post("carol", "carol root")
	for _, f := range []core.PostID{under, onStrict} {
		got, want := e.open("alice", f, "balanced"), e.open("alice", root, "balanced")
		if len(got.Rows) != 1 || got.Rows[0].Gen != 0 || got.Rows[0].Center != got.Focal || got.HasEarlier || got.HasLater ||
			len(got.Rows[0].Items) != len(want.Rows[0].Items) || got.Rows[0].Items[0].AlsoParents != nil {
			t.Fatalf("hidden parent shows: %s vs root %s", shape(got), shape(want))
		}
	}
	// The strict post itself lists no replies and counts none.
	tv := e.open("alice", strict, "balanced")
	if shape(tv) != "0:this_post[carol strict]" || tv.Rows[0].Items[0].Post.ReplyCount != 0 {
		t.Fatalf("strict %s", shape(tv))
	}
	// Carol sees both links.
	if got := shape(e.open("carol", under, "balanced")); got != "-1:replying_to[dave for friends] 0:this_post[carol under a hidden parent]" {
		t.Fatalf("carol %s", got)
	}
}

// Acceptance: a diamond shows each post once; the second spot is "shown above"
// (no body), and the child's other parent is a chip.
func TestDiamondShownOnce(t *testing.T) {
	e := newEnv(t)
	r := e.post("alice", "root")
	a := e.post("bob", "a", r)
	d := e.post("carol", "d", r, a)
	tv := e.open("alice", r, "latest")
	// Row +1 by tier (bob is a friend, carol two steps away), row +2 = children of a.
	if got := shape(tv); got != "0:this_post[root] 1:replies[a,|d] 2:replies_to_replies[^carol]" {
		t.Fatalf("shape %s", got)
	}
	stub := tv.Rows[2].Items[0]
	if !stub.ShownAbove || stub.Post.Body != "" || stub.Post.Ref != e.ref("alice", d) || stub.AlsoParents != nil {
		t.Fatalf("stub %+v", stub)
	}
	full := tv.Rows[1].Items[1]
	if full.ShownAbove || len(full.AlsoParents) != 1 || full.AlsoParents[0] != e.ref("alice", a) || full.AlsoRepliesTo[0].Handle != "bob" {
		t.Fatalf("chips %+v", full)
	}
}

// Acceptance: row order is (tier, rank.Less) under the reader's preset; equal
// t_eff falls to a friend+ first hop, then the older first-hop edge, then the
// lower first-hop ID.
func TestRowOrder(t *testing.T) {
	e := newEnv(t)
	for _, u := range []string{"fone", "ftwo", "fthree", "ffour", "gtwo"} {
		e.w.User(u)
	}
	e.reloadUsers()
	t0 := coretest.Epoch
	e.d.Graph.AddEdge(e.id("alice"), e.id("fone"), core.LevelFriendPlus, t0.Add(48*time.Hour))
	e.d.Graph.AddEdge(e.id("alice"), e.id("ftwo"), core.LevelFriend, t0)
	e.d.Graph.AddEdge(e.id("alice"), e.id("fthree"), core.LevelFriend, t0.Add(24*time.Hour))
	e.d.Graph.AddEdge(e.id("alice"), e.id("ffour"), core.LevelFriend, t0.Add(24*time.Hour))
	e.d.Graph.AddEdge(e.id("ftwo"), e.id("gtwo"), core.LevelFriend, t0)

	at := e.d.Clock.Now()
	e.w.Post("alice", "root", "root", coretest.At(at.Add(-time.Hour)))
	for _, u := range []string{"gtwo", "ffour", "fthree", "ftwo", "fone"} {
		e.w.Post(u, u, u, coretest.At(at), e.w.ReplyTo("root"))
	}
	e.w.Post("gtwo", "gtwo-new", "gtwo newest", coretest.At(at.Add(time.Minute)), e.w.ReplyTo("root"))
	e.w.Post("ftwo", "ftwo-old", "ftwo older", coretest.At(at.Add(-time.Minute)), e.w.ReplyTo("root"))
	root := e.w.PostID("root")

	tv := e.open("alice", root, "latest")
	if got := shape(tv); got != "0:this_post[root] 1:replies[fone,ftwo,fthree,ffour,ftwo older,|gtwo newest,gtwo]" {
		t.Fatalf("latest %s", got)
	}
	// Balanced: network likes move g2's post up its own tier only.
	for _, u := range []string{"bob", "fran", "fone", "ftwo", "fthree"} {
		e.w.Like(u, "gtwo")
	}
	if err := e.a.Engage.Load(e.ctx); err != nil {
		t.Fatal(err)
	}
	tv = e.open("alice", root, "balanced")
	got := rowShape(tv.Rows[1])
	if !strings.HasSuffix(got, "|gtwo,gtwo newest]") || strings.Index(got, "|") < strings.Index(got, "ftwo older") {
		t.Fatalf("balanced %s", got)
	}
}

// Acceptance: traversal never routes through an invisible post. A visible
// grandchild under an invisible child is not reached from the root, and the
// invisible child cannot be used as a row center.
func TestTraversalNeverThroughInvisible(t *testing.T) {
	e := newEnv(t)
	r := e.post("alice", "root")
	c := e.postDraft("dave", posts.Draft{Body: "hidden middle", Lang: "en", Audience: "d1"}, r)
	g := e.post("carol", "visible below", c)
	tv := e.open("alice", r, "balanced")
	if got := shape(tv); got != "0:this_post[root]" || tv.HasLater {
		t.Fatalf("root %s", got)
	}
	for _, center := range []core.PostID{c, g} {
		for _, gen := range []int{1, 2} {
			if _, err := e.tr.Row(e.as("alice"), tv.Snapshot, gen, e.ref("alice", center)); !errors.Is(err, gate.ErrNotFound) {
				t.Fatalf("row %d through %v: %v", gen, center, err)
			}
		}
	}
	if got := shape(e.open("alice", g, "balanced")); got != "0:this_post[visible below]" {
		t.Fatalf("grandchild %s", got)
	}
}

// Acceptance: another viewer's snapshot id (or cursor) is gate.ErrNotFound; the
// reader's own snapshot that expired is Problem tree_snapshot_expired.
func TestSnapshotsAreViewerBound(t *testing.T) {
	e := newEnv(t)
	r := e.w.Post("bob", "root", "root")
	c := e.w.Post("alice", "child", "child", e.w.ReplyTo("root"))
	for i := 0; i < tree.RowPage+2; i++ {
		e.advance(time.Minute)
		e.w.Post([]string{"bob", "fran"}[i%2], fmt.Sprintf("r%02d", i), fmt.Sprintf("reply %02d", i), e.w.ReplyTo("child"))
	}
	tv := e.open("alice", r, "latest")
	if len(tv.Rows) != 3 || tv.Rows[2].Cursor == "" || tv.Rows[2].More != 2 {
		t.Fatalf("shape %s", shape(tv))
	}
	cur := tv.Rows[2].Cursor
	// bob can see every post involved, yet alice's snapshot and cursor are not his.
	if _, err := e.tr.Row(e.as("bob"), tv.Snapshot, 1, e.ref("bob", r)); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("foreign snapshot: %v", err)
	}
	if _, err := e.tr.More(e.as("bob"), tv.Snapshot, 2, e.ref("bob", c), cur); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("foreign cursor: %v", err)
	}
	for _, bad := range []string{"", "garbage", tv.Snapshot + "x", strings.ToUpper(tv.Snapshot)} {
		if _, err := e.tr.Row(e.as("alice"), bad, 1, e.ref("alice", r)); !errors.Is(err, gate.ErrNotFound) {
			t.Fatalf("snapshot %q: %v", bad, err)
		}
	}
	// A cursor of another row (or a snapshot id used as a cursor) does not verify.
	if _, err := e.tr.More(e.as("alice"), tv.Snapshot, 1, e.ref("alice", r), cur); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("cursor of another row: %v", err)
	}
	if _, err := e.tr.More(e.as("alice"), tv.Snapshot, 2, e.ref("alice", c), tv.Snapshot); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("snapshot as cursor: %v", err)
	}
	// Anonymous: the uniform not-found.
	if _, err := e.tr.Open(e.ctx, e.ref("alice", r), ""); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("anonymous: %v", err)
	}
	// alice's own snapshot after 30 unused minutes.
	e.advance(tree.SnapshotTTL + time.Minute)
	_, err := e.tr.Row(e.as("alice"), tv.Snapshot, 1, e.ref("alice", r))
	var p *gate.Problem
	if !errors.As(err, &p) || p.Code != tree.CodeSnapshotExpired || p.Status != gate.StatusConflict {
		t.Fatalf("expired: %v", err)
	}
	if e.tr.Snapshots() != 0 {
		t.Fatalf("%d snapshots kept", e.tr.Snapshots())
	}
}

// More pages through a row in its snapshot order; every page re-checks, so a
// deleted or newly invisible post disappears and More counts only what is
// still visible. Rows re-center on posts served so far, never on others.
func TestMoreRecheckAndRecenter(t *testing.T) {
	e := newEnv(t)
	r := e.w.Post("alice", "root", "root")
	var replies []core.PostID
	for i := 0; i < 2*tree.RowPage+2; i++ {
		e.advance(time.Minute)
		replies = append(replies, e.w.Post([]string{"bob", "fran"}[i%2], fmt.Sprintf("r%02d", i), fmt.Sprintf("reply %02d", i), e.w.ReplyTo("root")))
	}
	far := e.post("carol", "carol replies", r)
	tv := e.open("alice", r, "latest")
	row := tv.Rows[1]
	if len(row.Items) != tree.RowPage || row.More != tree.RowPage+3 || row.Items[0].Post.Body != "reply 21" {
		t.Fatalf("first page %s", rowShape(row))
	}
	// Not yet served: no row below it.
	if _, err := e.tr.Row(e.as("alice"), tv.Snapshot, 2, e.ref("alice", replies[0])); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("unserved center: %v", err)
	}
	// Published after the snapshot: not in it.
	e.advance(time.Minute)
	e.w.Post("bob", "late", "late reply", e.w.ReplyTo("root"))
	p2, err := e.tr.More(e.as("alice"), tv.Snapshot, 1, e.ref("alice", r), row.Cursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(p2.Items) != tree.RowPage || p2.More != 3 || p2.Items[0].Post.Body != "reply 11" || p2.Items[0].TierDivider {
		t.Fatalf("second page %s", rowShape(p2))
	}
	// carol's reply goes, and fran's replies (the odd ones) become invisible:
	// alice blocks her.
	if err := e.a.Posts.Delete(e.as("carol"), e.ref("carol", far)); err != nil {
		t.Fatal(err)
	}
	if err := e.a.Friends.Block(e.as("alice"), e.d.Refs.User(e.id("alice"), e.id("fran"))); err != nil {
		t.Fatal(err)
	}
	p3, err := e.tr.More(e.as("alice"), tv.Snapshot, 1, e.ref("alice", r), p2.Cursor)
	if err != nil {
		t.Fatal(err)
	}
	if got := rowShape(p3); got != "1:replies[reply 00]" || p3.Cursor != "" {
		t.Fatalf("third page %s", got)
	}
	// A post served on page two centers row +2 now; a served post that became
	// invisible does not.
	if _, err := e.tr.Row(e.as("alice"), tv.Snapshot, 2, e.ref("alice", replies[10])); err != nil {
		t.Fatalf("recenter: %v", err)
	}
	if _, err := e.tr.Row(e.as("alice"), tv.Snapshot, 2, e.ref("alice", replies[11])); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("recenter on an invisible post: %v", err)
	}
}

// Swiping a row re-centers it: the row below reloads as the new center's
// children (ranked once per snapshot), the rows above stay; the focal row
// re-centers on another parent of the focal post; rows beyond ±2 load on demand.
func TestRowRecenter(t *testing.T) {
	e := newEnv(t)
	p1 := e.post("bob", "parent one")
	p2 := e.post("fran", "parent two")
	e.post("fran", "under parent one", p1)
	e.post("bob", "under parent two", p2)
	f := e.post("alice", "focal", p1, p2)
	c1 := e.post("bob", "c1", f)
	c2 := e.post("fran", "c2", f)
	g1 := e.post("fran", "g1", c1)
	e.post("bob", "g2", c2)
	gg := e.post("bob", "gg", g1)
	e.post("fran", "ggg", gg)

	tv := e.open("alice", f, "latest")
	if got := shape(tv); got != "-1:replying_to[parent two,parent one] 0:this_post[focal,|under parent two] 1:replies[c2,c1] 2:replies_to_replies[g2]" {
		t.Fatalf("shape %s", got)
	}
	// Re-center the focal row on the other parent.
	r0 := e.row("alice", tv, 0, p1)
	if got := rowShape(r0); got != "0:this_post[focal,|under parent one]" {
		t.Fatalf("focal row %s", got)
	}
	// Swipe row +1 to c1: row +2 lists c1's children; then deeper rows.
	r2 := e.row("alice", tv, 2, c1)
	if got := rowShape(r2); got != "2:replies_to_replies[g1]" {
		t.Fatalf("row 2 %s", got)
	}
	r3 := e.row("alice", tv, 3, g1)
	if got := rowShape(r3); got != "3:generations_down[gg]" {
		t.Fatalf("row 3 %s", got)
	}
	// Re-centering row +1 again drops the deeper rows from the view.
	e.row("alice", tv, 2, c2)
	if _, err := e.tr.Row(e.as("alice"), tv.Snapshot, 4, e.ref("alice", gg)); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("row below a dropped row: %v", err)
	}
	// Earlier rows: parents of the row above's posts.
	if got := rowShape(e.row("alice", tv, -2, p2)); got != "-2:earlier[]" {
		t.Fatalf("earlier %s", got)
	}
}

func TestPresets(t *testing.T) {
	e := newEnv(t)
	r := e.post("bob", "root")
	for _, p := range []string{"top", "nope"} {
		_, err := e.tr.Open(e.as("alice"), e.ref("alice", r), p)
		var pr *gate.Problem
		if !errors.As(err, &pr) || pr.Code != tree.CodePresetUnavailable {
			t.Fatalf("preset %s: %v", p, err)
		}
	}
	// A bad ref answers not-found before any Problem about the preset.
	for _, ref := range []gate.Ref{e.ref("bob", r), "garbage"} {
		if _, err := e.tr.Open(e.as("alice"), ref, "top"); !errors.Is(err, gate.ErrNotFound) {
			t.Fatalf("ref %q: %v", ref, err)
		}
	}
	x := e.post("xavier", "elsewhere")
	if _, err := e.tr.Open(e.as("alice"), e.ref("alice", x), ""); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("other component: %v", err)
	}
	if _, err := e.tr.Open(e.as("alice"), e.ref("alice", r), ""); err != nil {
		t.Fatalf("saved preset: %v", err)
	}
}

func TestAccountDeletionDropsSnapshots(t *testing.T) {
	e := newEnv(t)
	r := e.post("bob", "root")
	e.open("alice", r, "")
	e.open("bob", r, "")
	e.d.Bus.Publish(core.AccountDeleted{ID: e.id("alice")})
	if e.tr.Snapshots() != 1 {
		t.Fatalf("%d snapshots", e.tr.Snapshots())
	}
	e.tr.Reset()
	if e.tr.Snapshots() != 0 {
		t.Fatal("reset keeps snapshots")
	}
	for i := 0; i < tree.SnapshotsPerUser+3; i++ {
		e.open("bob", r, "")
	}
	if e.tr.Snapshots() != tree.SnapshotsPerUser {
		t.Fatalf("%d snapshots for one viewer", e.tr.Snapshots())
	}
}
