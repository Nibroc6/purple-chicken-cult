package store_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/store"
	"github.com/Nibroc6/purple-chicken-cult/internal/store/storetest"
)

func TestMigrationsWellFormed(t *testing.T) {
	ms, err := store.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) < 16 {
		t.Fatalf("expected the Phase 0 + Phase 1 scaffold migrations, got %d", len(ms))
	}
	for _, m := range ms {
		inScaffold := (m.Version >= 1 && m.Version <= 99) || (m.Version >= 2001 && m.Version <= 2099)
		inWP := (m.Version >= 1000 && m.Version <= 1999) || (m.Version >= 3000 && m.Version <= 3999)
		if !inScaffold && !inWP {
			t.Errorf("migration %04d_%s outside every reserved range", m.Version, m.Name)
		}
	}
}

func TestMigrateSmoke(t *testing.T) {
	db := storetest.NewDB(t)
	ctx := context.Background()
	// Re-running is a no-op.
	if err := store.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	ms, _ := store.Migrations()
	var n int
	if err := db.Pool.QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != len(ms) {
		t.Fatalf("applied %d of %d migrations", n, len(ms))
	}
	for _, table := range []string{"users", "sessions", "edges", "posts", "post_parents", "engagements", "notifications",
		"rate_counters", "reports", "jobs", "metrics_daily", "cosign_intents", "media", "push_subscriptions",
		"user_themes", "automation_signals", "mod_queue"} {
		var ok bool
		if err := db.Pool.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", table).Scan(&ok); err != nil || !ok {
			t.Errorf("table %s missing (%v)", table, err)
		}
	}
}

func TestMigrateRefusesChangedFile(t *testing.T) {
	db := storetest.NewDB(t)
	ctx := context.Background()
	if _, err := db.Pool.Exec(ctx, "UPDATE schema_migrations SET sha256 = '\\x00' WHERE version = 1"); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx, db); err == nil {
		t.Fatal("a changed applied migration must stop the runner")
	}
}

func TestTxAndUniqueViolation(t *testing.T) {
	db := storetest.NewDB(t)
	ctx := context.Background()
	ins := func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO users (id, ext_id, handle, handle_canon, is_genesis, webauthn_user_id)
			VALUES (3000000001, gen_random_uuid(), 'alice', 'alice', true, '\x01')`)
		return err
	}
	if err := db.Tx(ctx, ins); err != nil {
		t.Fatal(err)
	}
	if err := db.TxSerializable(ctx, ins); !store.IsUniqueViolation(err) {
		t.Fatalf("want unique violation, got %v", err)
	}
}
