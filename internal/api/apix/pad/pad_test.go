package pad_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/Nibroc6/purple-chicken-cult/internal/api/apitest"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/apix"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/apix/pad"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/apix/pad/padtest"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/limits"
)

const ms = time.Millisecond

func TestGridPoints(t *testing.T) {
	cases := []struct {
		g    pad.Grid
		d    time.Duration
		want time.Duration
	}{
		{pad.Default, 0, 150 * ms}, {pad.Default, 3 * ms, 150 * ms}, {pad.Default, 150 * ms, 150 * ms},
		{pad.Default, 151 * ms, 300 * ms}, {pad.Default, 300 * ms, 300 * ms}, {pad.Default, 301 * ms, 450 * ms},
		{pad.Deep, 5 * ms, 600 * ms}, {pad.Deep, 600 * ms, 600 * ms},
		// A deep search released at 600 ms plus ordinary handler work: the next
		// point, not a whole second bucket (no double padding, p1-w03-1).
		{pad.Deep, 640 * ms, 750 * ms}, {pad.Deep, 750 * ms, 750 * ms},
		{pad.Deep, 751 * ms, 1350 * ms}, {pad.Deep, 1240 * ms, 1350 * ms}, {pad.Deep, 1351 * ms, 1950 * ms},
	}
	for _, c := range cases {
		if got := c.g.Round(c.d); got != c.want {
			t.Errorf("%+v.Round(%v) = %v, want %v", c.g, c.d, got, c.want)
		}
		if got := c.g.Point(c.g.Index(c.d)); got != c.want {
			t.Errorf("Point(Index(%v)) = %v", c.d, got)
		}
	}
}

func TestForDerivesThePaddedSet(t *testing.T) {
	cases := map[string]struct {
		padded bool
		grid   pad.Grid
	}{
		"GET /posts/{ref}":                   {true, pad.Default},
		"DELETE /auth/passkeys/{id}":         {true, pad.Default},
		"GET /invites/handshakes/{hs}":       {true, pad.Default},
		"POST /posts":                        {true, pad.Default},
		"POST /seen":                         {true, pad.Default},
		"POST /auth/passkeys/finish":         {true, pad.Default},
		"GET /feed":                          {true, pad.Deep},
		"GET /feed/new":                      {true, pad.Deep},
		"GET /tree/{ref}":                    {true, pad.Deep},
		"GET /tree/s/{snapshot}/more":        {true, pad.Deep},
		"GET /session":                       {false, pad.Grid{}},
		"POST /signals":                      {false, pad.Grid{}},
		"GET /friends":                       {false, pad.Grid{}},
		"POST /auth/dbsc/refresh":            {false, pad.Grid{}},
		"GET /notifications":                 {false, pad.Grid{}},
		"POST /notifications/read":           {true, pad.Default},
		"POST /invites/handshakes/{hs}/epub": {true, pad.Default},
	}
	for route, want := range cases {
		m, p, _ := strings.Cut(route, " ")
		g, ok := pad.For(m, p)
		if ok != want.padded || g != want.grid {
			t.Errorf("For(%s) = %+v %v, want %+v %v", route, g, ok, want.grid, want.padded)
		}
	}
}

// refNames are request fields that carry a ref, token or handshake payload.
var refNames = map[string]bool{"ref": true, "refs": true, "target": true, "parents": true, "posts": true, "child": true,
	"subject": true, "subject_item": true, "intent": true, "up_to": true, "friend": true, "token": true,
	"session": true, "payload": true}

func carriesRef(s *openapi3.SchemaRef, seen map[*openapi3.Schema]bool) bool {
	if s == nil {
		return false
	}
	if strings.HasSuffix(s.Ref, "/schemas/Ref") {
		return true
	}
	v := s.Value
	if v == nil || seen[v] {
		return false
	}
	seen[v] = true
	for name, p := range v.Properties {
		if refNames[name] || carriesRef(p, seen) {
			return true
		}
	}
	if carriesRef(v.Items, seen) {
		return true
	}
	for _, list := range []openapi3.SchemaRefs{v.AllOf, v.OneOf, v.AnyOf} {
		for _, x := range list {
			if carriesRef(x, seen) {
				return true
			}
		}
	}
	return false
}

