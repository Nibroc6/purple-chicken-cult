package limits_test

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/limits"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire/wiretest"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/httpx"
)

type env struct {
	t   *testing.T
	a   *wire.App
	d   *core.Deps
	l   *limits.Service
	w   *wiretest.TestWorld
	ctx context.Context
}

func newEnv(t *testing.T) *env {
	t.Helper()
	a := wiretest.NewApp(t)
	e := &env{t: t, a: a, d: a.Deps(), l: a.Limits, w: wiretest.World(t, a), ctx: context.Background()}
	for _, u := range []string{"alice", "bob"} {
		e.w.User(u)
	}
	if err := a.Users.Reload(e.ctx); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) viewer(h string) core.Viewer {
	return core.Viewer{ID: e.w.ID(h), Kind: core.SessUser, AuthAt: e.d.Clock.Now()}
}

func (e *env) as(h string) context.Context { return core.WithViewer(e.ctx, e.viewer(h)) }

func (e *env) advance(d time.Duration) {
	e.t.Helper()
	if err := e.a.Hooks().AdvanceClock(d); err != nil {
		e.t.Fatal(err)
	}
}

// count is the stored count of user's bucket in the current window of length w.
func (e *env) count(h, bucket string, w time.Duration) int {
	e.t.Helper()
	var n int
	err := e.d.DB.Q().QueryRow(e.ctx, `SELECT coalesce(sum(count), 0) FROM rate_counters
		WHERE user_id = $1 AND bucket = $2 AND window_start = $3`, int64(e.w.ID(h)), bucket, e.d.Clock.Now().Truncate(w)).Scan(&n)
	if err != nil {
		e.t.Fatal(err)
	}
	return n
}

func retryOf(t *testing.T, err error) time.Duration {
	t.Helper()
	var rl *gate.RateLimitedError
	if !errors.As(err, &rl) || !errors.Is(err, gate.ErrRateLimited) {
		t.Fatalf("want rate_limited, got %v", err)
	}
	return rl.RetryAfter
}

func TestReadBudgetPerUTCDay(t *testing.T) {
	e := newEnv(t)
	p := e.l.Policy()
	p.Reads["graph_read"] = 3
	e.l.SetPolicy(p)
	// Midday: the window is the UTC day.
	e.advance(12 * time.Hour)
	for i := 0; i < 3; i++ {
		if err := e.l.ChargeRead(e.ctx, e.viewer("alice"), limits.ReadGraph); err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
	}
	err := e.l.ChargeRead(e.ctx, e.viewer("alice"), limits.ReadGraph)
	if got := retryOf(t, err); got != 12*time.Hour {
		t.Fatalf("retry after %v, want the rest of the UTC day", got)
	}
	if n := e.count("alice", "r:graph_read", limits.Day); n != 3 {
		t.Fatalf("stored count %d", n)
	}
	// Exhausted buckets are answered from memory: no further writes.
	_ = e.l.ChargeRead(e.ctx, e.viewer("alice"), limits.ReadGraph)
	if n := e.count("alice", "r:graph_read", limits.Day); n != 3 {
		t.Fatalf("stored count grew to %d", n)
	}
	// Other classes and other people have their own budgets.
	if err := e.l.ChargeRead(e.ctx, e.viewer("alice"), limits.ReadPost); err != nil {
		t.Fatal(err)
	}
	if err := e.l.ChargeRead(e.ctx, e.viewer("bob"), limits.ReadGraph); err != nil {
		t.Fatal(err)
	}
	e.advance(12 * time.Hour)
	if err := e.l.ChargeRead(e.ctx, e.viewer("alice"), limits.ReadGraph); err != nil {
		t.Fatalf("next day: %v", err)
	}
}

func TestDefaults(t *testing.T) {
	p := limits.DefaultPolicy()
	for c, want := range map[string]int{"feed_page": 400, "tree_view": 400, "post_read": 1500, "graph_read": 200, "media_full": 60} {
		if p.Reads[c] != want {
			t.Errorf("%s = %d, want %d", c, p.Reads[c], want)
		}
	}
	want := map[string][]limits.Cap{
		"post": {{Window: limits.Hour, Max: 10}, {Window: limits.Day, Max: 30}}, "like": {{Window: limits.Hour, Max: 100}},
		"friend_request": {{Window: limits.Day, Max: 20}}, "report": {{Window: limits.Day, Max: 20}},
		"social": {{Window: limits.Day, Max: 200}}, "account": {{Window: limits.Day, Max: 50}},
		"invite_handshake": {{Window: limits.Day, Max: 10}},
	}
	for k, caps := range want {
		got := p.Writes[k]
		if len(got) != len(caps) {
			t.Fatalf("%s caps %v", k, got)
		}
		for i := range caps {
			if got[i] != caps[i] {
				t.Errorf("%s cap %d = %v, want %v", k, i, got[i], caps[i])
			}
		}
	}
	if p.Spacing != 2*time.Second || p.ProbationFriendRequests != 5 || p.Anon["login"] != 30 || p.Anon["csp_report"] != 30 {
		t.Fatalf("policy %+v", p)
	}
}

