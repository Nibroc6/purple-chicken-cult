// Package devseed creates synthetic graphs/posts for dev and perf tests and loads
// the named e2e fixtures testdata/fixtures/<wp-id>.json. It refuses unless
// cfg.DevLike(). It may import gate/graph (Synth; same owner, archtest A8).
//
// Owner: P0-W02 → P1-W03.
//
// Both entry points write every fact in one transaction (placeholder record
// hashes, like coretest.World: dev data carries no signed records), then push
// the committed facts into the in-memory modules: the graph and the users cache
// reload from the database, and memory-only modules receive the facts directly.
package devseed

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// ErrNotDev is returned outside dev/test.
var ErrNotDev = errors.New("devseed: refused outside PCC_ENV=dev|test")

// Service is the devseed module.
type Service struct {
	d *core.Deps

	mu  sync.Mutex // one seeding at a time (IDs are allocated from max(id))
	dir string     // fixture directory; "" = <module root>/testdata/fixtures
}

// New returns the service.
func New(d *core.Deps) *Service { return &Service{d: d} }

// Init does nothing.
func (s *Service) Init(context.Context) error { return nil }

// SetFixtureDir overrides where LoadFixture looks for <name>.json (tests).
func (s *Service) SetFixtureDir(dir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dir = dir
}

func (s *Service) allowed() error {
	if s.d == nil || s.d.Cfg == nil || !s.d.Cfg.DevLike() {
		return ErrNotDev
	}
	if s.d.DB == nil {
		return errors.New("devseed: no database")
	}
	return nil
}

// Synthetic seeds n users with a "ba" (Barabási–Albert) or "ws" (Watts–Strogatz)
// graph from graph.Synth, plus seven days of posts (Poisson, one per user per
// day), some replies to friends' posts and likes from friends. The same seed and
// starting user ID always yield the same data.
func (s *Service) Synthetic(ctx context.Context, n int, model string, seed int64) error {
	if err := s.allowed(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var first int64
	if err := s.d.DB.Q().QueryRow(ctx, "SELECT COALESCE(max(id), 0) + 1 FROM users").Scan(&first); err != nil {
		return err
	}
	b, err := synthetic(n, model, seed, first, s.d.Clock.Now())
	if err != nil {
		return err
	}
	return s.commit(ctx, b)
}

// fixtureName keeps LoadFixture inside the fixture directory.
var fixtureName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// LoadFixture loads testdata/fixtures/<name>.json.
func (s *Service) LoadFixture(ctx context.Context, name string) error {
	if err := s.allowed(); err != nil {
		return err
	}
	if !fixtureName.MatchString(name) {
		return errors.New("devseed: invalid fixture name")
	}
	s.mu.Lock()
	dir := s.dir
	s.mu.Unlock()
	if dir == "" {
		root, err := moduleRoot()
		if err != nil {
			return err
		}
		dir = filepath.Join(root, "testdata", "fixtures")
	}
	data, err := os.ReadFile(filepath.Join(dir, name+".json")) //nolint:gosec // G304: name is validated above
	if err != nil {
		return err
	}
	return s.LoadFixtureJSON(ctx, name, data)
}

// LoadFixtureJSON loads a fixture document (format: see Fixture) under name,
// which only selects its user ID range.
func (s *Service) LoadFixtureJSON(ctx context.Context, name string, data []byte) error {
	if err := s.allowed(); err != nil {
		return err
	}
	f, err := ParseFixture(data)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := f.build(ctx, s.d, name)
	if err != nil {
		return err
	}
	return s.commit(ctx, b)
}

func (s *Service) commit(ctx context.Context, b *batch) error {
	if err := s.d.DB.Tx(ctx, func(tx pgx.Tx) error { return b.write(ctx, tx) }); err != nil {
		return err
	}
	return s.apply(ctx, b)
}

// moduleRoot walks up from the working directory to the directory holding
// go.mod (the server runs from the repo root; go test from a package dir).
func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("devseed: module root (go.mod) not found from the working directory")
		}
		dir = parent
	}
}