// TestRequestRefsMatchOpenAPI derives "the request carries a ref" from the
// OpenAPI fragments: every such operation is padded, and every listed route
// that the fragments describe does carry one.
func TestRequestRefsMatchOpenAPI(t *testing.T) {
	entries, err := os.ReadDir(apitest.OpenAPIDir())
	if err != nil {
		t.Fatal(err)
	}
	described := map[string]bool{}
	derived := map[string]bool{}
	for _, e := range entries {
		n := e.Name()
		if !strings.HasSuffix(n, ".yaml") || strings.HasPrefix(n, "_") {
			continue
		}
		l := openapi3.NewLoader()
		l.IsExternalRefsAllowed = true
		doc, err := l.LoadFromFile(filepath.Join(apitest.OpenAPIDir(), n))
		if err != nil {
			t.Fatalf("%s: %v", n, err)
		}
		for path, item := range doc.Paths.Map() {
			for method, op := range item.Operations() {
				route := method + " " + path
				described[route] = true
				ref := false
				for _, p := range op.Parameters {
					if p.Value != nil && p.Value.In == "query" && (refNames[p.Value.Name] || carriesRef(p.Value.Schema, map[*openapi3.Schema]bool{})) {
						ref = true
					}
				}
				if rb := op.RequestBody; rb != nil && rb.Value != nil {
					for _, mt := range rb.Value.Content {
						if carriesRef(mt.Schema, map[*openapi3.Schema]bool{}) {
							ref = true
						}
					}
				}
				if ref {
					derived[route] = true
					if _, ok := pad.For(method, path); !ok {
						t.Errorf("%s carries a ref in its request (%s) but is not padded", route, n)
					}
				}
			}
		}
	}
	for route := range pad.RequestRefs {
		if described[route] && !derived[route] {
			t.Errorf("%s is listed in RequestRefs but its request carries no ref", route)
		}
	}
}

// TestApixPadsExactlyTheDerivedSet: every registered route is padded iff For says so.
func TestApixPadsExactlyTheDerivedSet(t *testing.T) {
	_ = apitest.App(t) // registers every module
	n := 0
	for _, ri := range apix.Routes() {
		_, want := pad.For(ri.Method, ri.Pattern)
		if ri.Padded != want {
			t.Errorf("%s %s: padded %v, For %v", ri.Method, ri.Pattern, ri.Padded, want)
		}
		if ri.Padded {
			n++
		}
	}
	if n < 40 {
		t.Fatalf("only %d padded routes", n)
	}
}

// TestEveryRouteClassHasABudget (limits v2): every class a registered route
// declares has its own entry in the limits policy — no route silently falls
// back to the default budget, P1 routes included (media_full, save, media,
// dbsc).
func TestEveryRouteClassHasABudget(t *testing.T) {
	_ = apitest.App(t)
	p := limits.DefaultPolicy()
	for _, ri := range apix.Routes() {
		ok := true
		switch ri.Class.Kind {
		case gate.ClassRead:
			_, ok = p.Reads[ri.Class.Name]
		case gate.ClassWrite:
			_, ok = p.Writes[ri.Class.Name]
			if !ok {
				_, ok = p.Refills[ri.Class.Name]
			}
		case gate.ClassAnon:
			_, ok = p.Anon[ri.Class.Name]
		}
		if !ok {
			t.Errorf("%s %s: class %+v has no budget of its own", ri.Method, ri.Pattern, ri.Class)
		}
	}
}

// fakeTiming is a clock the handler advances and a recorder of the waits.
type fakeTiming struct {
	mu    sync.Mutex
	now   time.Time
	slept []time.Duration
	// wrote is checked at the wait: nothing reached the client yet.
	rec *httptest.ResponseRecorder
	t   *testing.T
}

func (f *fakeTiming) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeTiming) advance(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	f.mu.Unlock()
}

func (f *fakeTiming) Sleep(_ *http.Request, d time.Duration) {
	if f.rec != nil && (f.rec.Body.Len() > 0 || f.rec.Code != 200 || len(f.rec.Header()) > 0) {
		f.t.Errorf("bytes reached the client before the grid point")
	}
	f.slept = append(f.slept, d)
	f.advance(d)
}

