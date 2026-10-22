// Package automation scores automation signals (ARCHITECTURE §17.2(4)–(6)):
// client reports (navigator.webdriver, a DevTools-protocol hint and
// input-event COUNTS per minute — never content, never mouse movement) and
// request patterns seen by an apix-level hook (sustained page rate without
// idle minutes or round-the-clock activity, feed reads without seen events,
// breadth-first tree walks, chain and people expansion at machine speed).
//
// No signal blocks anyone by itself, and signals never ban: when the
// session's combined score reaches Threshold, the session must step up with a
// passkey (core.StepUp ⇒ gate.ErrStepUp on every request until a human taps
// the passkey); repeated step-ups go to moderators (gate/auth).
//
// Accessibility first (§17.2(6)). Input events of any kind count as input
// (keys included, so keyboard-only and screen-reader use is input), there is
// no mouse-movement signal, and every request pattern must persist across
// several minutes of the server clock at rates no person reading the app
// reaches: a burst inside one minute is never a pattern. The browser flags
// (webdriver, cdp) together weigh less than Threshold, so an automated but
// human-paced browser (assistive tech, test runners) never steps up.
//
// Every finding is an automation_signals row (aggregates only) carrying
// net_h = HMAC(daily salt, the client's /24 or /56) — never an address —
// which gate/signals/behavior correlates across accounts. Rows are purged
// after 30 days by signals.purge (P1-W06).
//
// Owner: P1-W07.
package automation

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/httpx"
)

// Signal kinds written by this package (automation_signals.kind).
const (
	KindWebdriver     = "webdriver"
	KindCDP           = "cdp"
	KindNoInput       = "no_input"
	KindSustainedRate = "sustained_rate"
	KindNoSeenReads   = "no_seen_reads"
	KindBFSTreeWalk   = "bfs_tree_walk"
	KindChainSpeed    = "chain_speed"
)

// Weights of each kind in the combined score; a session steps up at Threshold.
// webdriver and cdp share one "browser" weight (at most BrowserWeight).
const (
	Threshold     = 1.0
	BrowserWeight = 0.5
)

// Weights of the other kinds.
var Weights = map[string]float64{
	KindNoInput: 0.5, KindSustainedRate: 0.6, KindNoSeenReads: 0.4, KindBFSTreeWalk: 0.5, KindChainSpeed: 0.5,
}

// Pattern thresholds (server clock).
const (
	// SustainMinutes consecutive minutes with at least SustainReads reads each:
	// no idle minute in between (a person reading eight feed pages a minute for
	// ten minutes straight does not exist).
	SustainMinutes = 10
	SustainReads   = 8
	// RoundTheClockHours distinct hours with reads among the last 24.
	RoundTheClockHours = 20
	// NoSeenFeedPages feed pages in the last NoSeenWindow, spread over at least
	// NoSeenMinutes distinct minutes, without a single seen event.
	NoSeenFeedPages = 30
	NoSeenMinutes   = 5
	NoSeenWindow    = time.Hour
	// TreeWalkPerMin tree requests in each of two consecutive minutes.
	TreeWalkPerMin = 20
	// ChainPerMin people/chain expansions in each of two consecutive minutes.
	ChainPerMin = 30
	// NoInputMinutes reported visible minutes with zero input events while the
	// server saw reads in at least as many distinct minutes (NoInputWindow).
	NoInputMinutes = 5
	NoInputWindow  = 30 * time.Minute
	// ClientFresh is how long a client report's browser flags count.
	ClientFresh = 30 * time.Minute
	// RowCooldown: at most one row per session and kind in this time.
	RowCooldown = time.Hour
	// Keep is how long a session's window is kept after its last request.
	Keep = 24 * time.Hour
)

// MaxReport bounds a client report.
const (
	MaxReportMinutes = 60
	MaxInputsPerMin  = 100_000
)

// minute is one server-clock minute of a session.
type minute struct{ reads, feed, seen, tree, chain int }

// report is one client report.
type report struct {
	at              time.Time
	minutes, inputs int
}

