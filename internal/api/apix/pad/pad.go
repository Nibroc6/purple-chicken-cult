// Package pad is the P1 timing-padding middleware (fidelity #8, security #19):
// it holds a padded route's whole response — charge, handler, Problem, 404
// and 429 alike — until the next point of the route's grid, so found and
// not-found (and every other outcome) are indistinguishable by latency.
//
// The padded set is derived, not hand-picked (plan §5.1): For reports every
// route whose pattern has a {ref}/{hs}/{id}/{snapshot} parameter, every route
// whose request carries a ref (RequestRefs; pad_test checks that list against
// the OpenAPI fragments), and the deep-tier feed and tree routes, which use
// the Deep grid. apix.Route installs Wrap on exactly these.
//
// Grids. Default: every multiple of 150 ms. Deep: 600 ms, then every 600 ms
// plus 150 ms (750, 1350, …). The graph's lazy search already releases the
// deep tiers at a whole multiple of 600 ms (p1-w03-1); with plain 600 ms
// multiples here a deep page would pay one more whole bucket on top of that
// release. The 150 ms slack lets the released search plus ordinary handler
// work land on the next point instead, so the two never double-pad, while
// every response still takes one of a fixed set of durations.
//
// Every padded response is counted in a per-route histogram of grid points by
// status (Stats), which tests use for the server-side "found and not-found
// land in the same bucket" assertion; padtest has the KS helpers.
//
// Owner: P1-W07.
package pad

import (
	"bytes"
	"maps"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Buckets (plan §5.1).
const (
	DefaultBucket = 150 * time.Millisecond
	DeepBucket    = 600 * time.Millisecond
)

// Params are the path parameters that make a route padded.
var Params = []string{"{ref}", "{hs}", "{id}", "{snapshot}"}

// Grid is the set of durations a padded response can take: Bucket, then
// Bucket + Slack, then every further Bucket ((i−1)·Bucket + Slack for the
// i-th point, i ≥ 2). Slack == Bucket gives the plain multiples of Bucket.
type Grid struct {
	Bucket time.Duration
	Slack  time.Duration
}

// The two grids.
var (
	Default = Grid{Bucket: DefaultBucket, Slack: DefaultBucket}
	Deep    = Grid{Bucket: DeepBucket, Slack: DefaultBucket}
)

// Round returns the grid point a response that took d is held until: the
// smallest point ≥ d.
func (g Grid) Round(d time.Duration) time.Duration {
	return g.Point(g.Index(d))
}

// Index is the number of the grid point Round(d) (1 = the first).
func (g Grid) Index(d time.Duration) int {
	if g.Bucket <= 0 || d <= g.Bucket {
		return 1
	}
	k := (d - g.Slack + g.Bucket - 1) / g.Bucket
	return max(int(k)+1, 2)
}

// Point is the i-th grid point (i ≥ 1).
func (g Grid) Point(i int) time.Duration {
	if i <= 1 {
		return g.Bucket
	}
	return time.Duration(i-1)*g.Bucket + g.Slack
}

// RequestRefs are the routes without a ref path parameter whose request
// carries a ref (body field, query token or handshake payload). pad_test
// derives the same set from api/openapi and fails on any difference.
var RequestRefs = map[string]bool{
	"POST /posts":                true, // parents
	"POST /posts/thread":         true, // parents
	"POST /seen":                 true, // posts
	"POST /reports":              true, // target.ref
	"POST /notifications/read":   true, // up_to
	"POST /cosign":               true, // target
	"POST /enroll/begin":         true, // the enrollment token
	"POST /mod/recovery":         true, // subject
	"POST /mod/quarantine":       true, // subject_item
	"POST /auth/passkeys/finish": true, // intent
	"GET /feed/expand":           true, // token
	"GET /feed/new":              true, // session
	"POST /join/claim":           true, // payload
	"POST /join/dual/frame":      true, // payload
	"POST /recovery/claim":       true, // payload
	"POST /recovery/sessions":    true, // friend
}

// deep reports whether pattern is a deep-tier feed or tree route.
func deep(pattern string) bool {
	return pattern == "/feed" || strings.HasPrefix(pattern, "/feed/") || strings.HasPrefix(pattern, "/tree/")
}

// For reports whether method+pattern (relative to /api) is padded and on
// which grid.
func For(method, pattern string) (Grid, bool) {
	if deep(pattern) {
		return Deep, true
	}
	if RequestRefs[method+" "+pattern] {
		return Default, true
	}
	for _, p := range Params {
		if strings.Contains(pattern, p) {
			return Default, true
		}
	}
	return Grid{}, false
}

// Timing is how Wrap measures and waits (tests replace it).
type Timing struct {
	Now   func() time.Time
	Sleep func(r *http.Request, d time.Duration)
}

// sleepCtx waits d or until the client goes away.
func sleepCtx(r *http.Request, d time.Duration) {
	if d <= 0 {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-r.Context().Done():
	}
}

var (
	timingMu sync.RWMutex
	timing   = Timing{Now: time.Now, Sleep: sleepCtx}
)

// SetTiming replaces the clock and the wait of every Wrap (tests) and returns
// a function that restores the previous ones.
func SetTiming(t Timing) (restore func()) {
	timingMu.Lock()
	old := timing
	if t.Now == nil {
		t.Now = time.Now
	}
	if t.Sleep == nil {
		t.Sleep = sleepCtx
	}
	timing = t
	timingMu.Unlock()
	return func() {
		timingMu.Lock()
		timing = old
		timingMu.Unlock()
	}
}

func current() Timing {
	timingMu.RLock()
	defer timingMu.RUnlock()
	return timing
}

// buffer holds a response until its grid point.
type buffer struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (b *buffer) Header() http.Header { return b.header }

func (b *buffer) WriteHeader(code int) {
	if b.status == 0 {
		b.status = code
	}
}

func (b *buffer) Write(p []byte) (int, error) {
	if b.status == 0 {
		b.status = http.StatusOK
	}
	return b.body.Write(p)
}

// Wrap pads the route named route (for Stats) to grid g.
func Wrap(route string, g Grid) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tm := current()
			start := tm.Now()
			buf := &buffer{header: http.Header{}}
			next.ServeHTTP(buf, r)
			elapsed := tm.Now().Sub(start)
			i := g.Index(elapsed)
			tm.Sleep(r, g.Point(i)-elapsed)
			if buf.status == 0 {
				buf.status = http.StatusOK
			}
			h := w.Header()
			for k, v := range buf.header {
				h[k] = v
			}
			w.WriteHeader(buf.status)
			_, _ = w.Write(buf.body.Bytes())
			record(route, buf.status, i)
		})
	}
}

