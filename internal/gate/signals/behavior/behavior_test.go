package behavior_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coretest"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/signals/behavior"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire/wiretest"
)

type env struct {
	t   *testing.T
	a   *wire.App
	d   *core.Deps
	b   *behavior.Service
	w   *wiretest.TestWorld
	ctx context.Context
	n   int
}

func newEnv(t *testing.T) *env {
	t.Helper()
	a := wiretest.NewApp(t)
	return &env{t: t, a: a, d: a.Deps(), b: a.Behavior, w: wiretest.World(t, a), ctx: context.Background()}
}

func (e *env) must(err error) {
	e.t.Helper()
	if err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	e.must(e.d.DB.Q().QueryRow(e.ctx, sql, args...).Scan(&n))
	return n
}

func (e *env) exec(sql string, args ...any) {
	e.t.Helper()
	_, err := e.d.DB.Q().Exec(e.ctx, sql, args...)
	e.must(err)
}

func (e *env) reload() {
	e.t.Helper()
	e.must(e.a.Users.Reload(e.ctx))
}

// post writes a post by author, age before now.
func (e *env) post(author, body string, age time.Duration) {
	e.t.Helper()
	e.n++
	e.w.Post(author, fmt.Sprintf("p%d", e.n), body, coretest.At(e.d.Clock.Now().Add(-age)))
}

func (e *env) id(h string) core.UserID { return e.w.ID(h) }

// items returns the open queue items as (source, subject handle).
func (e *env) items() map[string]int {
	e.t.Helper()
	rows, err := e.d.DB.Q().Query(e.ctx, `SELECT q.source, u.handle FROM mod_queue q JOIN users u ON u.id = q.subject_user
		WHERE q.resolved_at IS NULL`)
	e.must(err)
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var (
			src int16
			h   string
		)
		e.must(rows.Scan(&src, &h))
		out[fmt.Sprintf("%d:%s", src, h)]++
	}
	return out
}

// Acceptance: cadence flags round-the-clock and machine-regular posting and
// never a person posting by hand; signals only queue (nobody is suspended,
// stepped up or told).
func TestCadence(t *testing.T) {
	e := newEnv(t)
	for _, h := range []string{"alice", "clock", "metro", "human"} {
		e.w.User(h)
	}
	e.reload()
	for i := range 20 { // every hour, round the clock
		e.post("clock", fmt.Sprintf("tick %d", i), time.Duration(i)*time.Hour+time.Duration(i*7)*time.Minute)
	}
	for i := range 13 { // every 5 minutes on the dot, within one hour
		e.post("metro", fmt.Sprintf("beat %d", i), time.Duration(i)*5*time.Minute)
	}
	for i, at := range []time.Duration{2 * time.Minute, 40 * time.Minute, 3 * time.Hour, 3*time.Hour + 5*time.Minute,
		9 * time.Hour, 9*time.Hour + 50*time.Minute, 10 * time.Hour, 14 * time.Hour, 15 * time.Hour, 15*time.Hour + 2*time.Minute,
		20 * time.Hour, 21 * time.Hour, 23 * time.Hour} { // 13 posts, irregular, 9 distinct hours
		e.post("human", fmt.Sprintf("thought %d", i), at)
	}
	n, err := e.b.Cadence(e.ctx)
	e.must(err)
	if n != 2 {
		t.Fatalf("flagged %d", n)
	}
	got := e.items()
	if got["1:clock"] != 1 || got["1:metro"] != 1 || got["1:human"] != 0 {
		t.Fatalf("items %v", got)
	}
	if e.count("SELECT count(*) FROM automation_signals WHERE kind = 'cadence' AND (data->>'posts')::int >= 13") != 2 {
		t.Fatal("signal rows")
	}
	// A second night merges into the open items.
	_, err = e.b.Cadence(e.ctx)
	e.must(err)
	if e.count("SELECT count(*) FROM mod_queue") != 2 {
		t.Fatal("a second night duplicated the items")
	}
	if e.count("SELECT count(*) FROM users WHERE status <> 0") != 0 || e.count("SELECT count(*) FROM stepups") != 0 ||
		e.count("SELECT count(*) FROM notifications") != 0 {
		t.Fatal("a signal acted on its own")
	}
}

// Acceptance: the similarity job links accounts with near-identical posts that
// share an inviter within three levels, never stores a body (MinHash only), and
// does not link look-alikes in unrelated invite trees.
func TestSimilarityStoresNoBodies(t *testing.T) {
	e := newEnv(t)
	e.w.User("root")
	e.w.User("other")
	e.w.User("mid", coretest.InvitedBy(e.id("root")))
	for _, h := range []string{"bot1", "bot2"} {
		e.w.User(h, coretest.InvitedBy(e.id("root")))
	}
	e.w.User("bot3", coretest.InvitedBy(e.id("mid")))  // a grandchild: same subtree
	e.w.User("far", coretest.InvitedBy(e.id("other"))) // same text, unrelated tree
	e.w.User("kin", coretest.InvitedBy(e.id("root")))  // same tree, own words
	e.reload()
	spam := []string{
		"Incredible zebrafinch supplements cure everything overnight, order today at the shop",
		"Limited offer on zebrafinch supplements, doctors hate this one weird trick",
		"Zebrafinch supplements changed my life and they can change yours too",
	}
	for _, h := range []string{"bot1", "bot2", "bot3", "far"} {
		for i, s := range spam {
			e.post(h, s, time.Duration(i+1)*time.Hour)
		}
	}
	e.post("kin", "Went hiking by the lake with my sister, the weather was lovely and cold", time.Hour)
	e.post("kin", "Baked bread again; the crust finally came out right after many tries", 2*time.Hour)

	n, err := e.b.Similarity(e.ctx)
	e.must(err)
	if n != 1 {
		t.Fatalf("clusters %d", n)
	}
	got := e.items()
	if got["1:bot1"] != 1 || len(got) != 1 {
		t.Fatalf("items %v", got)
	}
	if c := e.count(`SELECT count(*) FROM automation_signals WHERE kind = 'similarity' AND (data->>'accounts')::int = 3
		AND user_id = ANY($1)`, []int64{int64(e.id("bot1")), int64(e.id("bot2")), int64(e.id("bot3"))}); c != 3 {
		t.Fatalf("signal rows %d", c)
	}
	if e.count("SELECT count(*) FROM automation_signals WHERE user_id = ANY($1)",
		[]int64{int64(e.id("far")), int64(e.id("kin"))}) != 0 {
		t.Fatal("linked across trees or by topic")
	}
	// No body text, no word of it, in anything the job wrote.
	var dump string
	e.must(e.d.DB.Q().QueryRow(e.ctx, `SELECT COALESCE(string_agg(data::text, ' '), '') || ' ' ||
		(SELECT COALESCE(string_agg(summary::text || COALESCE(resolution, ''), ' '), '') FROM mod_queue)
		FROM automation_signals`).Scan(&dump))
	if strings.Contains(strings.ToLower(dump), "zebrafinch") || strings.Contains(dump, "supplement") {
		t.Fatalf("a body reached storage: %s", dump)
	}
}

