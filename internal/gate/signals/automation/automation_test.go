package automation_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/signals/automation"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire/wiretest"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/httpx"
)

type env struct {
	t   *testing.T
	a   *wire.App
	d   *core.Deps
	s   *automation.Service
	ctx context.Context
}

func newEnv(t *testing.T) *env {
	t.Helper()
	a := wiretest.NewApp(t)
	return &env{t: t, a: a, d: a.Deps(), s: a.Automation, ctx: context.Background()}
}

// session is one signed-in browser of handle, from addr.
type session struct {
	e      *env
	cookie string
	addr   string
}

func (e *env) session(handle, addr string) *session {
	e.t.Helper()
	tok, _ := wiretest.Login(e.t, e.a, handle)
	return &session{e: e, cookie: tok, addr: addr}
}

// ctx is the request context the real middleware chain builds (RealIP, auth).
func (s *session) ctx() context.Context {
	req := httptest.NewRequest(http.MethodGet, "/api/feed", nil)
	req.RemoteAddr = s.addr + ":5555"
	req.AddCookie(&http.Cookie{Name: httpx.CookieName(false), Value: s.cookie}) //nolint:gosec // test request
	var got context.Context
	h := httpx.RealIP(nil)(s.e.a.Auth.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = r.Context() })))
	h.ServeHTTP(httptest.NewRecorder(), req)
	return got
}

func (s *session) viewer() core.Viewer {
	v, _ := core.ViewerFrom(s.ctx())
	return v
}

func (s *session) steppedUp() bool { return s.viewer().StepUp }

// read is one request of the given counters.
func (s *session) read(feed, tree, chain bool) {
	v := s.viewer()
	if v.StepUp {
		return
	}
	s.e.s.Observe(s.ctx(), v, true, feed, false, tree, chain)
}

func (s *session) seen() { s.e.s.Observe(s.ctx(), s.viewer(), false, false, true, false, false) }

func (s *session) report(r automation.ClientSignals) {
	s.e.t.Helper()
	if err := s.e.s.Client(s.ctx(), r); err != nil {
		s.e.t.Fatal(err)
	}
}

// sustain reads SustainReads times a minute for SustainMinutes straight
// minutes (the clock ends in the last of them).
func (s *session) sustain(seen bool) {
	for m := range automation.SustainMinutes {
		if m > 0 {
			s.e.minute()
		}
		for range automation.SustainReads {
			s.read(true, false, false)
			if seen {
				s.seen()
			}
		}
	}
}

func (e *env) minute() { e.must(e.a.Hooks().AdvanceClock(time.Minute)) }

func (e *env) must(err error) {
	e.t.Helper()
	if err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	e.must(e.d.DB.Q().QueryRow(e.ctx, sql, args...).Scan(&n))
	return n
}

// webdriver + a sustained page rate without idle minutes ⇒ step-up; the rows
// carry the network hash and no address.
func TestWebdriverAndSustainedRateStepUp(t *testing.T) {
	e := newEnv(t)
	s := e.session("bot", "203.0.113.7")
	s.report(automation.ClientSignals{Webdriver: true, InputsPerMin: []int{30}, VisibleMs: 60_000})
	s.sustain(true)
	if !s.steppedUp() {
		t.Fatal("webdriver + sustained rate did not step up")
	}
	if _, err := core.RequireUser(s.ctx()); !errors.Is(err, gate.ErrStepUp) {
		t.Fatalf("reads after the trigger: %v", err)
	}
	v := s.viewer()
	if n := e.count("SELECT count(*) FROM stepups WHERE user_id = $1 AND reason = 'sustained_rate,webdriver'", int64(v.ID)); n != 1 {
		t.Fatalf("%d step-ups", n)
	}
	for _, kind := range []string{automation.KindWebdriver, automation.KindSustainedRate} {
		if n := e.count("SELECT count(*) FROM automation_signals WHERE user_id = $1 AND kind = $2 AND length(net_h) = 16",
			int64(v.ID), kind); n != 1 {
			t.Fatalf("%d %s rows with net_h", n, kind)
		}
	}
	// net_h is the same for one /24 on one day and never the address itself.
	other := e.session("bot2", "203.0.113.200")
	other.report(automation.ClientSignals{Webdriver: true})
	if n := e.count(`SELECT count(DISTINCT net_h) FROM automation_signals WHERE kind = 'webdriver'`); n != 1 {
		t.Fatalf("one /24 gave %d hashes", n)
	}
	if n := e.count(`SELECT count(*) FROM automation_signals WHERE data::text LIKE '%203.0.113%' OR position('\xcb007107'::bytea in net_h) > 0`); n != 0 {
		t.Fatal("an address reached the signal rows")
	}
}

// Keyboard-only and screen-reader use never steps up: input events of any
// kind count, human pace never forms a pattern — even from an automated
// browser (webdriver and cdp both set) reading for an hour.
func TestAssistiveProfilesNeverStepUp(t *testing.T) {
	e := newEnv(t)
	kb := e.session("keyboard", "198.51.100.1")
	sr := e.session("reader", "198.51.100.2")
	for m := range 60 {
		// Keyboard: tab through the feed, open trees and people at reading pace.
		kb.report(automation.ClientSignals{Webdriver: true, CDP: true, InputsPerMin: []int{40}, VisibleMs: 60_000})
		for range 4 {
			kb.read(true, false, false)
		}
		kb.read(false, true, false)
		kb.read(false, false, true)
		kb.seen()
		// Screen reader: arrow keys through every item, few page loads.
		sr.report(automation.ClientSignals{Webdriver: true, InputsPerMin: []int{120}, VisibleMs: 60_000})
		for range 3 {
			sr.read(true, false, false)
		}
		if m%2 == 0 {
			sr.seen()
		}
		e.minute()
	}
	if kb.steppedUp() || sr.steppedUp() {
		t.Fatal("an assistive profile stepped up")
	}
	if n := e.count("SELECT count(*) FROM stepups"); n != 0 {
		t.Fatalf("%d step-ups", n)
	}
}