// window is the scoring state of one session (memory only).
type window struct {
	user      core.UserID
	minutes   map[int64]*minute // unix minute → counts
	reports   []report
	flagsAt   time.Time // last report with webdriver or cdp
	webdriver bool
	cdp       bool
	rowAt     map[string]time.Time // kind → last row written
	last      time.Time
}

// Service is the automation-signals module.
type Service struct {
	d *core.Deps

	mu   sync.Mutex
	sess map[[32]byte]*window
	ops  int
}

// New returns the service.
func New(d *core.Deps) *Service { return &Service{d: d, sess: map[[32]byte]*window{}} }

// Init does nothing (the windows live in memory).
func (s *Service) Init(context.Context) error { return nil }

// Reset forgets every in-memory scoring window (test hooks; contract request p1-w07-1).
func (s *Service) Reset() {
	s.mu.Lock()
	s.sess = map[[32]byte]*window{}
	s.mu.Unlock()
}

// ClientSignals is POST /api/signals (counts only, never content).
type ClientSignals struct {
	Webdriver    bool  `json:"webdriver"`
	CDP          bool  `json:"cdp"`
	InputsPerMin []int `json:"inputs_per_min"`
	VisibleMs    int64 `json:"visible_ms"`
}

// signedIn is the signed-in viewer, also while a step-up is pending.
func signedIn(ctx context.Context) (core.Viewer, bool) {
	v, ok := core.ViewerFrom(ctx)
	return v, ok && v.Kind == core.SessUser && v.ID != 0
}

// Client records a client signal report of the caller's session. Anyone else
// gets the uniform not-found; a malformed report is Problem bad_request.
func (s *Service) Client(ctx context.Context, r ClientSignals) error {
	v, ok := signedIn(ctx)
	if !ok {
		return gate.ErrNotFound
	}
	if len(r.InputsPerMin) > MaxReportMinutes || r.VisibleMs < 0 || r.VisibleMs > int64(MaxReportMinutes)*60_000 {
		return gate.NewProblem(gate.StatusBadRequest, "bad_request")
	}
	inputs := 0
	for _, n := range r.InputsPerMin {
		if n < 0 || n > MaxInputsPerMin {
			return gate.NewProblem(gate.StatusBadRequest, "bad_request")
		}
		inputs += n
	}
	now := s.d.Clock.Now()
	s.mu.Lock()
	w := s.windowLocked(v, now)
	if mins := len(r.InputsPerMin); mins > 0 {
		w.reports = append(w.reports, report{at: now, minutes: mins, inputs: inputs})
	}
	var rows []row
	if r.Webdriver || r.CDP {
		w.flagsAt, w.webdriver, w.cdp = now, r.Webdriver, r.CDP
		if r.Webdriver {
			rows = append(rows, w.rowLocked(KindWebdriver, BrowserWeight, nil, now)...)
		}
		if r.CDP {
			rows = append(rows, w.rowLocked(KindCDP, BrowserWeight, nil, now)...)
		}
	}
	s.mu.Unlock()
	return s.evaluate(ctx, v, now, rows)
}

// row is a signal row to write.
type row struct {
	kind  string
	score float64
	data  map[string]int
}

// rowLocked returns the row for kind unless one was written within RowCooldown.
func (w *window) rowLocked(kind string, score float64, data map[string]int, now time.Time) []row {
	if at, ok := w.rowAt[kind]; ok && now.Sub(at) < RowCooldown {
		return nil
	}
	w.rowAt[kind] = now
	return []row{{kind: kind, score: score, data: data}}
}

func (s *Service) windowLocked(v core.Viewer, now time.Time) *window {
	w := s.sess[v.Session]
	if w == nil || w.user != v.ID {
		w = &window{user: v.ID, minutes: map[int64]*minute{}, rowAt: map[string]time.Time{}}
		s.sess[v.Session] = w
	}
	w.last = now
	s.ops++
	if s.ops%1024 == 0 {
		for k, x := range s.sess {
			if now.Sub(x.last) > Keep {
				delete(s.sess, k)
			}
		}
	}
	return w
}

// class is what a request counts as.
type class struct{ read, feed, seen, tree, chain bool }

