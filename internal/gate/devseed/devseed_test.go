package devseed

import (
	"context"
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/graph"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coretest"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/config"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/ids"
	"github.com/Nibroc6/purple-chicken-cult/internal/store/storetest"
)

// env is a memory-backed Deps over a fresh database with the real graph engine,
// so Graph.Load reads what devseed wrote.
type env struct {
	m *coretest.Mem
	s *Service
	g *graph.Graph
}

func newEnv(t *testing.T) *env {
	t.Helper()
	m := coretest.NewMem(t)
	m.D.DB = storetest.NewDB(t)
	g := graph.New(m.D)
	m.D.Graph = g
	return &env{m: m, s: New(m.D), g: g}
}

func (e *env) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := e.m.D.DB.Q().QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRefusesOutsideDev(t *testing.T) {
	m := coretest.NewMem(t)
	cfg := *m.D.Cfg
	cfg.Env = config.EnvProd
	m.D.Cfg = &cfg
	s := New(m.D)
	ctx := context.Background()
	for name, err := range map[string]error{
		"synthetic": s.Synthetic(ctx, 10, "ba", 1),
		"fixture":   s.LoadFixture(ctx, "p0-w02"),
		"json":      s.LoadFixtureJSON(ctx, "x", []byte(`{}`)),
	} {
		if !errors.Is(err, ErrNotDev) {
			t.Errorf("%s: got %v, want ErrNotDev", name, err)
		}
	}
}

func TestSyntheticArguments(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for _, c := range []struct {
		n     int
		model string
	}{{0, "ba"}, {-1, "ws"}, {MaxSynthetic + 1, "ba"}, {10, "er"}} {
		if err := e.s.Synthetic(ctx, c.n, c.model, 1); err == nil {
			t.Errorf("Synthetic(%d, %q) succeeded", c.n, c.model)
		}
	}
	if n := e.count(t, "SELECT count(*) FROM users"); n != 0 {
		t.Fatalf("a refused seed wrote %d users", n)
	}
}

func TestSyntheticGraph(t *testing.T) {
	for _, model := range []string{"ba", "ws"} {
		t.Run(model, func(t *testing.T) {
			e := newEnv(t)
			ctx := context.Background()
			const n = 400
			if err := e.s.Synthetic(ctx, n, model, 5); err != nil {
				t.Fatal(err)
			}
			nodes := graph.Synth(n, model, 5)
			degSum := 0
			for _, ns := range nodes {
				degSum += len(ns)
			}
			if got := e.count(t, "SELECT count(*) FROM users"); got != n {
				t.Fatalf("users: %d", got)
			}
			if got := e.count(t, "SELECT count(*) FROM edges"); got != degSum {
				t.Fatalf("edges: %d, want %d (both directions)", got, degSum)
			}
			if got := e.count(t, "SELECT count(*) FROM users WHERE is_genesis"); got != 1 {
				t.Fatalf("genesis users: %d", got)
			}
			if got := e.count(t, "SELECT count(*) FROM users WHERE NOT is_genesis AND invited_by IS NULL"); got != 0 {
				t.Fatalf("%d invited users without an inviter", got)
			}
			var first int64
			if err := e.m.D.DB.Q().QueryRow(ctx, "SELECT min(id) FROM users").Scan(&first); err != nil {
				t.Fatal(err)
			}
			// The loaded engine equals the reference BFS over the generator's graph.
			adj := graph.SynthAdjacency(nodes, core.UserID(first), coretest.Epoch)
			rng := rand.New(rand.NewPCG(3, 4)) //nolint:gosec // test data
			for range 15 {
				v := core.UserID(first) + core.UserID(rng.IntN(n))
				ref := graph.ReferenceBFS(adj, v, core.Exclusions{})
				sr, err := e.g.Search(ctx, v, core.Exclusions{})
				if err != nil {
					t.Fatal(err)
				}
				for u, want := range ref {
					if got, ok := sr.Dist(u); !ok || got != want {
						t.Fatalf("Dist(%d→%d) = %d,%v want %d", v, u, got, ok, want)
					}
				}
			}
			// Users reach the in-memory users module too.
			u, err := e.m.D.Users.Get(ctx, core.UserID(first)+1)
			if err != nil || u.InvitedBy == 0 || !strings.HasPrefix(u.Handle, "syn_") {
				t.Fatalf("user meta %+v, %v", u, err)
			}
			// Posts: Poisson mean 7/user, within the last 7 days, in memory and DB.
			posts := e.count(t, "SELECT count(*) FROM posts")
			if posts < n*5 || posts > n*9 {
				t.Fatalf("posts: %d for %d users", posts, n)
			}
			if got := e.count(t, "SELECT count(*) FROM post_versions"); got != posts {
				t.Fatalf("post_versions: %d", got)
			}
			now := e.m.Clock.Now()
			if got := e.count(t, "SELECT count(*) FROM posts WHERE created_at > $1 OR created_at < $2",
				now, now.Add(-7*24*time.Hour)); got != 0 {
				t.Fatalf("%d posts outside the 7-day window", got)
			}
			if got := e.m.Posts.WindowSize(); got == 0 {
				t.Fatal("posts did not reach the post index")
			}
			// Replies point at an older post of a friend that is not slippery.
			if got := e.count(t, `SELECT count(*) FROM post_parents l JOIN posts c ON c.id = l.child_id JOIN posts p ON p.id = l.parent_id
				LEFT JOIN edges e ON e.user_id = l.child_author AND e.friend_id = l.parent_author
				WHERE e.user_id IS NULL OR p.created_at >= c.created_at OR p.ref_policy = 3`); got != 0 {
				t.Fatalf("%d invalid reply links", got)
			}
			if got := e.count(t, "SELECT count(*) FROM post_parents"); got == 0 {
				t.Fatal("no replies")
			}
			// Likes come from friends of the author.
			if got := e.count(t, `SELECT count(*) FROM engagements g JOIN posts p ON p.id = g.post_id
				LEFT JOIN edges e ON e.user_id = g.user_id AND e.friend_id = p.author_id WHERE e.user_id IS NULL`); got != 0 {
				t.Fatalf("%d likes from non-friends", got)
			}
			var liked core.PostID
			if err := e.m.D.DB.Q().QueryRow(ctx, "SELECT post_id FROM engagements LIMIT 1").Scan(&liked); err != nil {
				t.Fatal(err)
			}
			if e.m.Engage.LikerSet(ctx, liked).IsEmpty() {
				t.Fatal("likes did not reach the engagement module")
			}
		})
	}
}

