package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"

	"github.com/jackc/pgx/v5"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// MigrationLock is the advisory lock key held while migrating.
const MigrationLock = 7313

// Migration is one embedded migration file.
type Migration struct {
	Version int
	Name    string
	SQL     string
	SHA256  []byte // over the LF-normalized file bytes
}

var fileRE = regexp.MustCompile(`^(\d{4})_([a-z0-9_]+)\.sql$`)

// Migrations returns every embedded migration in numeric order.
//
// Numbering (plan §3.1): 0001–0099 SCAFFOLD-0a; 1000+10n…+9 Phase 0 WP n;
// 2001–2099 SCAFFOLD-1; 3000+10n…+9 Phase 1 WP n. WP migrations are additive only.
func Migrations() ([]Migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, err
	}
	var out []Migration
	seen := map[int]string{}
	for _, e := range entries {
		m := fileRE.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("store: bad migration file name %q", e.Name())
		}
		v, _ := strconv.Atoi(m[1])
		if prev, dup := seen[v]; dup {
			return nil, fmt.Errorf("store: duplicate migration version %d (%s, %s)", v, prev, e.Name())
		}
		seen[v] = e.Name()
		b, err := fs.ReadFile(migrationFS, path.Join("migrations", e.Name()))
		if err != nil {
			return nil, err
		}
		b = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
		sum := sha256.Sum256(b)
		out = append(out, Migration{Version: v, Name: m[2], SQL: string(b), SHA256: sum[:]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// MigrationsHash is a short hash over every migration (names template test DBs).
func MigrationsHash() (string, error) {
	ms, err := Migrations()
	if err != nil {
		return "", err
	}
	h := sha256.New()
	for _, m := range ms {
		fmt.Fprintf(h, "%d:%s:%x\n", m.Version, m.Name, m.SHA256)
	}
	return hex.EncodeToString(h.Sum(nil))[:8], nil
}

// ErrMigrationChanged means an applied migration file was edited (a bug).
var ErrMigrationChanged = errors.New("store: an applied migration has changed")

// Migrate applies every unapplied migration in numeric order, each in its own
// transaction, under pg_advisory_lock(7313). It refuses to run if an applied
// migration's sha256 differs from the embedded file. Up-only.
func Migrate(ctx context.Context, db *DB) error {
	ms, err := Migrations()
	if err != nil {
		return err
	}
	conn, err := db.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", MigrationLock); err != nil {
		return err
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", MigrationLock) }()

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version int PRIMARY KEY, name text NOT NULL, applied_at timestamptz NOT NULL DEFAULT now(), sha256 bytea NOT NULL)`); err != nil {
		return err
	}
	applied := map[int][]byte{}
	rows, err := conn.Query(ctx, "SELECT version, sha256 FROM schema_migrations")
	if err != nil {
		return err
	}
	for rows.Next() {
		var v int
		var s []byte
		if err := rows.Scan(&v, &s); err != nil {
			rows.Close()
			return err
		}
		applied[v] = s
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, m := range ms {
		if sum, ok := applied[m.Version]; ok {
			if !bytes.Equal(sum, m.SHA256) {
				return fmt.Errorf("%w: %04d_%s", ErrMigrationChanged, m.Version, m.Name)
			}
		}
	}
	for _, m := range ms {
		if _, ok := applied[m.Version]; ok {
			continue
		}
		err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, m.SQL); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version, name, sha256) VALUES ($1, $2, $3)", m.Version, m.Name, m.SHA256)
			return err
		})
		if err != nil {
			return fmt.Errorf("store: migration %04d_%s: %w", m.Version, m.Name, err)
		}
	}
	return nil
}