// classify maps a request (method and chi route pattern) to its counters.
// Polls (/feed/new), the session, the event stream and writes are not reads.
func classify(method, pattern string) class {
	if method == http.MethodPost && pattern == "/api/seen" {
		return class{seen: true}
	}
	if method != http.MethodGet || !strings.HasPrefix(pattern, "/api/") {
		return class{}
	}
	p := strings.TrimPrefix(pattern, "/api")
	switch {
	case p == "/feed/new", p == "/session", p == "/events", strings.HasPrefix(p, "/push/"), strings.HasPrefix(p, "/_test/"):
		return class{}
	case p == "/feed", p == "/feed/expand", p == "/seen/recent", p == "/saves":
		return class{read: true, feed: true}
	case strings.HasPrefix(p, "/tree/"):
		return class{read: true, tree: true}
	case p == "/people/{ref}", p == "/people/{ref}/posts", p == "/posts/{ref}/why", p == "/suggestions",
		p == "/friends", p == "/posts/{ref}/likes":
		return class{read: true, chain: true}
	}
	return class{read: true}
}

// Middleware is the apix-level hook (installed on /api by the router): after
// each request of a signed-in session it counts the request in the session's
// window and evaluates the score. Nothing here changes the response.
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		v, ok := signedIn(r.Context())
		if !ok || v.StepUp {
			return
		}
		pattern := ""
		if rc := chi.RouteContext(r.Context()); rc != nil {
			pattern = rc.RoutePattern()
		}
		c := classify(r.Method, pattern)
		if c == (class{}) {
			return
		}
		s.Observe(r.Context(), v, c.read, c.feed, c.seen, c.tree, c.chain)
	})
}

// Observe counts one request of v's session and evaluates the score (the
// Middleware's body; tests drive it directly).
func (s *Service) Observe(ctx context.Context, v core.Viewer, read, feed, seen, tree, chain bool) {
	now := s.d.Clock.Now()
	m := now.Unix() / 60
	s.mu.Lock()
	w := s.windowLocked(v, now)
	cur := w.minutes[m]
	if cur == nil {
		cur = &minute{}
		w.minutes[m] = cur
		for k := range w.minutes {
			if m-k > 24*60 {
				delete(w.minutes, k)
			}
		}
	}
	if read {
		cur.reads++
	}
	if feed {
		cur.feed++
	}
	if seen {
		cur.seen++
	}
	if tree {
		cur.tree++
	}
	if chain {
		cur.chain++
	}
	s.mu.Unlock()
	if err := s.evaluate(ctx, v, now, nil); err != nil && s.d.Log != nil {
		s.d.Log.Warn("automation: evaluate", "err", err)
	}
}

// patternsLocked returns the request-pattern and input findings of w at now.
func (w *window) patternsLocked(now time.Time) map[string]map[string]int {
	m := now.Unix() / 60
	at := func(k int64) minute {
		if x := w.minutes[k]; x != nil {
			return *x
		}
		return minute{}
	}
	out := map[string]map[string]int{}
	// Sustained rate: SustainMinutes straight minutes, each busy.
	sustained := true
	total := 0
	for k := m - SustainMinutes + 1; k <= m; k++ {
		if at(k).reads < SustainReads {
			sustained = false
			break
		}
		total += at(k).reads
	}
	if sustained {
		out[KindSustainedRate] = map[string]int{"minutes": SustainMinutes, "reads": total}
	}
	// Round the clock: reads in most hours of the last day.
	hours := map[int64]bool{}
	for k, x := range w.minutes {
		if m-k < 24*60 && x.reads > 0 {
			hours[k/60] = true
		}
	}
	if len(hours) >= RoundTheClockHours {
		out[KindSustainedRate] = map[string]int{"hours": len(hours)}
	}
	// Feed reads without seen events.
	feed, seen, feedMinutes := 0, 0, 0
	readMinutes := 0
	for k, x := range w.minutes {
		if m-k < int64(NoSeenWindow/time.Minute) {
			feed += x.feed
			seen += x.seen
			if x.feed > 0 {
				feedMinutes++
			}
		}
		if m-k < int64(NoInputWindow/time.Minute) && x.reads > 0 {
			readMinutes++
		}
	}
	if feed >= NoSeenFeedPages && seen == 0 && feedMinutes >= NoSeenMinutes {
		out[KindNoSeenReads] = map[string]int{"feed_pages": feed, "minutes": feedMinutes}
	}
	// Breadth-first tree walks and chain expansion at machine speed: two
	// consecutive minutes.
	if at(m).tree >= TreeWalkPerMin && at(m-1).tree >= TreeWalkPerMin {
		out[KindBFSTreeWalk] = map[string]int{"per_min": min(at(m).tree, at(m-1).tree)}
	}
	if at(m).chain >= ChainPerMin && at(m-1).chain >= ChainPerMin {
		out[KindChainSpeed] = map[string]int{"per_min": min(at(m).chain, at(m-1).chain)}
	}
	// No input behind sustained activity.
	mins, inputs := 0, 0
	for _, r := range w.reports {
		if now.Sub(r.at) < NoInputWindow {
			mins += r.minutes
			inputs += r.inputs
		}
	}
	if mins >= NoInputMinutes && inputs == 0 && readMinutes >= NoInputMinutes {
		out[KindNoInput] = map[string]int{"minutes": mins}
	}
	return out
}

