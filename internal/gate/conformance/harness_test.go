package conformance_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coretest"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/clock"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/ids"
	"github.com/Nibroc6/purple-chicken-cult/internal/store/storetest"
)

// laneIDs keeps the IDs minted by the viewer's own calls independent of what
// other people did: two sequences, switched by the harness. A world with more
// activity outside H(V) therefore still gives V's new posts the same IDs (and
// so the same refs) as the world without it.
type laneIDs struct {
	other atomic.Bool
	v, o  ids.Seq
}

func (l *laneIDs) New() uuid.UUID {
	if l.other.Load() {
		u := l.o.New()
		u[0] = 0x0f // a distinct range for the other lane
		return u
	}
	return l.v.New()
}

// scope collects cleanups so one rapid check can release its databases before
// the next check runs (storetest registers its drop through Cleanup).
type scope struct {
	testing.TB
	fns []func()
}

func (s *scope) Cleanup(f func()) { s.fns = append(s.fns, f) }

func (s *scope) close() {
	for i := len(s.fns) - 1; i >= 0; i-- {
		s.fns[i]()
	}
	s.fns = nil
}

// world is one wired App over its own database.
type world struct {
	t     testing.TB
	a     *wire.App
	d     *core.Deps
	m     *model
	clock *clock.Fake
	lanes *laneIDs
	ctx   context.Context
}

// newWorld builds an App over a fresh database (fake clock at coretest.Epoch,
// fixed keys, lane IDs) and loads layers through devseed in order.
func newWorld(tb testing.TB, m *model, layers ...*layer) *world {
	tb.Helper()
	cfg := coretest.Config(tb, map[string]string{"PCC_TEST_HOOKS": "1"})
	db := storetest.NewDB(tb)
	_, key := coretest.FixedKeys(tb)
	ctx, cancel := context.WithCancel(context.Background())
	tb.Cleanup(cancel)
	w := &world{t: tb, m: m, clock: coretest.Clock(), lanes: &laneIDs{}, ctx: context.Background()}
	a, err := wire.Build(ctx, cfg, db, slog.New(slog.DiscardHandler), wire.WithClock(w.clock), wire.WithIDs(w.lanes),
		wire.WithServerKey(key))
	if err != nil {
		tb.Fatalf("conformance: build: %v", err)
	}
	w.a, w.d = a, a.Deps()
	for _, l := range layers {
		w.load(l)
	}
	return w
}

func (w *world) load(l *layer) {
	w.t.Helper()
	w.lanes.other.Store(true)
	defer w.lanes.other.Store(false)
	if err := w.a.DevSeed.LoadFixtureJSON(w.ctx, l.name, l.json()); err != nil {
		w.t.Fatalf("conformance: load %s: %v\n%s", l.name, err, l.json())
	}
	// Likes of posts loaded earlier: the fixture format only likes its own posts.
	n := 0
	for _, lk := range l.likes {
		if l.hasPost(lk.Post) {
			continue
		}
		n++
		if _, err := w.d.DB.Q().Exec(w.ctx, "INSERT INTO engagements (user_id, post_id, kind, at) VALUES ($1, $2, 1, $3)",
			int64(w.id(lk.User)), w.postID(lk.Post), w.clock.Now().Add(-time.Minute)); err != nil {
			w.t.Fatalf("conformance: like: %v", err)
		}
	}
	if n > 0 {
		if err := w.a.Engage.Load(w.ctx); err != nil {
			w.t.Fatalf("conformance: engage load: %v", err)
		}
	}
}

func (w *world) id(h string) core.UserID { return w.m.id(h) }

// as is a fresh user viewer context for handle h.
func (w *world) as(h string) context.Context {
	return core.WithViewer(w.ctx, core.Viewer{ID: w.id(h), Kind: core.SessUser, AuthAt: w.clock.Now()})
}

// other runs fn in the other-people ID lane.
func (w *world) other(fn func()) {
	w.lanes.other.Store(true)
	defer w.lanes.other.Store(false)
	fn()
}

