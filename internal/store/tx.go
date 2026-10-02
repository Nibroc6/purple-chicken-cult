package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

const maxRetries = 3

func retryable(err error) bool {
	c := pgCode(err)
	return c == "40001" || c == "40P01" // serialization_failure, deadlock_detected
}

func (db *DB) run(ctx context.Context, opts pgx.TxOptions, fn func(tx pgx.Tx) error) error {
	var err error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt*attempt) * 10 * time.Millisecond):
			}
		}
		err = pgx.BeginTxFunc(ctx, db.Pool, opts, fn)
		if err == nil || !retryable(err) {
			return err
		}
	}
	return err
}

// Tx runs fn in a READ COMMITTED transaction, retrying serialization failures and
// deadlocks up to 3 times. fn must be idempotent with respect to retries.
func (db *DB) Tx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	return db.run(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, fn)
}

// TxSerializable runs fn in a SERIALIZABLE transaction (invite, budget and edge
// writes), retrying serialization failures up to 3 times.
func (db *DB) TxSerializable(ctx context.Context, fn func(tx pgx.Tx) error) error {
	return db.run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, fn)
}