// Middleware pads every response of a route to the grid of bucket (Deep for
// DeepBucket, else the multiples of bucket).
func Middleware(bucket time.Duration) func(http.Handler) http.Handler {
	if bucket == DeepBucket {
		return Wrap("", Deep)
	}
	return Wrap("", Grid{Bucket: bucket, Slack: bucket})
}

// ---- histogram ----

var (
	statsMu sync.Mutex
	stats   = map[string]map[int]map[int]int{} // route → status → grid point index → count
)

func record(route string, status, i int) {
	if route == "" {
		return
	}
	statsMu.Lock()
	defer statsMu.Unlock()
	byStatus := stats[route]
	if byStatus == nil {
		byStatus = map[int]map[int]int{}
		stats[route] = byStatus
	}
	if byStatus[status] == nil {
		byStatus[status] = map[int]int{}
	}
	byStatus[status][i]++
}

// Stats returns a copy of the histogram of padded responses: route
// ("METHOD /pattern") → HTTP status → grid point index → count.
func Stats() map[string]map[int]map[int]int {
	statsMu.Lock()
	defer statsMu.Unlock()
	out := make(map[string]map[int]map[int]int, len(stats))
	for r, byStatus := range stats {
		cp := make(map[int]map[int]int, len(byStatus))
		for st, h := range byStatus {
			cp[st] = maps.Clone(h)
		}
		out[r] = cp
	}
	return out
}

// ResetStats empties the histogram.
func ResetStats() {
	statsMu.Lock()
	stats = map[string]map[int]map[int]int{}
	statsMu.Unlock()
}
