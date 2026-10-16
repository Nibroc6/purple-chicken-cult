// Package metrics records aggregate product metrics only (ARCHITECTURE §13, §18
// DoD): names and buckets from a closed list, numbers — never IDs, refs, edges or
// IPs. It implements core.Metrics; job metrics.flush writes metrics_daily.
//
// Owner: P0-W10.
//
// Counters live in memory per (UTC day, name, bucket) and are added to
// metrics_daily by metrics.flush every minute. The API takes only a name, a
// bucket and a number, so nothing else can ever be recorded; a name or bucket
// outside the closed list (names.go) is a bug: it panics in dev and test and is
// dropped in prod. Latencies are counted into fixed buckets (LatencyBuckets);
// Report derives the daily p95 from them.
package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// JobFlush is the periodic job that adds the in-memory counters to metrics_daily.
const JobFlush = "metrics.flush"

// FlushEvery is the metrics.flush period.
const FlushEvery = time.Minute

// P95Suffix names the derived rows Report adds for latency metrics
// (e.g. "feed.latency_ms.p95": the upper bound of the bucket holding the 95th
// percentile of that day, in ms; 0 means "above the last bucket").
const P95Suffix = ".p95"

// key is one counter.
type key struct {
	day    string // YYYY-MM-DD (UTC)
	name   string
	bucket string
}

// Service is the metrics module.
type Service struct {
	d *core.Deps

	mu     sync.Mutex
	counts map[key]int64
}

var _ core.Metrics = (*Service)(nil)

// New returns the service.
func New(d *core.Deps) *Service { return &Service{d: d, counts: map[key]int64{}} }

// Init registers metrics.flush (every minute).
func (s *Service) Init(context.Context) error {
	if s.d.Jobs != nil {
		s.d.Jobs.Handle(JobFlush, func(ctx context.Context, _ json.RawMessage) error { return s.Flush(ctx) })
		s.d.Jobs.Every(JobFlush, FlushEvery)
	}
	return nil
}

// Reset forgets the counters not flushed yet (test hooks: the database was wiped).
func (s *Service) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counts = map[key]int64{}
}

// strict reports whether an out-of-list name or bucket panics (dev and test).
func (s *Service) strict() bool { return s.d.Cfg == nil || s.d.Cfg.DevLike() }

func (s *Service) reject(name, bucket string) {
	if s.strict() {
		panic(fmt.Errorf("metrics: %s{%s} is not in the closed list", name, bucket))
	}
}

func (s *Service) day() string {
	now := time.Now()
	if s.d.Clock != nil {
		now = s.d.Clock.Now()
	}
	return now.UTC().Format(time.DateOnly)
}

// Inc adds n to (name, bucket). Names and buckets outside the closed list are
// rejected (panic in dev/test, dropped in prod); n ≤ 0 is ignored.
func (s *Service) Inc(name, bucket string, n int64) {
	if !Allowed(name, bucket) {
		s.reject(name, bucket)
		return
	}
	if n <= 0 {
		return
	}
	k := key{day: s.day(), name: name, bucket: bucket}
	s.mu.Lock()
	s.counts[k] += n
	s.mu.Unlock()
}

// ObserveMs counts one latency observation into its fixed bucket. Only names
// whose buckets are the latency set are accepted.
func (s *Service) ObserveMs(name string, ms int64) {
	if !isLatency(name) {
		s.reject(name, "")
		return
	}
	s.Inc(name, LatencyBucket(ms), 1)
}

// Pending returns the in-memory (not yet flushed) value of (name, bucket) today.
func (s *Service) Pending(name, bucket string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.counts[key{day: s.day(), name: name, bucket: bucket}]
}

// Flush adds the in-memory counters to metrics_daily (metrics.flush). On a
// database error the counters are put back, so nothing is lost or counted twice.
func (s *Service) Flush(ctx context.Context) error {
	s.mu.Lock()
	batch := s.counts
	s.counts = map[key]int64{}
	s.mu.Unlock()
	if len(batch) == 0 {
		return nil
	}
	err := s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		for k, v := range batch {
			if _, err := tx.Exec(ctx, `INSERT INTO metrics_daily (day, name, bucket, value) VALUES ($1::date, $2, $3, $4)
				ON CONFLICT (day, name, bucket) DO UPDATE SET value = metrics_daily.value + EXCLUDED.value`,
				k.day, k.name, k.bucket, v); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		s.mu.Lock()
		for k, v := range batch {
			s.counts[k] += v
		}
		s.mu.Unlock()
	}
	return err
}

// ReportRow is one aggregated metric row.
type ReportRow struct {
	Day    string `json:"day"`
	Name   string `json:"name"`
	Bucket string `json:"bucket"`
	Value  int64  `json:"value"`
}

// Report returns the metrics_daily rows of the last days days (`pcc metrics`),
// plus one derived "<name>.p95" row per day and latency metric.
func (s *Service) Report(ctx context.Context, days int) ([]ReportRow, error) {
	if days < 1 {
		days = 1
	}
	since := s.d.Clock.Now().UTC().AddDate(0, 0, -(days - 1)).Format(time.DateOnly)
	rows, err := s.d.DB.Q().Query(ctx, `SELECT to_char(day, 'YYYY-MM-DD'), name, bucket, value FROM metrics_daily
		WHERE day >= $1::date ORDER BY day, name, bucket`, since)
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return withP95(out), nil
}

// withP95 appends the derived p95 rows after each day's rows.
func withP95(in []ReportRow) []ReportRow {
	type dn struct{ day, name string }
	hist := map[dn]map[string]int64{}
	for _, r := range in {
		if !isLatency(r.Name) {
			continue
		}
		k := dn{r.Day, r.Name}
		if hist[k] == nil {
			hist[k] = map[string]int64{}
		}
		hist[k][r.Bucket] += r.Value
	}
	out := append([]ReportRow(nil), in...)
	for k, h := range hist {
		out = append(out, ReportRow{Day: k.day, Name: k.name + P95Suffix, Value: P95(h)})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Day != out[j].Day {
			return out[i].Day < out[j].Day
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Bucket < out[j].Bucket
	})
	return out
}

// P95 returns the upper bound (ms) of the latency bucket that holds the 95th
// percentile of hist (bucket label → count); 0 when it is the open last bucket
// or hist is empty.
func P95(hist map[string]int64) int64 {
	var total int64
	for _, n := range hist {
		total += n
	}
	if total == 0 {
		return 0
	}
	need := (total*95 + 99) / 100 // ceil(0.95·total)
	var seen int64
	for _, b := range LatencyBuckets {
		seen += hist[b.Label]
		if seen >= need {
			return b.UpTo
		}
	}
	return 0
}
