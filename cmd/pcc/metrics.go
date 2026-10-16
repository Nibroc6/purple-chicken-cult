package main

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/metrics"
)

// writeSummary prints the trial summary as plain tables (operator CLI text
// lives here, not in gate: plan A9).
func writeSummary(w io.Writer, s metrics.Summary) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
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
		d           metrics.Distribution
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