// The same seed over the same starting ID yields identical data.
func TestSyntheticDeterministic(t *testing.T) {
	now := coretest.Epoch
	a, err := synthetic(300, "ba", 9, 100, now)
	if err != nil {
		t.Fatal(err)
	}
	b, err := synthetic(300, "ba", 9, 100, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.posts) != len(b.posts) || len(a.links) != len(b.links) || len(a.likes) != len(b.likes) || len(a.edges) != len(b.edges) {
		t.Fatal("sizes differ")
	}
	for i := range a.posts {
		if a.posts[i] != b.posts[i] {
			t.Fatalf("post %d differs", i)
		}
	}
	for i := range a.likes {
		if a.likes[i] != b.likes[i] {
			t.Fatalf("like %d differs", i)
		}
	}
	c, err := synthetic(300, "ba", 10, 100, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.posts) == len(a.posts) && c.posts[0] == a.posts[0] {
		t.Fatal("a different seed produced the same data")
	}
}

// A second synthetic seed appends after the existing users.
func TestSyntheticTwice(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if err := e.s.Synthetic(ctx, 50, "ba", 1); err != nil {
		t.Fatal(err)
	}
	if err := e.s.Synthetic(ctx, 50, "ws", 1); err != nil {
		t.Fatal(err)
	}
	if got := e.count(t, "SELECT count(*) FROM users"); got != 100 {
		t.Fatalf("users: %d", got)
	}
	// The identity sequence continues after the seeded users.
	var next int64
	if err := e.m.D.DB.Q().QueryRow(ctx, "SELECT nextval(pg_get_serial_sequence('users', 'id'))").Scan(&next); err != nil {
		t.Fatal(err)
	}
	if next != 101 {
		t.Fatalf("next user id %d", next)
	}
}

