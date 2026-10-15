package wire

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// Hooks are the runtime test hooks behind /api/_test/* (PCC_ENV=test and
// PCC_TEST_HOOKS=1 only). Every e2e run uses its own database, so Reset only
// ever wipes that run's data.
type Hooks struct{ a *App }

// Reset truncates every table (except schema_migrations) and reloads caches
// (users, the post index, liker sets and the graph).
func (h *Hooks) Reset(ctx context.Context) error {
	d := h.a.d
	rows, err := d.DB.Q().Query(ctx, `SELECT tablename FROM pg_tables
		WHERE schemaname = 'public' AND tablename <> 'schema_migrations'`)
	if err != nil {
		return err
	}
	var tables []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			rows.Close()
			return err
		}
		tables = append(tables, `"`+t+`"`)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(tables) > 0 {
		if _, err := d.DB.Q().Exec(ctx, "TRUNCATE "+strings.Join(tables, ", ")+" RESTART IDENTITY CASCADE"); err != nil {
			return err
		}
	}
	if err := h.a.Users.Reload(ctx); err != nil {
		return err
	}
	if err := h.a.PostIndex.Load(ctx); err != nil {
		return err
	}
	if err := h.a.Engage.Load(ctx); err != nil {
		return err
	}
	return h.a.Graph.Load(ctx)
}

// AdvanceClock moves the fake clock forward.
func (h *Hooks) AdvanceClock(dur time.Duration) error {
	if h.a.fake == nil {
		return errors.New("wire: no fake clock")
	}
	h.a.fake.Advance(dur)
	return nil
}

// Seed loads testdata/fixtures/<fixture>.json.
func (h *Hooks) Seed(ctx context.Context, fixture string) error {
	return h.a.DevSeed.LoadFixture(ctx, fixture)
}

// Login creates a user session for handle directly (fresh auth) and returns the
// cookie token and its CSRF token.
func (h *Hooks) Login(ctx context.Context, handle string) (string, string, error) {
	id, ok := h.a.Users.ByHandle(ctx, strings.ToLower(strings.TrimPrefix(handle, "@")))
	if !ok {
		return "", "", gate.ErrNotFound
	}
	if _, err := h.a.d.DB.Q().Exec(ctx, "UPDATE users SET status = 0 WHERE id = $1 AND status = 1", int64(id)); err != nil {
		return "", "", err
	}
	h.a.Users.Invalidate(id)
	tok, err := h.a.Auth.IssueSession(ctx, nil, core.SessUser, id, h.a.d.Clock.Now())
	if err != nil {
		return "", "", err
	}
	csrf, err := h.a.Auth.CSRFForToken(ctx, tok)
	return tok, csrf, err
}

// GenesisToken seeds a genesis user and returns its enrollment token.
func (h *Hooks) GenesisToken(ctx context.Context, handle string) (string, error) {
	return h.a.Auth.GenesisToken(ctx, handle)
}
