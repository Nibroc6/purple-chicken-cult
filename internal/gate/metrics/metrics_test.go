package metrics_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/metrics"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire/wiretest"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/config"
)

func newApp(t *testing.T) (*wire.App, *metrics.Service) {
	t.Helper()
	a := wiretest.NewApp(t)
	return a, a.Metrics
}

func panics(fn func()) (p bool) {
	defer func() { p = recover() != nil }()
	fn()
	return false
}

func TestClosedListRejectsEverythingElse(t *testing.T) {
	_, m := newApp(t)
	bad := []func(){
		func() { m.Inc("users.signed_up", "", 1) },
		func() { m.Inc("feed.tier_reached", "0", 1) },
		func() { m.Inc("feed.tier_reached", "8", 1) },
		func() { m.Inc("feed.planner", "alice", 1) },          // a handle is not a bucket
		func() { m.Inc("invites.redeemed", "3000000001", 1) }, // nor an ID
		func() { m.Inc("feed.latency_ms", "17", 1) },          // only the fixed latency buckets
		func() { m.ObserveMs("tree.opened", 5) },              // not a latency metric
		func() { m.ObserveMs("feed.latency", 5) },
	}
	for i, fn := range bad {
		if !panics(fn) {
			t.Fatalf("case %d accepted in test", i)
		}
	}
	for n, bs := range metrics.Names() {
		for _, b := range bs {
			if panics(func() { m.Inc(n, b, 1) }) {
				t.Fatalf("%s{%s} rejected", n, b)
			}
		}
	}
}

func TestProdDropsUnknownNames(t *testing.T) {
	a, _ := newApp(t)
	d := *a.Deps()
	cfg := *d.Cfg
	cfg.Env = config.EnvProd
	d.Cfg = &cfg
	m := metrics.New(&d)
	if panics(func() { m.Inc("users.signed_up", "", 1) }) || panics(func() { m.ObserveMs("nope", 1) }) {
		t.Fatal("prod panicked")
	}
	if m.Pending("users.signed_up", "") != 0 {
		t.Fatal("unknown name recorded")
	}
	m.Inc("tree.opened", "", 2)
	if m.Pending("tree.opened", "") != 2 {
		t.Fatal("known name dropped")
	}
}

func TestFlushWritesOnlyAggregates(t *testing.T) {
	a, m := newApp(t)
	ctx := context.Background()
	m.Inc("feed.tier_reached", "3", 2)
	m.Inc("feed.tier_reached", "3", 1)
	m.Inc("invites.redeemed", "", 1)
	m.Inc("tree.opened", "", 0) // nothing
	for _, ms := range []int64{3, 4, 40, 60, 70, 900, 9000} {
		m.ObserveMs("feed.latency_ms", ms)
	}
	if err := a.DrainJobs(ctx); err != nil { // metrics.flush
		t.Fatal(err)
	}
	if m.Pending("feed.tier_reached", "3") != 0 {
		t.Fatal("not flushed")
	}
	m.Inc("feed.tier_reached", "3", 4)
	if err := m.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	// The table has exactly (day, name, bucket, value).
	rows, err := a.Deps().DB.Q().Query(ctx, `SELECT column_name FROM information_schema.columns
		WHERE table_name = 'metrics_daily' ORDER BY ordinal_position`)
	if err != nil {
		t.Fatal(err)
	}
	var cols []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatal(err)
		}
		cols = append(cols, c)
	}
	rows.Close()
	if strings.Join(cols, ",") != "day,name,bucket,value" {
		t.Fatalf("columns %v", cols)
	}
	rep, err := m.Report(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	names := metrics.Names()
	for _, r := range rep {
		got[r.Name+"{"+r.Bucket+"}"] = r.Value
		base := strings.TrimSuffix(r.Name, metrics.P95Suffix)
		if r.Day != "2026-01-01" || !slices.Contains(names[base], r.Bucket) && r.Name == base {
			t.Fatalf("row %+v", r)
		}
	}
	want := map[string]int64{
		"feed.tier_reached{3}":     7,
		"invites.redeemed{}":       1,
		"feed.latency_ms{le_5}":    2,
		"feed.latency_ms{le_50}":   1,
		"feed.latency_ms{le_100}":  2,
		"feed.latency_ms{le_1000}": 1,
		"feed.latency_ms{gt_5000}": 1,
		"feed.latency_ms.p95{}":    0, // the 95th percentile is in the open bucket
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s = %d, want %d (%v)", k, got[k], v, got)
		}
	}
	if _, ok := got["tree.opened{}"]; ok {
		t.Fatal("zero increment stored")
	}
}

func TestDaysAndP95(t *testing.T) {
	a, m := newApp(t)
	ctx := context.Background()
	for i := 0; i < 19; i++ {
		m.ObserveMs("feed.latency_ms", 20)
	}
	m.ObserveMs("feed.latency_ms", 400)
	if err := m.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.Hooks().AdvanceClock(24 * time.Hour); err != nil {
		t.Fatal(err)
	}
	m.Inc("tree.opened", "", 1)
	if err := m.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	rep, err := m.Report(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	var p95 []metrics.ReportRow
	days := map[string]bool{}
	for _, r := range rep {
		days[r.Day] = true
		if r.Name == "feed.latency_ms"+metrics.P95Suffix {
			p95 = append(p95, r)
		}
	}
	if len(days) != 2 || len(p95) != 1 || p95[0].Value != 25 || p95[0].Day != "2026-01-01" {
		t.Fatalf("report %+v", rep)
	}
	one, err := m.Report(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 1 || one[0].Name != "tree.opened" || one[0].Day != "2026-01-02" {
		t.Fatalf("one day: %+v", one)
	}
	if metrics.P95(map[string]int64{}) != 0 || metrics.P95(map[string]int64{"le_10": 1}) != 10 {
		t.Fatal("P95")
	}
	if metrics.LatencyBucket(0) != "le_5" || metrics.LatencyBucket(5) != "le_5" || metrics.LatencyBucket(6) != "le_10" ||
		metrics.LatencyBucket(1e9) != "gt_5000" {
		t.Fatal("LatencyBucket")
	}
}

func TestResetForgetsPending(t *testing.T) {
	_, m := newApp(t)
	m.Inc("tree.opened", "", 1)
	m.Reset()
	if m.Pending("tree.opened", "") != 0 {
		t.Fatal("reset kept counters")
	}
}

var _ core.Metrics = (*metrics.Service)(nil)