const fixtureDoc = `{
  "users": [
    {"handle": "Alice", "name": "Alice A"},
    {"handle": "bob", "invited_by": "alice", "probation": "72h", "role": "moderator"},
    {"handle": "carol", "invited_by": "bob", "name_in_chains": true, "suggestible": false, "preset": "latest"},
    {"handle": "dave", "invited_by": "alice", "status": "suspended", "age": "48h"}
  ],
  "friends": [
    {"a": "alice", "b": "bob", "level": "friend_plus"},
    {"a": "bob", "b": "carol"},
    {"a": "carol", "b": "dave"}
  ],
  "blocks": [["dave", "alice"]],
  "mutes": [["alice", "carol"]],
  "posts": [
    {"label": "fx-root", "author": "alice", "body": "hello", "age": "3h"},
    {"label": "fx-cont", "author": "alice", "body": "more", "age": "2h", "parents": [{"post": "fx-root", "kind": "cont"}]},
    {"label": "fx-reply", "author": "bob", "body": "hi @alice", "audience": "d2", "ref_policy": "nobody",
     "parents": [{"post": "fx-root"}]},
    {"label": "fx-gone", "author": "carol", "body": "bye", "deleted": true}
  ],
  "likes": [{"user": "bob", "post": "fx-root"}, {"user": "carol", "post": "fx-reply", "age": "1m"}]
}`

func TestLoadFixture(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "p0-w02.json"), []byte(fixtureDoc), 0o600); err != nil {
		t.Fatal(err)
	}
	e.s.SetFixtureDir(dir)
	if err := e.s.LoadFixture(ctx, "p0-w02"); err != nil {
		t.Fatal(err)
	}
	base := fixtureBase("p0-w02")
	alice, bob, carol, dave := base, base+1, base+2, base+3
	now := e.m.Clock.Now()

	// Users: label-independent explicit IDs, attributes, DB and memory agree.
	if got := e.count(t, "SELECT count(*) FROM users WHERE id BETWEEN $1 AND $2", int64(alice), int64(dave)); got != 4 {
		t.Fatalf("users: %d", got)
	}
	if got := e.count(t, `SELECT count(*) FROM users WHERE id = $1 AND role = 1 AND invited_by = $2 AND NOT is_genesis
		AND probation_until = $3 AND probation_cleared_at IS NULL`, int64(bob), int64(alice), now.Add(72*time.Hour)); got != 1 {
		t.Fatal("bob's row")
	}
	if got := e.count(t, "SELECT count(*) FROM users WHERE id = $1 AND handle = 'Alice' AND handle_canon = 'alice' AND is_genesis",
		int64(alice)); got != 1 {
		t.Fatal("alice's row")
	}
	um, err := e.m.D.Users.Get(ctx, bob)
	if err != nil || !um.OnProbation(now) || um.Role != core.RoleModerator {
		t.Fatalf("bob in memory: %+v %v", um, err)
	}
	cm, _ := e.m.D.Users.Get(ctx, carol)
	if !cm.NameInChains || cm.Suggestible || cm.Preset != core.PresetLatest || cm.InvitedBy != bob {
		t.Fatalf("carol in memory: %+v", cm)
	}
	if dm, _ := e.m.D.Users.Get(ctx, dave); dm.Status != core.StatusSuspended || !dm.CreatedAt.Equal(now.Add(-48*time.Hour)) {
		t.Fatalf("dave in memory: %+v", dm)
	}

	// Graph: edges in both directions, levels, blocks one-directional.
	if lvl, ok := e.g.Level(alice, bob); !ok || lvl != core.LevelFriendPlus {
		t.Fatalf("alice–bob level %v %v", lvl, ok)
	}
	if got := e.count(t, "SELECT count(*) FROM edges"); got != 6 {
		t.Fatalf("edges: %d", got)
	}
	if !e.g.Blocks(dave, alice) || e.g.Blocks(alice, dave) {
		t.Fatal("block direction")
	}
	sr, err := e.g.Search(ctx, alice, e.g.Exclusions(alice))
	if err != nil {
		t.Fatal(err)
	}
	if d, ok := sr.Dist(dave); !ok || d != 3 {
		t.Fatalf("Dist(alice→dave) = %d %v", d, ok)
	}
	if _, ok := e.m.Mutes.MutedAuthors(ctx, alice)[carol]; !ok {
		t.Fatal("mute not applied")
	}

	// Posts: label-derived IDs, links, cont chain, reply flag, deletion.
	root, cont, reply, gone := ids.Named("post:fx-root"), ids.Named("post:fx-cont"), ids.Named("post:fx-reply"), ids.Named("post:fx-gone")
	rm, ok := e.m.Posts.Meta(ctx, root)
	if !ok || rm.ContChild != cont || !rm.CreatedAt.Equal(now.Add(-3*time.Hour)) {
		t.Fatalf("root meta %+v %v", rm, ok)
	}
	if m, _ := e.m.Posts.Meta(ctx, cont); m.ContParent != root || m.IsReply {
		t.Fatalf("cont meta %+v", m)
	}
	if m, _ := e.m.Posts.Meta(ctx, reply); !m.IsReply || m.Audience != core.AudD2 || m.RefPolicy != core.PolNobody {
		t.Fatalf("reply meta %+v", m)
	}
	if m, _ := e.m.Posts.Meta(ctx, gone); !m.Deleted {
		t.Fatalf("gone meta %+v", m)
	}
	if got := e.count(t, "SELECT count(*) FROM posts WHERE id = $1 AND body IS NULL AND deleted_at IS NOT NULL", gone); got != 1 {
		t.Fatal("deleted post row")
	}
	if got := e.count(t, "SELECT count(*) FROM post_parents WHERE parent_id = $1", root); got != 2 {
		t.Fatalf("links to root: %d", got)
	}
	if got := e.count(t, "SELECT count(*) FROM post_parents WHERE child_id = $1 AND kind = 1 AND ord = 0", cont); got != 1 {
		t.Fatal("cont link row")
	}
	kids := 0
	e.m.Posts.Children(ctx, root, func(core.LinkMeta) bool { kids++; return true })
	if kids != 2 {
		t.Fatalf("children in memory: %d", kids)
	}
	if got := e.count(t, "SELECT count(*) FROM posts WHERE id = $1 AND audience = 2 AND ref_policy = 3", reply); got != 1 {
		t.Fatal("reply row")
	}

	// Likes.
	if !e.m.Engage.LikerSet(ctx, root).Contains(uint32(bob)) || !e.m.Engage.LikerSet(ctx, reply).Contains(uint32(carol)) {
		t.Fatal("likes not applied")
	}
	if got := e.count(t, "SELECT count(*) FROM engagements WHERE kind = 1"); got != 2 {
		t.Fatalf("likes: %d", got)
	}

	// Loading the same fixture twice fails (same IDs) and writes nothing.
	if err := e.s.LoadFixture(ctx, "p0-w02"); err == nil {
		t.Fatal("second load succeeded")
	}
	if got := e.count(t, "SELECT count(*) FROM users"); got != 4 {
		t.Fatalf("users after failed reload: %d", got)
	}
}