func (w *world) postID(label string) core.PostID { return ids.Named("post:" + label) }

func (w *world) postRef(viewer, label string) gate.Ref {
	return w.d.Refs.Post(w.id(viewer), w.postID(label))
}

func (w *world) userRef(viewer, h string) gate.Ref { return w.d.Refs.User(w.id(viewer), w.id(h)) }

// ---- normalization (buildability D1) ----

// tokenKeys are JSON fields holding opaque tokens that are legitimately random
// or sequence-based; they are replaced by their first-seen index in the run.
var tokenKeys = map[string]string{
	"session": "session", "next": "cursor", "cursor": "cursor", "token": "collapse", "snapshot": "snapshot",
	"hs": "hs",
}

type normalizer struct {
	seen map[string]map[string]int
	// extra marks additional keys as tokens for one call (notification ids).
	extra map[string]string
}

func newNormalizer() *normalizer { return &normalizer{seen: map[string]map[string]int{}} }

func (n *normalizer) token(class, raw string) string {
	if raw == "" {
		return ""
	}
	m := n.seen[class]
	if m == nil {
		m = map[string]int{}
		n.seen[class] = m
	}
	i, ok := m[raw]
	if !ok {
		i = len(m) + 1
		m[raw] = i
	}
	return fmt.Sprintf("<%s#%d>", class, i)
}

func (n *normalizer) walk(v any) any {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys) // first-seen indexes follow a fixed key order
		for _, k := range keys {
			class, ok := tokenKeys[k]
			if !ok {
				class, ok = n.extra[k]
			}
			if s, isStr := x[k].(string); ok && isStr {
				x[k] = n.token(class, s)
				continue
			}
			x[k] = n.walk(x[k])
		}
		return x
	case []any:
		for i := range x {
			x[i] = n.walk(x[i])
		}
		return x
	}
	return v
}

// errCode is the canonical form of a gate failure.
func errCode(err error) string {
	var p *gate.Problem
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, gate.ErrNotFound):
		return "not_found"
	case errors.As(err, &p):
		return fmt.Sprintf("problem:%s:%d:%s", p.Code, p.Status, p.Field)
	case errors.Is(err, gate.ErrRateLimited), errors.Is(err, gate.ErrStepUp), errors.Is(err, gate.ErrDisabled):
		return err.Error()
	}
	return "unexpected:" + err.Error()
}

// record normalizes one call's result: the comparable line and the decoded value.
func (n *normalizer) record(name string, out any, err error, extra map[string]string) (string, any) {
	b, jerr := json.Marshal(out)
	if jerr != nil {
		return fmt.Sprintf("%s → marshal error %v", name, jerr), nil
	}
	var v any
	_ = json.Unmarshal(b, &v)
	n.extra = extra
	v = n.walk(v)
	n.extra = nil
	b, _ = json.Marshal(v)
	return fmt.Sprintf("%s → %s %s", name, errCode(err), b), v
}

// garbageRef is a well-formed but meaningless ref.
const garbageRef gate.Ref = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

// randomPostID is a post ID that exists in no world.
func randomPostID(i int) core.PostID {
	var u uuid.UUID
	binary.BigEndian.PutUint64(u[8:], uint64(i)+0xdead0000)
	u[0], u[6], u[8] = 0x7e, 0x80, 0x80
	return u
}

func firstDiff(a, b []string) (int, string) {
	for i := range min(len(a), len(b)) {
		if a[i] != b[i] {
			j := 0
			for j < min(len(a[i]), len(b[i])) && a[i][j] == b[i][j] {
				j++
			}
			lo := max(0, j-300)
			return i, fmt.Sprintf("call %d differs at byte %d:\n W1: …%s\n W2: …%s", i, j, clip(a[i][lo:]), clip(b[i][lo:]))
		}
	}
	if len(a) != len(b) {
		return min(len(a), len(b)), fmt.Sprintf("call counts differ: %d vs %d", len(a), len(b))
	}
	return -1, ""
}

func clip(s string) string {
	if len(s) > 600 {
		return s[:600] + "…"
	}
	return s
}