// evaluate writes new findings (and extra rows) and steps the session up when
// the combined score reaches Threshold.
func (s *Service) evaluate(ctx context.Context, v core.Viewer, now time.Time, rows []row) error {
	s.mu.Lock()
	w := s.sess[v.Session]
	if w == nil {
		s.mu.Unlock()
		return s.write(ctx, v, now, rows)
	}
	found := w.patternsLocked(now)
	score := 0.0
	kinds := make([]string, 0, len(found)+2)
	for kind, data := range found {
		score += Weights[kind]
		kinds = append(kinds, kind)
		rows = append(rows, w.rowLocked(kind, Weights[kind], data, now)...)
	}
	if now.Sub(w.flagsAt) < ClientFresh && (w.webdriver || w.cdp) {
		score += BrowserWeight
		if w.webdriver {
			kinds = append(kinds, KindWebdriver)
		}
		if w.cdp {
			kinds = append(kinds, KindCDP)
		}
	}
	trigger := score >= Threshold-1e-9 && len(found) > 0
	if trigger {
		// A fresh start once the person stepped up: the same minutes never
		// count twice.
		w.minutes, w.reports, w.flagsAt = map[int64]*minute{}, nil, time.Time{}
	}
	s.mu.Unlock()
	if err := s.write(ctx, v, now, rows); err != nil {
		return err
	}
	if !trigger || s.d.StepUp == nil {
		return nil
	}
	slices.Sort(kinds)
	return s.d.StepUp.Require(ctx, v.ID, v.Session, strings.Join(kinds, ","))
}

// write inserts signal rows with the request's network hash.
func (s *Service) write(ctx context.Context, v core.Viewer, now time.Time, rows []row) error {
	if len(rows) == 0 {
		return nil
	}
	net := s.netHash(ctx, now)
	for _, r := range rows {
		data := []byte("{}")
		if len(r.data) > 0 {
			var err error
			if data, err = json.Marshal(r.data); err != nil {
				return err
			}
		}
		if _, err := s.d.DB.Q().Exec(ctx, `INSERT INTO automation_signals (user_id, session_hash, kind, score, data, net_h, at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`, int64(v.ID), v.Session[:], r.kind, r.score, data, net, now); err != nil {
			return err
		}
	}
	return nil
}

// netHash is HMAC(daily salt, the client's /24 or /56): equal for one network
// on one day, never reversible to an address (nil without a client address).
func (s *Service) netHash(ctx context.Context, now time.Time) []byte {
	a, ok := httpx.ClientIPFrom(ctx)
	if !ok || s.d.Cfg == nil {
		return nil
	}
	p := httpx.ClientPrefix(a)
	b, err := p.MarshalBinary()
	if err != nil {
		return nil
	}
	m := hmac.New(sha256.New, s.d.Cfg.Key("pcc/net-salt/v1/"+now.UTC().Format(time.DateOnly), 32))
	m.Write(b)
	return m.Sum(nil)[:16]
}