func TestWriteCapsHourAndDay(t *testing.T) {
	e := newEnv(t)
	v := e.viewer("alice")
	charge := func() error { return e.l.ChargeWrite(e.ctx, v, limits.WritePost, 1) }
	for day := 0; day < 3; day++ {
		for i := 0; i < 10; i++ {
			if err := charge(); err != nil {
				t.Fatalf("hour %d post %d: %v", day, i, err)
			}
		}
		if got := retryOf(t, charge()); got != time.Hour {
			t.Fatalf("hourly retry %v", got)
		}
		e.advance(time.Hour)
	}
	// 30 a day: the fourth hour is still refused, until the next UTC day.
	err := charge()
	if got := retryOf(t, err); got != 21*time.Hour {
		t.Fatalf("daily retry %v", got)
	}
	if n := e.count("alice", "w:post", limits.Day); n != 30 {
		t.Fatalf("daily count %d", n)
	}
	e.advance(21 * time.Hour)
	if err := charge(); err != nil {
		t.Fatalf("next day: %v", err)
	}
}

func TestMinimumSpacing(t *testing.T) {
	e := newEnv(t)
	p := e.l.Policy()
	if p.Spacing != 0 {
		t.Fatal("test servers start without spacing (frozen clock)")
	}
	p.Spacing = 2 * time.Second
	e.l.SetPolicy(p)
	v := e.viewer("alice")
	if err := e.l.ChargeWrite(e.ctx, v, limits.WriteLike, 1); err != nil {
		t.Fatal(err)
	}
	e.advance(500 * time.Millisecond)
	if got := retryOf(t, e.l.ChargeWrite(e.ctx, v, limits.WriteLike, 1)); got != 1500*time.Millisecond {
		t.Fatalf("spacing retry %v", got)
	}
	// A refused write does not move the clock of the kind, and is not counted.
	if n := e.count("alice", "w:like:h", limits.Hour); n != 1 {
		t.Fatalf("count %d", n)
	}
	// Other kinds and other people are independent.
	if err := e.l.ChargeWrite(e.ctx, v, limits.WriteSocial, 1); err != nil {
		t.Fatal(err)
	}
	if err := e.l.ChargeWrite(e.ctx, e.viewer("bob"), limits.WriteLike, 1); err != nil {
		t.Fatal(err)
	}
	e.advance(1500 * time.Millisecond)
	if err := e.l.ChargeWrite(e.ctx, v, limits.WriteLike, 1); err != nil {
		t.Fatalf("after 2 s: %v", err)
	}
}

// A step of a write already begun (a ceremony finish, a co-sign intent) is
// charged to the kind's caps but neither waits out nor restarts the spacing:
// adding a passkey sends /auth/passkeys/finish and /cosign back to back.
func TestStepSkipsSpacing(t *testing.T) {
	e := newEnv(t)
	p := e.l.Policy()
	p.Spacing = 2 * time.Second
	e.l.SetPolicy(p)
	ctx := e.as("alice")
	write := gate.Class{Kind: gate.ClassWrite, Name: string(limits.WriteAccount)}
	step := gate.Class{Kind: gate.ClassStep, Name: string(limits.WriteAccount)}
	if err := e.l.ChargeRequest(ctx, write); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := e.l.ChargeRequest(ctx, step); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}
	if n := e.count("alice", "w:account", limits.Day); n != 3 {
		t.Fatalf("count %d", n)
	}
	// The spacing clock still runs from the write, not the steps.
	e.advance(500 * time.Millisecond)
	if got := retryOf(t, e.l.ChargeRequest(ctx, write)); got != 1500*time.Millisecond {
		t.Fatalf("spacing retry %v", got)
	}
}

func TestProbationFriendRequests(t *testing.T) {
	e := newEnv(t)
	carol := e.w.User("carol", func(m *core.UserMeta) {
		u := e.d.Clock.Now().Add(72 * time.Hour)
		m.InvitedBy, m.Genesis, m.ProbationUntil = e.w.ID("alice"), false, &u
	})
	if _, err := e.d.DB.Q().Exec(e.ctx, "UPDATE users SET probation_until = $2 WHERE id = $1", int64(carol),
		e.d.Clock.Now().Add(72*time.Hour)); err != nil {
		t.Fatal(err)
	}
	e.a.Users.Invalidate(carol)
	v := core.Viewer{ID: carol, Kind: core.SessUser}
	for i := 0; i < 5; i++ {
		if err := e.l.ChargeWrite(e.ctx, v, limits.WriteFriendReq, 1); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}
	retryOf(t, e.l.ChargeWrite(e.ctx, v, limits.WriteFriendReq, 1))
	for i := 0; i < 6; i++ {
		if err := e.l.ChargeWrite(e.ctx, e.viewer("alice"), limits.WriteFriendReq, 1); err != nil {
			t.Fatalf("alice request %d: %v", i, err)
		}
	}
}