// Acceptance: invite clusters correlate on net_h only — ≥ 3 invitees of one
// inviter on one network — and never copy the hash; signals.purge drops rows
// after 30 days.
func TestInviteClustersAndPurge(t *testing.T) {
	e := newEnv(t)
	e.w.User("host")
	for _, h := range []string{"aaa", "bbb", "ccc", "ddd"} {
		e.w.User(h, coretest.InvitedBy(e.id("host")))
	}
	e.w.User("lone")
	e.w.User("eee", coretest.InvitedBy(e.id("lone")))
	e.reload()
	now := e.d.Clock.Now()
	net := []byte("0123456789abcdef0123456789abcdef")
	for _, h := range []string{"aaa", "bbb", "ccc", "eee"} {
		e.exec("INSERT INTO automation_signals (user_id, kind, score, net_h, at) VALUES ($1, 'sustained_rate', 1, $2, $3)",
			int64(e.id(h)), net, now.Add(-time.Hour))
	}
	e.exec("INSERT INTO automation_signals (user_id, kind, score, net_h, at) VALUES ($1, 'sustained_rate', 1, $2, $3)",
		int64(e.id("ddd")), []byte("another network hash 0123456789"), now.Add(-time.Hour))
	f, err := e.b.Nightly(e.ctx)
	e.must(err)
	if f.InviteClusters != 1 || f.Cadence != 0 || f.Similarity != 0 {
		t.Fatalf("findings %+v", f)
	}
	if got := e.items(); got["4:aaa"] != 1 || len(got) != 1 {
		t.Fatalf("items %v", got)
	}
	if e.count("SELECT count(*) FROM automation_signals WHERE kind = 'invite_cluster' AND net_h IS NULL") != 3 ||
		e.count("SELECT count(*) FROM automation_signals WHERE kind = 'invite_cluster'") != 3 {
		t.Fatal("cluster rows (and no copied hash)")
	}
	var summary string
	e.must(e.d.DB.Q().QueryRow(e.ctx, "SELECT summary::text FROM mod_queue").Scan(&summary))
	if !strings.Contains(summary, `"shared_network": 1`) || strings.Contains(summary, "0123") {
		t.Fatalf("summary %s", summary)
	}

	e.exec("INSERT INTO automation_signals (user_id, kind, score, at) VALUES ($1, 'cadence', 1, $2)",
		int64(e.id("lone")), now.Add(-behavior.Retention-time.Minute))
	n, err := e.b.Purge(e.ctx)
	e.must(err)
	if n != 1 || e.count("SELECT count(*) FROM automation_signals WHERE at < $1", now.Add(-behavior.Retention)) != 0 {
		t.Fatalf("purged %d", n)
	}
}

// Acceptance: no raw IP column exists anywhere in the schema (cross-account
// correlation uses net_h only).
func TestNoRawIPColumn(t *testing.T) {
	e := newEnv(t)
	rows, err := e.d.DB.Q().Query(e.ctx, `SELECT table_name, column_name, data_type FROM information_schema.columns
		WHERE table_schema = 'public' AND (data_type IN ('inet', 'cidr', 'macaddr', 'macaddr8')
			OR column_name ~ '(^|_)(ip|ips|ipv4|ipv6|ip_addr|addr|address|remote_addr)($|_)')`)
	e.must(err)
	defer rows.Close()
	for rows.Next() {
		var tbl, col, typ string
		e.must(rows.Scan(&tbl, &col, &typ))
		t.Errorf("raw address column %s.%s (%s)", tbl, col, typ)
	}
}

func TestMinHashEstimates(t *testing.T) {
	a, _ := behavior.SketchOf([]string{"the quick brown fox jumps over the lazy dog again and again"})
	b, _ := behavior.SketchOf([]string{"The quick brown fox jumps over the lazy dog again and again!"})
	c, _ := behavior.SketchOf([]string{"completely different words about baking bread in a stone oven"})
	if behavior.Jaccard(a, b) != 1 {
		t.Fatalf("same words: %v", behavior.Jaccard(a, b))
	}
	if behavior.Jaccard(a, c) > 0.1 {
		t.Fatalf("different words: %v", behavior.Jaccard(a, c))
	}
	if _, ok := behavior.SketchOf([]string{"", "  ;; "}); ok {
		t.Fatal("empty text sketched")
	}
}
