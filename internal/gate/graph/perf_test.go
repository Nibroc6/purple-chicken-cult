//go:build perf

package graph

import (
	"context"
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// Perf-tagged (go test -tags perf): Phase 0 budget is a full BFS < 5 ms p95 on
// the synthetic 10k graph (plan §7). Not part of unit acceptance.

func synth10k(b testing.TB) *Graph {
	b.Helper()
	g := New(&core.Deps{})
	g.LoadAdjacency(SynthAdjacency(Synth(10_000, "ba", 1), 1, t0))
	return g
}

func BenchmarkSearch10k(b *testing.B) {
	g := synth10k(b)
	ctx := context.Background()
	rng := rand.New(rand.NewPCG(3, 4)) //nolint:gosec // benchmark data
	b.ReportAllocs()
	for b.Loop() {
		v := core.UserID(1 + rng.IntN(10_000))
		if _, err := g.Search(ctx, v, g.Exclusions(v)); err != nil {
			b.Fatal(err)
		}
	}
}

func TestSearch10kP95(t *testing.T) {
	g := synth10k(t)
	ctx := context.Background()
	rng := rand.New(rand.NewPCG(5, 6)) //nolint:gosec // perf data
	ds := make([]time.Duration, 0, 200)
	for range 200 {
		v := core.UserID(1 + rng.IntN(10_000))
		start := time.Now()
		if _, err := g.Search(ctx, v, g.Exclusions(v)); err != nil {
			t.Fatal(err)
		}
		ds = append(ds, time.Since(start))
	}
	slices.Sort(ds)
	p95 := ds[len(ds)*95/100]
	t.Logf("BFS 10k p95 = %v", p95)
	if p95 > 5*time.Millisecond {
		t.Fatalf("BFS 10k p95 = %v, budget 5ms", p95)
	}
}