func TestChargeRequestClasses(t *testing.T) {
	e := newEnv(t)
	ctx := e.as("alice")
	for _, c := range []gate.Class{{Kind: gate.ClassRead, Name: "feed_page"}, {Kind: gate.ClassWrite, Name: "social"},
		{Kind: gate.ClassFree, Name: "free"}} {
		if err := e.l.ChargeRequest(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	if e.count("alice", "r:feed_page", limits.Day) != 1 || e.count("alice", "w:social", limits.Day) != 1 {
		t.Fatal("read and write classes are charged to the viewer")
	}
	var n int
	if err := e.d.DB.Q().QueryRow(e.ctx, "SELECT coalesce(sum(count), 0) FROM rate_counters").Scan(&n); err != nil || n != 2 {
		t.Fatalf("free is charged: %d %v", n, err)
	}
	// Without a signed-in session, reads and writes fall to the anonymous
	// bucket of the client network (nothing at rest).
	p := e.l.Policy()
	p.Anon[limits.UnauthBucket] = 2
	e.l.SetPolicy(p)
	guest := core.WithViewer(e.ctx, core.Viewer{Kind: core.SessGuest})
	for i := 0; i < 2; i++ {
		if err := e.l.ChargeRequest(guest, gate.Class{Kind: gate.ClassRead, Name: "graph_read"}); err != nil {
			t.Fatal(err)
		}
	}
	retryOf(t, e.l.ChargeRequest(e.ctx, gate.Class{Kind: gate.ClassWrite, Name: "post"}))
	if err := e.d.DB.Q().QueryRow(e.ctx, "SELECT coalesce(sum(count), 0) FROM rate_counters").Scan(&n); err != nil || n != 2 {
		t.Fatalf("anonymous charges reached the database: %d %v", n, err)
	}
}

func TestAnonPerPrefix(t *testing.T) {
	e := newEnv(t)
	a := httpx.ClientPrefix(netip.MustParseAddr("203.0.113.7"))
	same := httpx.ClientPrefix(netip.MustParseAddr("203.0.113.200"))
	other := httpx.ClientPrefix(netip.MustParseAddr("198.51.100.1"))
	v6 := httpx.ClientPrefix(netip.MustParseAddr("2001:db8:1:2:3::1"))
	v6same := httpx.ClientPrefix(netip.MustParseAddr("2001:db8:1:2:ff::9"))
	if a != same || v6 != v6same || a == other {
		t.Fatal("prefixes are /24 and /56")
	}
	e.advance(10 * time.Second)
	for i := 0; i < 30; i++ {
		if err := e.l.ChargeAnon(a, "login"); err != nil {
			t.Fatalf("login %d: %v", i, err)
		}
	}
	if got := retryOf(t, e.l.ChargeAnon(same, "login")); got != 50*time.Second {
		t.Fatalf("anon retry %v", got)
	}
	if err := e.l.ChargeAnon(other, "login"); err != nil {
		t.Fatal("another network has its own bucket")
	}
	if err := e.l.ChargeAnon(a, "enroll"); err != nil {
		t.Fatal("another bucket is independent")
	}
	e.advance(50 * time.Second)
	if err := e.l.ChargeAnon(a, "login"); err != nil {
		t.Fatalf("next minute: %v", err)
	}
	// Reset forgets every anonymous bucket.
	for i := 0; i < 40; i++ {
		_ = e.l.ChargeAnon(a, "csp_report")
	}
	e.l.Reset()
	if err := e.l.ChargeAnon(a, "csp_report"); err != nil {
		t.Fatal(err)
	}
}

func TestResetAndPurge(t *testing.T) {
	e := newEnv(t)
	p := e.l.Policy()
	p.Reads["tree_view"] = 1
	e.l.SetPolicy(p)
	v := e.viewer("alice")
	_ = e.l.ChargeRead(e.ctx, v, limits.ReadTreeView)
	retryOf(t, e.l.ChargeRead(e.ctx, v, limits.ReadTreeView))
	// A reset that truncates the table must not leave a cached count behind.
	if _, err := e.d.DB.Q().Exec(e.ctx, "TRUNCATE rate_counters"); err != nil {
		t.Fatal(err)
	}
	e.l.Reset()
	if err := e.l.ChargeRead(e.ctx, v, limits.ReadTreeView); err != nil {
		t.Fatalf("after reset: %v", err)
	}
	e.advance(3 * limits.Day)
	_ = e.l.ChargeRead(e.ctx, v, limits.ReadTreeView)
	if err := e.l.Purge(e.ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := e.d.DB.Q().QueryRow(e.ctx, "SELECT count(*) FROM rate_counters").Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows after purge %d %v", n, err)
	}
	// The purge job is registered and runs.
	if err := e.a.DrainJobs(e.ctx); err != nil {
		t.Fatal(err)
	}
}
