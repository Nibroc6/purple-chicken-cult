// Package store is database infrastructure ONLY: the pgx pool, transaction helpers,
// the embedded migration runner and the job queue. It has no domain knowledge.
//
// Importers are restricted (ARCHITECTURE §13, archtest A1): only packages under
// internal/gate/..., internal/store/... and cmd/pcc (Open, Migrate, Close) may
// import it. Handlers never touch the database.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB wraps the connection pool.
type DB struct{ Pool *pgxpool.Pool }

// Q is satisfied by *pgxpool.Pool and pgx.Tx: code that may run inside or outside
// a transaction takes a Q.
type Q interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type options struct{ maxConns int32 }

// Option configures Open.
type Option func(*options)

// WithMaxConns sets the pool size (serve default 20, tests 4).
func WithMaxConns(n int32) Option { return func(o *options) { o.maxConns = n } }

// Open connects and pings.
func Open(ctx context.Context, url string, opts ...Option) (*DB, error) {
	o := options{maxConns: 20}
	for _, f := range opts {
		f(&o)
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("store: parse url: %w", err)
	}
	cfg.MaxConns = o.maxConns
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("store: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	return &DB{Pool: pool}, nil
}

// Close closes the pool.
func (db *DB) Close() { db.Pool.Close() }

// Q returns the pool as a Q.
func (db *DB) Q() Q { return db.Pool }

// IsUniqueViolation reports a 23505 unique_violation.
func IsUniqueViolation(err error) bool { return pgCode(err) == "23505" }

// IsForeignKeyViolation reports a 23503 foreign_key_violation.
func IsForeignKeyViolation(err error) bool { return pgCode(err) == "23503" }

func pgCode(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Code
	}
	return ""
}
