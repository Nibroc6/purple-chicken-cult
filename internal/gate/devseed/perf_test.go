//go:build perf

package devseed

import (
	"context"
	"slices"
	"testing"
	"time"
)

// Perf-tagged (go test -tags perf): the Phase 0 budget for loading the synthetic
// 10k graph (edges, blocks, hide_via) from Postgres into the engine is < 200 ms
// (plan §7). Not part of unit acceptance.

func seeded10k(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	if err := e.s.Synthetic(context.Background(), 10_000, "ba", 1); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestGraphLoad10k(t *testing.T) {
	e := seeded10k(t)
	ctx := context.Background()
	ds := make([]time.Duration, 0, 20)
	for range 20 {
		start := time.Now()
		if err := e.g.Load(ctx); err != nil {
			t.Fatal(err)
		}
		ds = append(ds, time.Since(start))
	}
	slices.Sort(ds)
	p95 := ds[len(ds)*95/100]
	t.Logf("graph load 10k p95 = %v (degree of user 1: %d)", p95, e.g.Degree(1))
	if p95 > 200*time.Millisecond {
		t.Fatalf("graph load 10k p95 = %v, budget 200ms", p95)
	}
}

func TestSynthetic10kSeed(t *testing.T) {
	start := time.Now()
	e := seeded10k(t)
	t.Logf("Synthetic(10000, ba) took %v", time.Since(start))
	if got := e.count(t, "SELECT count(*) FROM users"); got != 10_000 {
		t.Fatalf("users: %d", got)
	}
}
