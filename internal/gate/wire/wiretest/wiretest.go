// Package wiretest builds a full *wire.App over a fresh storetest database for Go
// tests (fake clock, sequential IDs, fixed keys). It lives apart from wire so the
// production binary never links `testing`.
//
// Owner: SCAFFOLD (contract steward during phases).
package wiretest

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coretest"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/ids"
	"github.com/Nibroc6/purple-chicken-cult/internal/store/storetest"
)

// Option adjusts the test configuration (PCC_* overrides).
type Option func(map[string]string)

// WithEnv sets a PCC_* variable.
func WithEnv(k, v string) Option { return func(m map[string]string) { m[k] = v } }

// NewApp builds a migrated, initialized App for t.
func NewApp(t testing.TB, opts ...Option) *wire.App {
	t.Helper()
	env := map[string]string{"PCC_TEST_HOOKS": "1"}
	for _, o := range opts {
		o(env)
	}
	cfg := coretest.Config(t, env)
	db := storetest.NewDB(t)
	_, key := coretest.FixedKeys(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a, err := wire.Build(ctx, cfg, db, slog.New(slog.DiscardHandler),
		wire.WithClock(coretest.Clock()), wire.WithIDs(&ids.Seq{}), wire.WithServerKey(key))
	if err != nil {
		t.Fatalf("wiretest: build: %v", err)
	}
	return a
}

// Login creates (if needed) an active genesis user with handle and a fresh user
// session; it returns the cookie token and the CSRF token.
func Login(t testing.TB, a *wire.App, handle string) (cookie, csrf string) {
	t.Helper()
	ctx := context.Background()
	if _, ok := a.Users.ByHandle(ctx, strings.ToLower(handle)); !ok {
		if _, err := a.Users.Create(ctx, a.Deps().DB.Q(), core.NewUser{Handle: handle, Genesis: true}); err != nil {
			t.Fatalf("wiretest: create %s: %v", handle, err)
		}
	}
	tok, c, err := a.Hooks().Login(ctx, handle)
	if err != nil {
		t.Fatalf("wiretest: login %s: %v", handle, err)
	}
	return tok, c
}

// TestWorld is the fixture builder over the App's deps and database.
type TestWorld = coretest.World

// World returns a fixture builder writing to the App's database and memory.
func World(t testing.TB, a *wire.App) *TestWorld {
	return coretest.NewWorld(t, a.Deps(), a.Deps().DB)
}

// Charges is every charge on record of a, summed over users: rate_counters per
// "bucket@window start", and per build-up bucket ("b:<kind>", rate_buckets)
// the charges it still holds at the harness clock (whole refill intervals
// between now and its tat).
func Charges(t testing.TB, a *wire.App) map[string]int {
	t.Helper()
	ctx := context.Background()
	d := a.Deps()
	out := map[string]int{}
	rows, err := d.DB.Q().Query(ctx, "SELECT bucket, window_start, count FROM rate_counters")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var (
			b  string
			at time.Time
			n  int
		)
		if err := rows.Scan(&b, &at, &n); err != nil {
			t.Fatal(err)
		}
		out[b+"@"+at.UTC().Format(time.RFC3339)] += n
	}
	rows.Close()
	refills := a.Limits.Policy().Refills
	now := d.Clock.Now()
	rows, err = d.DB.Q().Query(ctx, "SELECT bucket, tat FROM rate_buckets")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			b   string
			tat time.Time
		)
		if err := rows.Scan(&b, &tat); err != nil {
			t.Fatal(err)
		}
		r, ok := refills[strings.TrimPrefix(b, "b:")]
		if ahead := tat.Sub(now); ok && ahead > 0 {
			out[b] += int((ahead + r.Every - 1) / r.Every)
		}
	}
	return out
}