// A second fixture can build on users and posts of the first one.
func TestFixtureReferencesExisting(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if err := e.s.LoadFixtureJSON(ctx, "one", []byte(fixtureDoc)); err != nil {
		t.Fatal(err)
	}
	two := `{"users": [{"handle": "erin", "invited_by": "carol"}],
		"friends": [{"a": "erin", "b": "carol"}],
		"posts": [{"label": "fx-erin", "author": "erin", "body": "late reply", "parents": [{"post": "fx-reply"}]},
		          {"label": "fx-cont2", "author": "bob", "body": "and", "parents": [{"post": "fx-reply", "kind": "cont"}]}]}`
	if err := e.s.LoadFixtureJSON(ctx, "two", []byte(two)); err != nil {
		t.Fatal(err)
	}
	erin := fixtureBase("two")
	if fixtureBase("one") == erin {
		t.Fatal("fixture ranges collide")
	}
	if l, ok := e.g.Level(erin, fixtureBase("one")+2); !ok || l != core.LevelFriend {
		t.Fatal("erin–carol edge")
	}
	if m, _ := e.m.Posts.Meta(ctx, ids.Named("post:fx-reply")); m.ContChild != ids.Named("post:fx-cont2") {
		t.Fatalf("existing parent's cont child not updated: %+v", m)
	}
}