// Everything inside one minute is a burst, not a pattern: a frozen-clock test
// run (or a person opening twenty tabs) never steps up.
func TestBurstsInOneMinuteNeverStepUp(t *testing.T) {
	e := newEnv(t)
	s := e.session("burst", "192.0.2.1")
	s.report(automation.ClientSignals{Webdriver: true, CDP: true, InputsPerMin: []int{0, 0, 0, 0, 0, 0}, VisibleMs: 360_000})
	for range 500 {
		s.read(true, true, true)
	}
	if s.steppedUp() {
		t.Fatal("a one-minute burst stepped up")
	}
}

func TestMachineSpeedChainExpansionStepsUpWithBrowserFlags(t *testing.T) {
	e := newEnv(t)
	s := e.session("chains", "192.0.2.9")
	plain := e.session("plain", "192.0.2.10") // the same pattern without a browser flag
	s.report(automation.ClientSignals{CDP: true})
	for m := range 2 {
		if m > 0 {
			e.minute()
		}
		for range automation.ChainPerMin {
			s.read(false, false, true)
			plain.read(false, false, true)
		}
	}
	if !s.steppedUp() {
		t.Fatal("cdp + chain expansion at machine speed did not step up")
	}
	if plain.steppedUp() {
		t.Fatal("one signal alone stepped up")
	}
}

// A bot that reports nothing: sustained rate + feed reads without seen events.
func TestSilentBotStepsUp(t *testing.T) {
	e := newEnv(t)
	s := e.session("silent", "192.0.2.20")
	s.sustain(false)
	if !s.steppedUp() {
		t.Fatal("sustained rate + no seen did not step up")
	}
}

// After the person stepped up, the same minutes never count twice.
func TestStepUpStartsAFreshWindow(t *testing.T) {
	e := newEnv(t)
	s := e.session("again", "192.0.2.30")
	s.sustain(false)
	if !s.steppedUp() {
		t.Fatal("no step-up")
	}
	// Satisfy it directly (the passkey ceremony is gate/auth's test).
	v := s.viewer()
	_, err := e.d.DB.Q().Exec(e.ctx, "UPDATE sessions SET stepup_required = false WHERE id_hash = $1", v.Session[:])
	e.must(err)
	s.read(true, false, false)
	if s.steppedUp() {
		t.Fatal("stepped up again from the old window")
	}
}

// no_input: reported visible minutes without any input behind reads in as many
// minutes, plus webdriver.
func TestNoInputWithWebdriverStepsUp(t *testing.T) {
	e := newEnv(t)
	s := e.session("idle", "192.0.2.40")
	for m := range automation.NoInputMinutes {
		s.read(true, false, false)
		s.seen()
		s.report(automation.ClientSignals{Webdriver: true, InputsPerMin: []int{0}, VisibleMs: 60_000})
		if m < automation.NoInputMinutes-1 {
			e.minute()
		}
	}
	if !s.steppedUp() {
		t.Fatal("webdriver + no input did not step up")
	}
}

func TestClientReportValidation(t *testing.T) {
	e := newEnv(t)
	s := e.session("validate", "192.0.2.50")
	for _, bad := range []automation.ClientSignals{
		{InputsPerMin: make([]int, automation.MaxReportMinutes+1)},
		{InputsPerMin: []int{-1}},
		{InputsPerMin: []int{automation.MaxInputsPerMin + 1}},
		{VisibleMs: -1},
	} {
		var p *gate.Problem
		if err := e.s.Client(s.ctx(), bad); !errors.As(err, &p) || p.Code != "bad_request" {
			t.Fatalf("%+v: %v", bad, err)
		}
	}
	if err := e.s.Client(context.Background(), automation.ClientSignals{}); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("anonymous report: %v", err)
	}
	// One row per kind and session per hour.
	for range 5 {
		s.report(automation.ClientSignals{Webdriver: true})
	}
	if n := e.count("SELECT count(*) FROM automation_signals WHERE kind = 'webdriver'"); n != 1 {
		t.Fatalf("%d webdriver rows", n)
	}
	e.s.Reset()
}

func TestMiddlewareClassifiesRoutes(t *testing.T) {
	cases := map[string][5]bool{ // read feed seen tree chain
		"GET /api/feed":            {true, true, false, false, false},
		"GET /api/feed/new":        {},
		"GET /api/tree/{ref}":      {true, false, false, true, false},
		"GET /api/people/{ref}":    {true, false, false, false, true},
		"GET /api/posts/{ref}/why": {true, false, false, false, true},
		"GET /api/posts/{ref}":     {true, false, false, false, false},
		"POST /api/seen":           {false, false, true, false, false},
		"POST /api/posts":          {},
		"GET /api/session":         {},
		"GET /api/events":          {},
		"GET /api/notifications":   {true, false, false, false, false},
	}
	for route, want := range cases {
		c := automation.ClassifyForTest(route)
		if c != want {
			t.Errorf("%s: %v, want %v", route, c, want)
		}
	}
}
