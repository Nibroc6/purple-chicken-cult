// Package metrics records aggregate product metrics only (ARCHITECTURE §13, §18
// DoD): names and buckets from a closed list, numbers — never IDs, refs, edges or
// IPs. It implements core.Metrics; job metrics.flush writes metrics_daily.
//
// Owner: P0-W10. Scaffold stub: in-memory counters (coreref.MemMetrics), Report
// reads metrics_daily.
package metrics

import (
	"context"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coreref"
)

// Names is the closed metric list (name → allowed buckets; nil = any bucket from
// the fixed latency bucket set).
var Names = map[string][]string{
	"feed.tier_reached":          {"1", "2", "3", "4", "5", "6", "7+"},
	"feed.impressions_by_degree": {"1", "2", "3", "4", "5", "6", "7+"},
	"invites.redeemed":           {""},
	"feed.preset_switch":         {"latest", "balanced", "top"},
	"feed.latency_ms":            nil,
	"feed.planner":               {"author", "time"},
	"graph.deep_search":          {"count"},
	"tree.opened":                {""},
}

// Service is the metrics module.
type Service struct {
	d   *core.Deps
	ref *coreref.MemMetrics
}

var _ core.Metrics = (*Service)(nil)

// New returns the service.
func New(d *core.Deps) *Service { return &Service{d: d, ref: &coreref.MemMetrics{}} }

// Init registers metrics.flush in W10.
func (s *Service) Init(context.Context) error { return nil }

// Inc adds n to (name, bucket); names outside the closed list are dropped.
func (s *Service) Inc(name, bucket string, n int64) {
	if _, ok := Names[name]; !ok {
		return
	}
	s.ref.Inc(name, bucket, n)
}

// ObserveMs records a latency observation.
func (s *Service) ObserveMs(name string, ms int64) {
	if _, ok := Names[name]; !ok {
		return
	}
	s.ref.ObserveMs(name, ms)
}

// ReportRow is one aggregated metric row.
type ReportRow struct {
	Day    string `json:"day"`
	Name   string `json:"name"`
	Bucket string `json:"bucket"`
	Value  int64  `json:"value"`
}

// Report returns metrics_daily rows of the last days days (`pcc metrics`).
func (s *Service) Report(ctx context.Context, days int) ([]ReportRow, error) {
	since := s.d.Clock.Now().AddDate(0, 0, -days).Truncate(24 * time.Hour)
	rows, err := s.d.DB.Q().Query(ctx, `SELECT to_char(day, 'YYYY-MM-DD'), name, bucket, value FROM metrics_daily
		WHERE day >= $1 ORDER BY day, name, bucket`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReportRow
	for rows.Next() {
		var r ReportRow
		if err := rows.Scan(&r.Day, &r.Name, &r.Bucket, &r.Value); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
