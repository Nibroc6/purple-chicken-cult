package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/metrics"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire/wiretest"
)

// `pcc metrics` prints the trial summary (invites per day, feed depth,
// impressions by degree, preset switches), its JSON form, and the raw rows.
func TestPrintMetrics(t *testing.T) {
	a := wiretest.NewApp(t)
	ctx := context.Background()
	a.Metrics.Inc("invites.redeemed", "", 2)
	a.Metrics.Inc("feed.tier_reached", "4", 1)
	a.Metrics.Inc("feed.impressions_by_degree", "2", 3)
	a.Metrics.Inc("feed.preset_switch", "balanced", 1)
	if err := a.Metrics.Flush(ctx); err != nil {
		t.Fatal(err)
	}

	var text strings.Builder
	if err := printMetrics(ctx, &text, a.Metrics, 14, true, false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"(14 days, UTC)",
		"invites redeemed: 2 total, 0.14 per day",
		"feed depth (deepest tier a feed session reached): 1 sessions",
		"impressions by degree (posts seen, by distance to the author): 3 impressions",
		"preset switches (sessions opened in a different preset): 1 switches",
		"feed.impressions_by_degree       2          3", // --raw
	} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, text.String())
		}
	}

	var js strings.Builder
	if err := printMetrics(ctx, &js, a.Metrics, 7, false, true); err != nil {
		t.Fatal(err)
	}
	var s metrics.Summary
	if err := json.Unmarshal([]byte(js.String()), &s); err != nil {
		t.Fatalf("%v: %s", err, js.String())
	}
	if s.Days != 7 || s.InvitesTotal != 2 || s.FeedDepth.Counts[3] != 1 || s.Impressions.Counts[1] != 3 || s.PresetSwitches.Counts[1] != 1 {
		t.Fatalf("json summary %+v", s)
	}
	if strings.Contains(js.String(), "feed.impressions_by_degree") {
		t.Fatal("raw rows printed without --raw")
	}
}
