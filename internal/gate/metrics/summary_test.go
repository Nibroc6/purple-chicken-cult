package metrics_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/metrics"
)

func TestSummarizeAnswersTheTrialQuestions(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rows := []metrics.ReportRow{
		{Day: "2025-12-31", Name: "invites.redeemed", Value: 99}, // before the window
		{Day: "2026-01-01", Name: "invites.redeemed", Value: 2},
		{Day: "2026-01-03", Name: "invites.redeemed", Value: 4},
		{Day: "2026-01-01", Name: "feed.tier_reached", Bucket: "1", Value: 3},
		{Day: "2026-01-02", Name: "feed.tier_reached", Bucket: "3", Value: 1},
		{Day: "2026-01-02", Name: "feed.impressions_by_degree", Bucket: "1", Value: 6},
		{Day: "2026-01-02", Name: "feed.impressions_by_degree", Bucket: "2", Value: 3},
		{Day: "2026-01-03", Name: "feed.impressions_by_degree", Bucket: "7+", Value: 1},
		{Day: "2026-01-03", Name: "feed.preset_switch", Bucket: "latest", Value: 2},
		{Day: "2026-01-03", Name: "feed.preset_switch", Bucket: "balanced", Value: 1},
		{Day: "2026-01-02", Name: "feed.latency_ms.p95", Value: 25},
		{Day: "2026-01-02", Name: "tree.opened", Value: 5},
	}
	s := metrics.Summarize(rows, from, from.AddDate(0, 0, 2))
	if s.From != "2026-01-01" || s.To != "2026-01-03" || s.Days != 3 {
		t.Fatalf("window %+v", s)
	}
	if s.InvitesTotal != 6 || s.InvitesMean != 2 || len(s.InvitesPerDay) != 3 || s.InvitesPerDay[1] != (metrics.DayCount{Day: "2026-01-02"}) {
		t.Fatalf("invites %+v", s)
	}
	if s.FeedDepth.Total != 4 || s.FeedDepth.Counts[0] != 3 || s.FeedDepth.Counts[2] != 1 || s.FeedDepth.Share(0) != 75 {
		t.Fatalf("depth %+v", s.FeedDepth)
	}
	if s.Impressions.Total != 10 || s.Impressions.Counts[6] != 1 || s.Impressions.Share(1) != 30 {
		t.Fatalf("impressions %+v", s.Impressions)
	}
	if strings.Join(s.PresetSwitches.Buckets, ",") != "latest,balanced,top" || s.PresetSwitches.Counts[0] != 2 || s.PresetSwitches.Counts[1] != 1 {
		t.Fatalf("presets %+v", s.PresetSwitches)
	}
	if len(s.FeedP95PerDay) != 1 || s.FeedP95PerDay[0] != (metrics.DayCount{Day: "2026-01-02", Value: 25}) {
		t.Fatalf("p95 %+v", s.FeedP95PerDay)
	}
	if (metrics.Distribution{}).Share(0) != 0 {
		t.Fatal("share of nothing")
	}
}

// The service path `pcc metrics` uses: counters recorded through the closed
// API, flushed to metrics_daily, read back as the summary and printed.
func TestSummaryFromRecordedCounters(t *testing.T) {
	a, m := newApp(t)
	ctx := context.Background()
	m.Inc("invites.redeemed", "", 3)
	m.Inc("feed.tier_reached", "2", 1)
	m.Inc("feed.impressions_by_degree", "1", 4)
	m.ObserveMs("feed.latency_ms", 30)
	if err := m.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.Hooks().AdvanceClock(24 * time.Hour); err != nil {
		t.Fatal(err)
	}
	m.Inc("invites.redeemed", "", 1)
	m.Inc("feed.preset_switch", "latest", 1)
	m.Inc("feed.impressions_by_degree", "3", 1)
	if err := m.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	m.Inc("feed.impressions_by_degree", "3", 1) // not flushed: not in the report yet

	s, err := m.Summary(ctx, 3)
	if err != nil {
		t.Fatal(err)
	}
	if s.Days != 3 || s.To != "2026-01-02" || s.InvitesTotal != 4 || s.InvitesPerDay[0].Value != 0 || s.InvitesPerDay[1].Value != 3 {
		t.Fatalf("summary %+v", s)
	}
	if s.FeedDepth.Counts[1] != 1 || s.Impressions.Total != 5 || s.PresetSwitches.Counts[0] != 1 {
		t.Fatalf("summary %+v", s)
	}
	if len(s.FeedP95PerDay) != 1 || s.FeedP95PerDay[0].Value != 50 {
		t.Fatalf("p95 %+v", s.FeedP95PerDay)
	}

}
