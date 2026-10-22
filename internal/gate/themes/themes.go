// Package themes stores themes (ARCHITECTURE §15.2 A), validated server-side:
// WCAG AA for integrity elements, a size floor, a font allowlist (tokens.go),
// and optionally an advanced mode: the owner's own raw theme CSS, which can
// use the theme's uploaded images through image(name) (sheet.go). One theme per user in user_themes (its
// images in theme_images); no theme means the app's default tokens. The theme
// is the reader's own: nobody else ever sees it.
//
// Owner: P1-W09.
package themes

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// Service is the themes module.
type Service struct{ d *core.Deps }

// View is the viewer's stored theme. CSS is the normalized theme CSS ("" for
// none) and Images the uploads it names, with the viewer's own refs.
type View struct {
	Tokens    json.RawMessage `json:"tokens"`
	CSS       string          `json:"css"`
	Images    []ImageView     `json:"images"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// ImageView is one image of a theme.
type ImageView struct {
	Name string   `json:"name"`
	Ref  gate.Ref `json:"ref"`
	W    int      `json:"w"`
	H    int      `json:"h"`
}

// ImageIn names one upload (or one of the theme's current images) for image(name).
type ImageIn struct {
	Name string   `json:"name"`
	Ref  gate.Ref `json:"ref"`
}

// Theme is what Set stores: tokens, and optionally theme CSS and its images.
type Theme struct {
	Tokens json.RawMessage
	CSS    string
	Images []ImageIn
}

// New returns the service.
func New(d *core.Deps) *Service { return &Service{d: d} }

// Init does nothing: the tables cascade with their user.
func (s *Service) Init(context.Context) error { return nil }

// Get returns the viewer's theme, nil when the viewer uses the default.
func (s *Service) Get(ctx context.Context) (*View, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return nil, err
	}
	var out View
	err = s.d.DB.Q().QueryRow(ctx, `SELECT tokens, css, updated_at FROM user_themes WHERE user_id = $1`, int64(v.ID)).
		Scan(&out.Tokens, &out.CSS, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	cur, err := s.images(ctx, s.d.DB.Q(), v.ID)
	if err != nil {
		return nil, err
	}
	out.Images = []ImageView{}
	if len(cur) == 0 || s.d.ThemeMedia == nil {
		return &out, nil
	}
	names := make([]string, 0, len(cur))
	ids := make([]uuid.UUID, 0, len(cur))
	for n, id := range cur {
		names = append(names, n)
		ids = append(ids, id)
	}
	views, err := s.d.ThemeMedia.Own(ctx, v.ID, ids)
	if err != nil {
		return nil, err
	}
	byID := map[uuid.UUID]gate.MediaView{}
	for _, mv := range views {
		if id, ok := s.d.Refs.OpenMedia(v.ID, mv.Ref); ok {
			byID[id] = mv
		}
	}
	for i, id := range ids {
		if mv, ok := byID[id]; ok {
			out.Images = append(out.Images, ImageView{Name: names[i], Ref: mv.Ref, W: mv.W, H: mv.H})
		}
	}
	slices.SortFunc(out.Images, func(a, b ImageView) int { return strings.Compare(a.Name, b.Name) })
	return &out, nil
}

// images are the viewer's current theme images by name.
func (s *Service) images(ctx context.Context, q store.Q, u core.UserID) (map[string]uuid.UUID, error) {
	rows, err := q.Query(ctx, `SELECT name, media_id FROM theme_images WHERE user_id = $1`, int64(u))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]uuid.UUID{}
	for rows.Next() {
		var (
			n  string
			id uuid.UUID
		)
		if err := rows.Scan(&n, &id); err != nil {
			return nil, err
		}
		out[n] = id
	}
	return out, rows.Err()
}

// Set validates and stores the viewer's theme, replacing any earlier theme.
// An image ref is either a fresh upload of the viewer's (attached to the
// theme) or one of the theme's current images; current images the new theme
// leaves out are released, and so are images its CSS does not use. Problems:
// theme_contrast (tokens), theme_css, theme_font, theme_invalid; gate.ErrNotFound for
// an image ref that resolves to neither; gate.Disabled("media") for images
// while media is off.
func (s *Service) Set(ctx context.Context, in Theme) (*View, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return nil, err
	}
	norm, err := Validate(in.Tokens)
	if err != nil {
		return nil, err
	}
	if len(in.Images) > MaxImages {
		return nil, invalid("images")
	}
	given := map[string]bool{}
	for _, im := range in.Images {
		if !ImageName.MatchString(im.Name) || given[im.Name] {
			return nil, invalid("images")
		}
		given[im.Name] = true
	}
	css, err := NormalizeSheet(in.CSS, given)
	if err != nil {
		return nil, err
	}
	used := SheetImages(css)
	if len(used) > 0 && (s.d.ThemeMedia == nil || s.d.Media == nil) {
		return nil, gate.Disabled("media")
	}
	out := View{Tokens: norm, CSS: css, UpdatedAt: s.d.Clock.Now().UTC().Truncate(time.Microsecond)}
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO user_themes (user_id, tokens, css, updated_at) VALUES ($1, $2, $3, $4)
			ON CONFLICT (user_id) DO UPDATE SET tokens = EXCLUDED.tokens, css = EXCLUDED.css, updated_at = EXCLUDED.updated_at`,
			int64(v.ID), norm, css, out.UpdatedAt); err != nil {
			return err
		}
		cur, err := s.images(ctx, tx, v.ID)
		if err != nil {
			return err
		}
		keep := map[uuid.UUID]string{}
		for _, im := range in.Images {
			if !used[im.Name] {
				continue
			}
			// A current image is kept as is; anything else must be a fresh
			// upload (Attach also answers for refs that do not open).
			id, ok := s.d.Refs.OpenMedia(v.ID, im.Ref)
			if !ok || !slices.Contains(mapValues(cur), id) {
				ids, err := s.d.Media.Attach(ctx, tx, v.ID, []gate.Ref{im.Ref})
				if err != nil {
					return err
				}
				id = ids[0]
			}
			if _, dup := keep[id]; dup {
				return invalid("images")
			}
			keep[id] = im.Name
		}
		var release []uuid.UUID
		for _, id := range cur {
			if _, ok := keep[id]; !ok {
				release = append(release, id)
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM theme_images WHERE user_id = $1`, int64(v.ID)); err != nil {
			return err
		}
		for id, name := range keep {
			if _, err := tx.Exec(ctx, `INSERT INTO theme_images (user_id, name, media_id) VALUES ($1, $2, $3)`,
				int64(v.ID), name, id); err != nil {
				return err
			}
		}
		if len(release) > 0 && s.d.ThemeMedia != nil {
			return s.d.ThemeMedia.Release(ctx, tx, v.ID, release)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx)
}

func mapValues(m map[string]uuid.UUID) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(m))
	for _, id := range m {
		out = append(out, id)
	}
	return out
}

// Clear resets the viewer's theme to the default and releases its images.
func (s *Service) Clear(ctx context.Context) error {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return err
	}
	return s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		cur, err := s.images(ctx, tx, v.ID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM theme_images WHERE user_id = $1`, int64(v.ID)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM user_themes WHERE user_id = $1`, int64(v.ID)); err != nil {
			return err
		}
		if len(cur) > 0 && s.d.ThemeMedia != nil {
			return s.d.ThemeMedia.Release(ctx, tx, v.ID, mapValues(cur))
		}
		return nil
	})
}
