// Package themes stores token-only themes (ARCHITECTURE §15.2 A), validated
// server-side: WCAG AA for integrity elements, a size floor, a font allowlist
// (tokens.go). One theme per user in user_themes; no theme means the app's
// default tokens. The theme is the reader's own: nobody else ever sees it.
//
// Owner: P1-W09.
package themes

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// Service is the themes module.
type Service struct{ d *core.Deps }

// View is the viewer's stored theme.
type View struct {
	Tokens    json.RawMessage `json:"tokens"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// New returns the service.
func New(d *core.Deps) *Service { return &Service{d: d} }

// Init does nothing: the table cascades with its user.
func (s *Service) Init(context.Context) error { return nil }

// Get returns the viewer's theme, nil when the viewer uses the default.
func (s *Service) Get(ctx context.Context) (*View, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return nil, err
	}
	var out View
	err = s.d.DB.Q().QueryRow(ctx, `SELECT tokens, updated_at FROM user_themes WHERE user_id = $1`, int64(v.ID)).
		Scan(&out.Tokens, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// Set validates and stores the viewer's theme tokens, replacing any earlier
// theme. Problems: theme_contrast, theme_font, theme_invalid.
func (s *Service) Set(ctx context.Context, tokens json.RawMessage) (*View, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return nil, err
	}
	norm, err := Validate(tokens)
	if err != nil {
		return nil, err
	}
	out := View{Tokens: norm, UpdatedAt: s.d.Clock.Now().UTC().Truncate(time.Microsecond)}
	if _, err := s.d.DB.Q().Exec(ctx, `INSERT INTO user_themes (user_id, tokens, updated_at) VALUES ($1, $2, $3)
		ON CONFLICT (user_id) DO UPDATE SET tokens = EXCLUDED.tokens, updated_at = EXCLUDED.updated_at`,
		int64(v.ID), norm, out.UpdatedAt); err != nil {
		return nil, err
	}
	return &out, nil
}

// Clear resets the viewer's theme to the default.
func (s *Service) Clear(ctx context.Context) error {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return err
	}
	_, err = s.d.DB.Q().Exec(ctx, `DELETE FROM user_themes WHERE user_id = $1`, int64(v.ID))
	return err
}
