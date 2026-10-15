package jobq_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/platform/clock"
	"github.com/Nibroc6/purple-chicken-cult/internal/store/jobq"
	"github.com/Nibroc6/purple-chicken-cult/internal/store/storetest"
)

func TestEnqueueDrainRetryDedupe(t *testing.T) {
	db := storetest.NewDB(t)
	ctx := context.Background()
	clk := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	q := jobq.New(db, clk, nil)

	var got []int
	fails := 1
	q.Handle("t.echo", func(_ context.Context, p json.RawMessage) error {
		var v struct{ N int }
		_ = json.Unmarshal(p, &v)
		if v.N == 2 && fails > 0 {
			fails--
			return errors.New("transient")
		}
		got = append(got, v.N)
		return nil
	})
	for i := 1; i <= 3; i++ {
		if err := q.Enqueue(ctx, nil, "t.echo", map[string]int{"N": i}, time.Time{}, ""); err != nil {
			t.Fatal(err)
		}
	}
	// Dedupe: the second job with the same key is dropped.
	_ = q.Enqueue(ctx, nil, "t.echo", map[string]int{"N": 9}, time.Time{}, "k")
	_ = q.Enqueue(ctx, nil, "t.echo", map[string]int{"N": 9}, time.Time{}, "k")

	if err := q.Drain(ctx); err == nil {
		t.Fatal("first drain must report the transient failure")
	}
	if len(got) != 3 { // 1, 3, 9
		t.Fatalf("after first drain: %v", got)
	}
	clk.Advance(10 * time.Second) // past the 2 s backoff
	if err := q.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("retry must run once: %v", got)
	}
}

func TestPeriodic(t *testing.T) {
	db := storetest.NewDB(t)
	ctx := context.Background()
	clk := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	q := jobq.New(db, clk, nil)
	n := 0
	q.Handle("t.tick", func(context.Context, json.RawMessage) error { n++; return nil })
	q.Every("t.tick", time.Hour)
	_ = q.Drain(ctx)
	_ = q.Drain(ctx)
	if n != 1 {
		t.Fatalf("periodic job must run once per period, ran %d", n)
	}
	clk.Advance(time.Hour)
	_ = q.Drain(ctx)
	if n != 2 {
		t.Fatalf("after one period: %d", n)
	}
}