func TestWrapHoldsEveryOutcomeUntilTheGridPoint(t *testing.T) {
	f := &fakeTiming{now: time.Unix(1e9, 0), t: t}
	defer pad.SetTiming(pad.Timing{Now: f.Now, Sleep: f.Sleep})()
	pad.ResetStats()
	outcomes := []struct {
		status int
		work   time.Duration
	}{{200, 3 * ms}, {404, 1 * ms}, {400, 2 * ms}, {429, 0}, {201, 140 * ms}, {204, 0}}
	for _, o := range outcomes {
		h := pad.Wrap("POST /x/{ref}", pad.Default)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			f.advance(o.work)
			http.SetCookie(w, &http.Cookie{Name: "c", Value: "v"})
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(o.status)
			if o.status != 204 {
				_, _ = w.Write([]byte(`{"x":1}`))
			}
		}))
		f.rec = httptest.NewRecorder()
		start := f.Now()
		h.ServeHTTP(f.rec, httptest.NewRequest("POST", "/api/x/1", nil))
		if got := f.Now().Sub(start); got != 150*ms {
			t.Errorf("status %d: answered after %v", o.status, got)
		}
		if f.rec.Code != o.status || f.rec.Header().Get("Set-Cookie") != "c=v" || f.rec.Header().Get("Content-Type") != "application/json" {
			t.Errorf("status %d: response %d %v", o.status, f.rec.Code, f.rec.Header())
		}
		if o.status != 204 && f.rec.Body.String() != `{"x":1}` {
			t.Errorf("body %q", f.rec.Body.String())
		}
	}
	st := pad.Stats()
	if err := padtest.SameHistogram(st, "POST /x/{ref}", 200, 404, 400, 429, 201, 204); err != nil {
		t.Fatal(err)
	}
	// A slow response moves to the next point, and the histogram shows it.
	f.rec = nil
	slow := pad.Wrap("GET /y/{ref}", pad.Default)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f.advance(170 * ms)
		w.WriteHeader(404)
	}))
	slow.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/y/1", nil))
	if got := pad.Stats()["GET /y/{ref}"][404]; got[2] != 1 {
		t.Fatalf("slow histogram %v", got)
	}
}

func TestDeepGridDoesNotDoublePad(t *testing.T) {
	f := &fakeTiming{now: time.Unix(1e9, 0), t: t}
	defer pad.SetTiming(pad.Timing{Now: f.Now, Sleep: f.Sleep})()
	// The graph released its deep tiers at 600 ms; the page took 35 ms more.
	h := pad.Wrap("GET /feed", pad.Deep)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f.advance(635 * ms)
		w.WriteHeader(200)
	}))
	start := f.Now()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/feed", nil))
	if got := f.Now().Sub(start); got != 750*ms {
		t.Fatalf("deep page answered after %v, want 750ms", got)
	}
	shallow := pad.Wrap("GET /feed", pad.Deep)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f.advance(20 * ms)
		w.WriteHeader(200)
	}))
	start = f.Now()
	shallow.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/feed", nil))
	if got := f.Now().Sub(start); got != 600*ms {
		t.Fatalf("shallow page answered after %v, want 600ms", got)
	}
}

// TestRealTimeFoundAndNotFoundShareABucket runs the real clock: a fast
// not-found and a slower found land in the same 150 ms point (client-side
// measurement, KS on top).
func TestRealTimeFoundAndNotFoundShareABucket(t *testing.T) {
	pad.ResetStats()
	h := pad.Wrap("GET /z/{ref}", pad.Default)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("found") == "1" {
			time.Sleep(40 * ms)
			w.WriteHeader(200)
			return
		}
		w.WriteHeader(404)
	}))
	var found, missing []time.Duration
	var wg sync.WaitGroup
	var mu sync.Mutex
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			q := "/api/z/1"
			if i%2 == 0 {
				q += "?found=1"
			}
			start := time.Now()
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", q, nil))
			d := time.Since(start)
			mu.Lock()
			if i%2 == 0 {
				found = append(found, d)
			} else {
				missing = append(missing, d)
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if err := padtest.SameBucket(pad.Default, found, missing); err != nil {
		t.Fatal(err)
	}
	if err := padtest.SameHistogram(pad.Stats(), "GET /z/{ref}", 200, 404); err != nil {
		t.Fatal(err)
	}
}

func TestClientGoneStopsTheWait(t *testing.T) {
	h := pad.Wrap("GET /w/{ref}", pad.Deep)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(404) }))
	req := httptest.NewRequest("GET", "/api/w/1", nil)
	ctx, cancel := contextWithCancel(req)
	cancel()
	start := time.Now()
	h.ServeHTTP(httptest.NewRecorder(), req.WithContext(ctx))
	if time.Since(start) > 300*ms {
		t.Fatal("waited for a client that left")
	}
}

func contextWithCancel(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithCancel(r.Context())
}
