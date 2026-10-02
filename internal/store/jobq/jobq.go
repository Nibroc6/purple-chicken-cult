// Package jobq is the Postgres-backed job queue (ARCHITECTURE §1 "Jobs"): rows in
// `jobs`, claimed with SELECT … FOR UPDATE SKIP LOCKED, retried with exponential
// backoff (2^n s), optional dedupe keys, and self-rescheduling periodic jobs.
//
// Job kinds and the WP registering each handler are listed in plan §4.3.
package jobq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/platform/clock"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// HandlerFunc runs one job.
type HandlerFunc func(ctx context.Context, payload json.RawMessage) error

// Queue dispatches jobs to handlers.
type Queue struct {
	db  *store.DB
	clk clock.Clock
	log *slog.Logger

	mu       sync.RWMutex
	handlers map[string]HandlerFunc
	periodic map[string]time.Duration

	// PollInterval is how long Run sleeps when no job is due.
	PollInterval time.Duration
	// LockFor is how long a claimed job stays invisible to other workers.
	LockFor time.Duration
}

// New returns a queue.
func New(db *store.DB, clk clock.Clock, log *slog.Logger) *Queue {
	if log == nil {
		log = slog.Default()
	}
	return &Queue{db: db, clk: clk, log: log, handlers: map[string]HandlerFunc{}, periodic: map[string]time.Duration{},
		PollInterval: time.Second, LockFor: 5 * time.Minute}
}

// Enqueue inserts a job (in tx when given, so it commits with the caller's
// writes). With a non-empty dedupe key, a second pending job of the same kind and
// key is silently dropped. A zero runAt means now.
func (q *Queue) Enqueue(ctx context.Context, tx store.Q, kind string, payload any, runAt time.Time, dedupe string) error {
	if tx == nil {
		tx = q.db.Q()
	}
	if runAt.IsZero() {
		runAt = q.clk.Now()
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if payload == nil {
		b = []byte("{}")
	}
	var dk *string
	if dedupe != "" {
		dk = &dedupe
	}
	_, err = tx.Exec(ctx, `INSERT INTO jobs (kind, payload, dedupe_key, run_at) VALUES ($1, $2, $3, $4)
		ON CONFLICT (kind, dedupe_key) WHERE done_at IS NULL AND dedupe_key IS NOT NULL DO NOTHING`, kind, b, dk, runAt)
	return err
}

// Handle registers the handler for kind.
func (q *Queue) Handle(kind string, fn HandlerFunc) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.handlers[kind] = fn
}

// Every makes kind a periodic job (dedupe key = kind): it is scheduled when Run
// or Drain starts and re-scheduled period after each completion.
func (q *Queue) Every(kind string, period time.Duration) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.periodic[kind] = period
}

func (q *Queue) kinds() []string {
	q.mu.RLock()
	defer q.mu.RUnlock()
	out := make([]string, 0, len(q.handlers))
	for k := range q.handlers {
		out = append(out, k)
	}
	return out
}

func (q *Queue) schedulePeriodic(ctx context.Context) error {
	q.mu.RLock()
	defer q.mu.RUnlock()
	for kind := range q.periodic {
		if err := q.Enqueue(ctx, nil, kind, nil, time.Time{}, kind); err != nil {
			return err
		}
	}
	return nil
}

type claimed struct {
	id                    int64
	kind                  string
	payload               json.RawMessage
	attempts, maxAttempts int
}

func (q *Queue) claim(ctx context.Context) (*claimed, error) {
	now := q.clk.Now()
	var c claimed
	err := q.db.Pool.QueryRow(ctx, `UPDATE jobs SET locked_until = $2, attempts = attempts + 1
		WHERE id = (SELECT id FROM jobs WHERE done_at IS NULL AND run_at <= $1
		              AND (locked_until IS NULL OR locked_until < $1) AND kind = ANY($3)
		            ORDER BY run_at, id FOR UPDATE SKIP LOCKED LIMIT 1)
		RETURNING id, kind, payload, attempts, max_attempts`, now, now.Add(q.LockFor), q.kinds()).
		Scan(&c.id, &c.kind, &c.payload, &c.attempts, &c.maxAttempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (q *Queue) runOne(ctx context.Context, c *claimed) error {
	q.mu.RLock()
	fn := q.handlers[c.kind]
	period, periodic := q.periodic[c.kind]
	q.mu.RUnlock()

	err := func() (err error) {
		defer func() {
			if v := recover(); v != nil {
				err = fmt.Errorf("jobq: panic: %v", v)
			}
		}()
		return fn(ctx, c.payload)
	}()
	now := q.clk.Now()
	if err == nil {
		if _, uerr := q.db.Pool.Exec(ctx, "UPDATE jobs SET done_at = $2, locked_until = NULL WHERE id = $1", c.id, now); uerr != nil {
			return uerr
		}
		if periodic {
			return q.Enqueue(ctx, nil, c.kind, nil, now.Add(period), c.kind)
		}
		return nil
	}
	q.log.Warn("job failed", "job", c.kind, "attempt", c.attempts, "err", err.Error())
	if c.attempts >= c.maxAttempts {
		_, uerr := q.db.Pool.Exec(ctx, "UPDATE jobs SET done_at = $2, last_error = $3, locked_until = NULL WHERE id = $1", c.id, now, err.Error())
		if uerr == nil && periodic {
			uerr = q.Enqueue(ctx, nil, c.kind, nil, now.Add(period), c.kind)
		}
		return errors.Join(err, uerr)
	}
	backoff := time.Duration(1<<min(c.attempts, 16)) * time.Second
	_, uerr := q.db.Pool.Exec(ctx, "UPDATE jobs SET run_at = $2, last_error = $3, locked_until = NULL WHERE id = $1", c.id, now.Add(backoff), err.Error())
	return errors.Join(err, uerr)
}

// Run processes due jobs with n workers until ctx is cancelled.
func (q *Queue) Run(ctx context.Context, workers int) error {
	if err := q.schedulePeriodic(ctx); err != nil {
		return err
	}
	var wg sync.WaitGroup
	for range max(workers, 1) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				c, err := q.claim(ctx)
				if err != nil && ctx.Err() == nil {
					q.log.Error("job claim", "err", err.Error())
				}
				if c == nil {
					select {
					case <-ctx.Done():
					case <-time.After(q.PollInterval):
					}
					continue
				}
				_ = q.runOne(ctx, c)
			}
		}()
	}
	wg.Wait()
	return nil
}

// Drain runs every due job synchronously (tests); it returns the joined handler errors.
func (q *Queue) Drain(ctx context.Context) error {
	if err := q.schedulePeriodic(ctx); err != nil {
		return err
	}
	var errs []error
	for i := 0; i < 10000; i++ {
		c, err := q.claim(ctx)
		if err != nil {
			return err
		}
		if c == nil {
			break
		}
		if err := q.runOne(ctx, c); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
