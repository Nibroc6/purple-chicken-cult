// Package storetest gives each test its own migrated database, cloned from a
// template named after the hash of every migration file.
//
// Template creation is cross-process safe (go test runs one binary per package):
// connect to the test DB, take pg_advisory_lock(7314), create + migrate the
// hash-named template only if it does not exist (built under a temporary name and
// renamed, so a failed migration never leaves a broken template), unlock.
// Templates are never dropped by tests. Each test gets pcc_t_<rand> (pool of 4),
// dropped in t.Cleanup.
//
// An unreachable database fails the test when PCC_REQUIRE_DB=1 (scripts, CI) and
// skips it with a loud message otherwise.
package storetest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// DefaultURL is the dev container's test database.
const DefaultURL = "postgres://pcc:pcc@localhost:54329/pcc_test?sslmode=disable" //nolint:gosec // local test container

const templateLock = 7314

// URL returns PCC_TEST_DATABASE_URL, defaulting to DefaultURL unless PCC_ENV=prod.
func URL() string {
	if u := os.Getenv("PCC_TEST_DATABASE_URL"); u != "" {
		return u
	}
	if os.Getenv("PCC_ENV") == "prod" {
		return ""
	}
	return DefaultURL
}

// URLFor returns base with its database name replaced by name.
func URLFor(base, name string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	u.Path = "/" + name
	return u.String(), nil
}

var (
	tplMu   sync.Mutex
	tplDone = map[string]bool{}
)

func unavailable(t testing.TB, err error) {
	t.Helper()
	if os.Getenv("PCC_REQUIRE_DB") == "1" {
		t.Fatalf("storetest: database unavailable and PCC_REQUIRE_DB=1: %v", err)
	}
	t.Skipf("storetest: *** SKIPPING: test database unavailable (%v). Start it with `docker compose up -d postgres` or set PCC_TEST_DATABASE_URL; set PCC_REQUIRE_DB=1 to fail instead. ***", err)
}

func randName(prefix string) string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return prefix + hex.EncodeToString(b[:])
}

// Template returns the name of the migrated template database, creating it if needed.
func Template(ctx context.Context, base string) (string, error) {
	hash, err := store.MigrationsHash()
	if err != nil {
		return "", err
	}
	tpl := "pcc_tpl_" + hash
	tplMu.Lock()
	defer tplMu.Unlock()
	if tplDone[tpl] {
		return tpl, nil
	}
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		return "", err
	}
	defer func() { _ = admin.Close(context.WithoutCancel(ctx)) }()
	if _, err := admin.Exec(ctx, "SELECT pg_advisory_lock($1)", templateLock); err != nil {
		return "", err
	}
	defer func() { _, _ = admin.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", templateLock) }()

	var exists bool
	if err := admin.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", tpl).Scan(&exists); err != nil {
		return "", err
	}
	if !exists {
		tmp := randName("pcc_tplbuild_")
		if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{tmp}.Sanitize()); err != nil {
			return "", err
		}
		tmpURL, err := URLFor(base, tmp)
		if err != nil {
			return "", err
		}
		db, err := store.Open(ctx, tmpURL, store.WithMaxConns(2))
		if err != nil {
			return "", err
		}
		err = store.Migrate(ctx, db)
		db.Close()
		if err != nil {
			_, _ = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{tmp}.Sanitize())
			return "", fmt.Errorf("storetest: migrating template: %w", err)
		}
		if _, err := admin.Exec(ctx, "ALTER DATABASE "+pgx.Identifier{tmp}.Sanitize()+" RENAME TO "+pgx.Identifier{tpl}.Sanitize()); err != nil {
			return "", err
		}
	}
	tplDone[tpl] = true
	return tpl, nil
}

// CreateFromTemplate creates database name from the current template.
func CreateFromTemplate(ctx context.Context, base, name string) error {
	tpl, err := Template(ctx, base)
	if err != nil {
		return err
	}
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		return err
	}
	defer func() { _ = admin.Close(context.WithoutCancel(ctx)) }()
	sql := "CREATE DATABASE " + pgx.Identifier{name}.Sanitize() + " TEMPLATE " + pgx.Identifier{tpl}.Sanitize()
	for attempt := 0; ; attempt++ {
		_, err = admin.Exec(ctx, sql)
		var pe *pgconn.PgError
		if err == nil || attempt >= 20 || !asPg(err, &pe) || pe.Code != "55006" { // object_in_use: template busy
			return err
		}
		time.Sleep(time.Duration(50+attempt*25) * time.Millisecond)
	}
}

func asPg(err error, pe **pgconn.PgError) bool {
	e, ok := err.(*pgconn.PgError) //nolint:errorlint // pgx returns the concrete type
	if ok {
		*pe = e
	}
	return ok
}

// NewDB returns a fresh migrated database for t (pool of 4), dropped at cleanup.
func NewDB(t testing.TB) *store.DB {
	t.Helper()
	base := URL()
	if base == "" {
		unavailable(t, fmt.Errorf("PCC_TEST_DATABASE_URL unset"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if c, err := pgx.Connect(ctx, base); err != nil {
		unavailable(t, err)
	} else {
		_ = c.Close(ctx)
	}
	name := randName("pcc_t_")
	if err := CreateFromTemplate(ctx, base, name); err != nil {
		t.Fatalf("storetest: create database: %v", err)
	}
	u, err := URLFor(base, name)
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, u, store.WithMaxConns(4))
	if err != nil {
		t.Fatalf("storetest: open: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		admin, err := pgx.Connect(ctx, base)
		if err != nil {
			t.Logf("storetest: cleanup connect: %v", err)
			return
		}
		defer func() { _ = admin.Close(ctx) }()
		if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()); err != nil {
			t.Logf("storetest: drop %s: %v", name, err)
		}
	})
	return db
}
