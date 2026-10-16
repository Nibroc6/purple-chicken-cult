package metrics

import (
	"context"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"
)

// The Phase 0 trial questions (ARCHITECTURE §18 DoD: "measure invites per
// day, feed depth, the mix of posts by degree, and preset switching") answered
// from metrics_daily by `pcc metrics`. Aggregates only, like everything here.

// Metric names read by the summary (names.go is the closed list).
const (
	nameInvites     = "invites.redeemed"
	nameTierReached = "feed.tier_reached"
	nameImpressions = "feed.impressions_by_degree"
	namePreset      = "feed.preset_switch"
	nameLatency     = "feed.latency_ms"
)

// DayCount is one day's value.
type DayCount struct {
	Day   string `json:"day"`
	Value int64  `json:"value"`
}

// Distribution is a count per bucket in the closed bucket order, with its total.
type Distribution struct {
	Buckets []string `json:"buckets"`
	Counts  []int64  `json:"counts"`
	Total   int64    `json:"total"`
}

// Share returns bucket i's share of the total in percent (0 when empty).
func (d Distribution) Share(i int) float64 {
	if d.Total == 0 || i < 0 || i >= len(d.Counts) {
		return 0
	}
	return 100 * float64(d.Counts[i]) / float64(d.Total)
}

// Summary answers the trial questions over a window of days.
type Summary struct {
	From string `json:"from"` // first day (UTC, inclusive)
	To   string `json:"to"`   // last day (UTC, inclusive)
	Days int    `json:"days"`

	InvitesPerDay []DayCount `json:"invites_per_day"` // every day of the window, zeros included
	InvitesTotal  int64      `json:"invites_total"`
	InvitesMean   float64    `json:"invites_mean"` // per day

	// FeedDepth counts feed sessions by the deepest tier they served.
	FeedDepth Distribution `json:"feed_depth"`
	// Impressions counts posts seen for the first time by the reader's
	// distance to the author when seen: the mix of posts by degree.
	Impressions Distribution `json:"impressions_by_degree"`
	// PresetSwitches counts feed sessions opened in a preset other than the
	// reader's previous session's, by the preset switched to.
	PresetSwitches Distribution `json:"preset_switches"`

	FeedP95PerDay []DayCount `json:"feed_p95_ms_per_day"` // days with feed opens only
}

// Summary reads the last days days of metrics_daily and answers the trial
// questions (`pcc metrics`).
func (s *Service) Summary(ctx context.Context, days int) (Summary, error) {
	if days < 1 {
		days = 1
	}
	rows, err := s.Report(ctx, days)
	if err != nil {
		return Summary{}, err
	}
	to := s.d.Clock.Now().UTC()
	return Summarize(rows, to.AddDate(0, 0, -(days-1)), to), nil
}

// Summarize builds the summary of rows (Report's output) for the days from
// from to to (UTC dates, inclusive). Rows outside the window are ignored.
func Summarize(rows []ReportRow, from, to time.Time) Summary {
	first, last := from.UTC().Format(time.DateOnly), to.UTC().Format(time.DateOnly)
	s := Summary{
		From:           first,
		To:             last,
		FeedDepth:      dist(degrees),
		Impressions:    dist(degrees),
		PresetSwitches: dist(names[namePreset]),
	}
	invites := map[string]int64{}
	p95 := map[string]int64{}
	for _, r := range rows {
		if r.Day < first || r.Day > last {
			continue
		}
		switch r.Name {
		case nameInvites:
			invites[r.Day] += r.Value
			s.InvitesTotal += r.Value
		case nameTierReached:
			s.FeedDepth.add(r.Bucket, r.Value)
		case nameImpressions:
			s.Impressions.add(r.Bucket, r.Value)
		case namePreset:
			s.PresetSwitches.add(r.Bucket, r.Value)
		case nameLatency + P95Suffix:
			p95[r.Day] = r.Value
		}
	}
	for d := from.UTC(); d.Format(time.DateOnly) <= last; d = d.AddDate(0, 0, 1) {
		day := d.Format(time.DateOnly)
		s.Days++
		s.InvitesPerDay = append(s.InvitesPerDay, DayCount{Day: day, Value: invites[day]})
		if v, ok := p95[day]; ok {
			s.FeedP95PerDay = append(s.FeedP95PerDay, DayCount{Day: day, Value: v})
		}
	}
	if s.Days > 0 {
		s.InvitesMean = float64(s.InvitesTotal) / float64(s.Days)
	}
	return s
}

func dist(buckets []string) Distribution {
	return Distribution{Buckets: append([]string(nil), buckets...), Counts: make([]int64, len(buckets))}
}

func (d *Distribution) add(bucket string, n int64) {
	for i, b := range d.Buckets {
		if b == bucket {
			d.Counts[i] += n
			d.Total += n
			return
		}
	}
}

// WriteText prints the summary as plain tables.
func (s Summary) WriteText(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) } //nolint:forbidigo // CLI text into a builder, not a response body
	flush := func() error {
		if _, err := io.WriteString(tw, b.String()); err != nil {
			return err
		}
		b.Reset()
		return tw.Flush()
	}

	p("trial metrics %s .. %s (%d days, UTC)\n\n", s.From, s.To, s.Days)
	if err := flush(); err != nil {
		return err
	}

	p("invites redeemed: %d total, %.2f per day\n", s.InvitesTotal, s.InvitesMean)
	for _, d := range s.InvitesPerDay {
		p("\t%s\t%d\t\n", d.Day, d.Value)
	}
	p("\n")
	if err := flush(); err != nil {
		return err
	}

	for _, sec := range []struct {
		title, unit string
		d           Distribution
	}{
		{"feed depth (deepest tier a feed session reached)", "sessions", s.FeedDepth},
		{"impressions by degree (posts seen, by distance to the author)", "impressions", s.Impressions},
		{"preset switches (sessions opened in a different preset)", "switches", s.PresetSwitches},
	} {
		p("%s: %d %s\n", sec.title, sec.d.Total, sec.unit)
		p("\t%s\t", "")
		for _, bk := range sec.d.Buckets {
			p("%s\t", bk)
		}
		p("\n\tcount\t")
		for _, c := range sec.d.Counts {
			p("%d\t", c)
		}
		p("\n\tshare %%\t")
		for i := range sec.d.Counts {
			p("%.1f\t", sec.d.Share(i))
		}
		p("\n\n")
		if err := flush(); err != nil {
			return err
		}
	}

	p("feed open p95 (ms, upper bucket bound; 0 = above 5000):")
	if len(s.FeedP95PerDay) == 0 {
		p(" no feed opens\n")
	} else {
		p("\n")
		for _, d := range s.FeedP95PerDay {
			p("\t%s\t%d\t\n", d.Day, d.Value)
		}
	}
	return flush()
}