func TestFixtureErrors(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for name, doc := range map[string]string{
		"unknown field":   `{"users": [{"handle": "abc", "nickname": "x"}]}`,
		"trailing":        `{} {}`,
		"bad handle":      `{"users": [{"handle": "a"}]}`,
		"dup user":        `{"users": [{"handle": "abc"}, {"handle": "ABC"}]}`,
		"unknown inviter": `{"users": [{"handle": "abc", "invited_by": "nobody"}]}`,
		"genesis prob":    `{"users": [{"handle": "abc", "probation": "1h"}]}`,
		"bad role":        `{"users": [{"handle": "abc", "role": "king"}]}`,
		"bad duration":    `{"users": [{"handle": "abc", "age": "soon"}]}`,
		"negative age":    `{"users": [{"handle": "abc", "age": "-1h"}]}`,
		"self friend":     `{"users": [{"handle": "abc"}], "friends": [{"a": "abc", "b": "abc"}]}`,
		"dup friend":      `{"users": [{"handle": "abc"}, {"handle": "abd"}], "friends": [{"a": "abc", "b": "abd"}, {"a": "abd", "b": "abc"}]}`,
		"bad level":       `{"users": [{"handle": "abc"}, {"handle": "abd"}], "friends": [{"a": "abc", "b": "abd", "level": "best"}]}`,
		"self block":      `{"users": [{"handle": "abc"}], "blocks": [["abc", "abc"]]}`,
		"friend blocked":  `{"users": [{"handle": "abc"}, {"handle": "abd"}], "friends": [{"a": "abc", "b": "abd"}], "blocks": [["abd", "abc"]]}`,
		"no label":        `{"users": [{"handle": "abc"}], "posts": [{"author": "abc"}]}`,
		"dup label":       `{"users": [{"handle": "abc"}], "posts": [{"label": "x", "author": "abc"}, {"label": "x", "author": "abc"}]}`,
		"bad audience":    `{"users": [{"handle": "abc"}], "posts": [{"label": "x", "author": "abc", "audience": "all"}]}`,
		"bad policy":      `{"users": [{"handle": "abc"}], "posts": [{"label": "x", "author": "abc", "ref_policy": "some"}]}`,
		"later parent":    `{"users": [{"handle": "abc"}], "posts": [{"label": "x", "author": "abc", "parents": [{"post": "y"}]}, {"label": "y", "author": "abc"}]}`,
		"foreign cont":    `{"users": [{"handle": "abc"}, {"handle": "abd"}], "posts": [{"label": "x", "author": "abc"}, {"label": "y", "author": "abd", "parents": [{"post": "x", "kind": "cont"}]}]}`,
		"two conts":       `{"users": [{"handle": "abc"}], "posts": [{"label": "x", "author": "abc"}, {"label": "y", "author": "abc", "parents": [{"post": "x", "kind": "cont"}]}, {"label": "z", "author": "abc", "parents": [{"post": "x", "kind": "cont"}]}]}`,
		"dup parent":      `{"users": [{"handle": "abc"}], "posts": [{"label": "x", "author": "abc"}, {"label": "y", "author": "abc", "parents": [{"post": "x"}, {"post": "x"}]}]}`,
		"four parents":    `{"users": [{"handle": "abc"}], "posts": [{"label": "a", "author": "abc"}, {"label": "b", "author": "abc"}, {"label": "c", "author": "abc"}, {"label": "d", "author": "abc"}, {"label": "x", "author": "abc", "parents": [{"post": "a"}, {"post": "b"}, {"post": "c"}, {"post": "d"}]}]}`,
		"bad kind":        `{"users": [{"handle": "abc"}], "posts": [{"label": "x", "author": "abc"}, {"label": "y", "author": "abc", "parents": [{"post": "x", "kind": "quote"}]}]}`,
		"like unknown":    `{"users": [{"handle": "abc"}], "likes": [{"user": "abc", "post": "x"}]}`,
	} {
		if err := e.s.LoadFixtureJSON(ctx, "bad", []byte(doc)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	for _, name := range []string{"../etc/passwd", "", "A", "x/y", strings.Repeat("a", 65)} {
		if err := e.s.LoadFixture(ctx, name); err == nil || !strings.Contains(err.Error(), "invalid fixture name") {
			t.Errorf("name %q: %v", name, err)
		}
	}
	if err := e.s.LoadFixture(ctx, "does-not-exist-anywhere"); err == nil {
		t.Error("missing fixture accepted")
	}
	if got := e.count(t, "SELECT count(*) FROM users"); got != 0 {
		t.Fatalf("rejected fixtures wrote %d users", got)
	}
}

// Without an override, fixtures are read from <module root>/testdata/fixtures.
func TestModuleRoot(t *testing.T) {
	root, err := moduleRoot()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "internal", "gate", "devseed")); err != nil {
		t.Fatalf("root %s is not this module: %v", root, err)
	}
}

func TestFixtureBaseRange(t *testing.T) {
	for _, n := range []string{"p0-w01", "p0-w02", "p1-w12", "x", strings.Repeat("z", 64)} {
		b := fixtureBase(n)
		if b < FirstFixtureID || uint64(b)+slotSize > 1<<32-1 {
			t.Fatalf("%s: base %d out of range", n, b)
		}
		if b != fixtureBase(n) {
			t.Fatal("not deterministic")
		}
	}
}
