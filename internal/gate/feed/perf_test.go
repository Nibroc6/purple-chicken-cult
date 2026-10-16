//go:build perf

package feed_test

import (
	"context"
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/RoaringBitmap/roaring/v2"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/feed"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/graph"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire/wiretest"
)

// TestPerfFeed10k is the Phase 0 DoD perf test (plan §7 "Synthetic 10k graph",
// ARCHITECTURE §18): graph.Synth(10000, "ba", seed) seeded through devseed with
// seven days of posts. For 200 random readers: tiers 3–6 equal the reference
// BFS, the planner picks the cheaper scan for every tier it plans (costs
// recomputed independently from the posts table), and p95 of the first page of
// a cold session is under 50 ms (in-memory post index; Postgres only for
// seen/hides).
//
// Run: PCC_REQUIRE_DB=1 go test -tags perf -run TestPerf ./internal/gate/feed/
func TestPerfFeed10k(t *testing.T) {
	const (
		n       = 10_000
		seed    = 7
		readers = 200
		budget  = 50 * time.Millisecond
	)
	a := wiretest.NewApp(t)
	d := a.Deps()
	ctx := context.Background()
	start := time.Now()
	if err := a.DevSeed.Synthetic(ctx, n, "ba", seed); err != nil {
		t.Fatal(err)
	}
	var posts int
	if err := d.DB.Q().QueryRow(ctx, "SELECT count(*) FROM posts").Scan(&posts); err != nil {
		t.Fatal(err)
	}
	t.Logf("seeded %d users, %d posts in %v", n, posts, time.Since(start))

	// Independent cost inputs: window posts per author from the database.
	now := d.Clock.Now()
	perAuthor := map[core.UserID]int{}
	window := 0
	rows, err := d.DB.Q().Query(ctx, `SELECT author_id, count(*) FROM posts
		WHERE deleted_at IS NULL AND created_at >= $1 GROUP BY author_id`, now.Add(-d.Cfg.FeedWindow))
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id int64
		var c int
		if err := rows.Scan(&id, &c); err != nil {
			t.Fatal(err)
		}
		perAuthor[core.UserID(id)] = c
		window += c
	}
	rows.Close()

	// Reference distances over the same graph (devseed numbers users from 1).
	adj := graph.SynthAdjacency(graph.Synth(n, "ba", seed), 1, now)
	noEx := core.Exclusions{Blocked: roaring.New(), HiddenVia: roaring.New()}

	var cur core.UserID
	var sr core.Search
	planned, wrong := 0, 0
	plannedTiers := map[int]int{}
	a.Feed.SetPlanHook(func(k int, scan feed.Scan) {
		layer := sr.Layer(k)
		pk := 0
		for _, u := range layer {
			pk += perAuthor[u]
		}
		author, timeCost := len(layer)+pk, window
		want := feed.ScanAuthor
		if author > timeCost {
			want = feed.ScanTime
		}
		planned++
		plannedTiers[k]++
		if scan != want {
			wrong++
			t.Errorf("reader %d tier %d: planner chose %s (author cost %d, time cost %d)", cur, k, scan, author, timeCost)
		}
	})

	rng := rand.New(rand.NewPCG(seed, 99)) //nolint:gosec // perf data
	ds := make([]time.Duration, 0, readers)
	tiersChecked := 0
	for range readers {
		cur = core.UserID(1 + rng.IntN(n))
		vctx := core.WithViewer(ctx, core.Viewer{ID: cur, Kind: core.SessUser, AuthAt: now})
		if sr, err = a.Visibility.SearchFor(ctx, cur); err != nil {
			t.Fatal(err)
		}
		ref := graph.ReferenceBFS(adj, cur, noEx)
		for k := 3; k <= 6; k++ {
			var want []core.UserID
			for u, dist := range ref {
				if dist == k {
					want = append(want, u)
				}
			}
			slices.Sort(want)
			got := slices.Clone(sr.Layer(k))
			slices.Sort(got)
			if !slices.Equal(got, want) {
				t.Fatalf("reader %d tier %d: engine %d users, reference %d", cur, k, len(got), len(want))
			}
			tiersChecked++
		}
		begin := time.Now()
		if _, err := a.Feed.Page(vctx, "", ""); err != nil {
			t.Fatal(err)
		}
		ds = append(ds, time.Since(begin))
	}
	// Deeper tiers: follow a few sessions far enough that the planner sees them.
	for i := range 10 {
		cur = core.UserID(1 + i*997)
		vctx := core.WithViewer(ctx, core.Viewer{ID: cur, Kind: core.SessUser, AuthAt: now})
		if sr, err = a.Visibility.SearchFor(ctx, cur); err != nil {
			t.Fatal(err)
		}
		cursor := ""
		for range 400 {
			fp, err := a.Feed.Page(vctx, "", cursor)
			if err != nil {
				t.Fatal(err)
			}
			if cursor = fp.Next; cursor == "" || plannedTiers[4] > i {
				break
			}
		}
	}
	slices.Sort(ds)
	p50, p95 := ds[len(ds)/2], ds[len(ds)*95/100]
	t.Logf("feed first page (cold session) over %d readers: p50 %v, p95 %v (budget %v); %d tiers 3–6 match the reference BFS; planner %d choices by tier %v, %d wrong",
		readers, p50, p95, budget, tiersChecked, planned, plannedTiers, wrong)
	if p95 > budget {
		t.Fatalf("feed first page p95 = %v, budget %v", p95, budget)
	}
	if planned == 0 {
		t.Fatal("the planner never ran")
	}
}
