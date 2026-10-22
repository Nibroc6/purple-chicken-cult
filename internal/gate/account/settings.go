package account

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"

	"github.com/jackc/pgx/v5"
	"golang.org/x/text/language"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// Settings limits.
const (
	MaxLanguages = 10
	MaxTagLen    = 35
	MaxUIPrefs   = 4 << 10
)

// Closed value sets of ui_prefs (§10.7, §15.1).
var (
	Formats    = []string{"cards", "compact", "explorer"} // explorer: P1-W09 (steward: p1-w09-1)
	Densities  = []string{"comfortable", "compact"}
	NavLayouts = []string{"sidebar", "header"}
	Contrasts  = []string{"normal", "high"}
	TextScales = []string{"small", "normal", "large", "xlarge"}
)

// UIPrefs is the ui_prefs JSON schema (unknown keys are rejected).
type UIPrefs struct {
	Format  *string    `json:"format,omitempty"`
	Nav     *string    `json:"nav,omitempty"`
	Density *string    `json:"density,omitempty"`
	A11y    *A11yPrefs `json:"a11y,omitempty"`
}

// A11yPrefs are the accessibility switches applied to every format.
type A11yPrefs struct {
	Contrast      *string `json:"contrast,omitempty"`
	TextScale     *string `json:"text_scale,omitempty"`
	ReducedMotion *bool   `json:"reduced_motion,omitempty"`
	DyslexiaFont  *bool   `json:"dyslexia_font,omitempty"`
}

func oneOf(p *string, set []string) bool { return p == nil || slices.Contains(set, *p) }

// ValidateUIPrefs checks raw against the ui_prefs schema and returns its
// normalized JSON. Problem ui_prefs_invalid.
func ValidateUIPrefs(raw []byte) ([]byte, error) {
	bad := gate.NewProblem(gate.StatusUnprocessable, "ui_prefs_invalid", "ui_prefs")
	if len(raw) > MaxUIPrefs {
		return nil, bad
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, bad
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	var p UIPrefs
	if err := dec.Decode(&p); err != nil || dec.More() {
		return nil, bad
	}
	if !oneOf(p.Format, Formats) || !oneOf(p.Nav, NavLayouts) || !oneOf(p.Density, Densities) {
		return nil, bad
	}
	if p.A11y != nil && (!oneOf(p.A11y.Contrast, Contrasts) || !oneOf(p.A11y.TextScale, TextScales)) {
		return nil, bad
	}
	return json.Marshal(p)
}

// ParseTag validates a BCP-47 language tag and returns its canonical form.
func ParseTag(s string) (string, bool) {
	if s == "" || len(s) > MaxTagLen {
		return "", false
	}
	t, err := language.Parse(s)
	if err != nil || t == language.Und {
		return "", false
	}
	return t.String(), true
}

// UpdateSettings validates and applies a settings patch (nil fields are left
// unchanged). Problems: display_name_invalid, locale_invalid, lang_invalid,
// translate_to_not_in_languages, preset_unavailable (unknown),
// ui_prefs_invalid. An empty translate_to clears it; a languages patch that drops
// the current translate_to clears it too.
func (s *Service) UpdateSettings(ctx context.Context, p gate.SettingsPatch) (gate.SettingsView, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return gate.SettingsView{}, err
	}
	var out settingsRow
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		r, err := loadSettings(ctx, tx, v.ID, true)
		if err != nil {
			return err
		}
		if r.status == int16(core.StatusDeleted) {
			return gate.ErrNotFound
		}
		if err := s.apply(&r, p); err != nil {
			return err
		}
		var translateTo *string
		if r.translateTo != nil && *r.translateTo != "" {
			translateTo = r.translateTo
		}
		r.translateTo = translateTo
		_, err = tx.Exec(ctx, `UPDATE users SET display_name = $2, ui_locale = $3, languages = $4, translate_to = $5,
			autotranslate = $6, rank_preset = $7, name_in_chains = $8, suggestible = $9, named_in_likes = $10,
			slippery_default = $11, ui_prefs = $12 WHERE id = $1`,
			int64(v.ID), r.displayName, r.locale, r.languages, translateTo, r.autotranslate, r.preset, r.nameInChains,
			r.suggestible, r.namedInLikes, r.slippery, string(r.uiPrefs))
		out = r
		return err
	})
	if err != nil {
		return gate.SettingsView{}, err
	}
	s.d.Bus.Publish(core.UserChanged{ID: v.ID})
	return out.view(), nil
}

func (s *Service) apply(r *settingsRow, p gate.SettingsPatch) error {
	if p.DisplayName != nil {
		dn := s.d.DisplayNames
		if dn == nil {
			return errNoDep
		}
		name, valid := dn.CleanDisplayName(*p.DisplayName)
		if !valid {
			return gate.NewProblem(gate.StatusUnprocessable, "display_name_invalid", "display_name")
		}
		r.displayName = name
	}
	if p.UILocale != nil {
		tag, ok := ParseTag(*p.UILocale)
		if !ok {
			return gate.NewProblem(gate.StatusUnprocessable, "locale_invalid", "ui_locale")
		}
		r.locale = tag
	}
	if p.Languages != nil {
		in := *p.Languages
		if len(in) == 0 || len(in) > MaxLanguages {
			return gate.NewProblem(gate.StatusUnprocessable, "lang_invalid", "languages")
		}
		langs := make([]string, 0, len(in))
		for _, l := range in {
			tag, ok := ParseTag(l)
			if !ok {
				return gate.NewProblem(gate.StatusUnprocessable, "lang_invalid", "languages")
			}
			if !slices.Contains(langs, tag) {
				langs = append(langs, tag)
			}
		}
		r.languages = langs
		if p.TranslateTo == nil && r.translateTo != nil && !slices.Contains(langs, *r.translateTo) {
			r.translateTo = nil
		}
	}
	if p.TranslateTo != nil {
		if *p.TranslateTo == "" {
			r.translateTo = nil
		} else {
			tag, ok := ParseTag(*p.TranslateTo)
			if !ok || !slices.Contains(r.languages, tag) {
				return gate.NewProblem(gate.StatusUnprocessable, "translate_to_not_in_languages", "translate_to")
			}
			r.translateTo = &tag
		}
	}
	if p.Autotranslate != nil {
		r.autotranslate = *p.Autotranslate
	}
	if p.RankPreset != nil {
		pr, ok := core.ParsePreset(*p.RankPreset)
		if !ok {
			return gate.NewProblem(gate.StatusUnprocessable, "preset_unavailable", "rank_preset")
		}
		r.preset = int16(pr)
	}
	if p.NameInChains != nil {
		r.nameInChains = *p.NameInChains
	}
	if p.Suggestible != nil {
		r.suggestible = *p.Suggestible
	}
	if p.NamedInLikes != nil {
		r.namedInLikes = *p.NamedInLikes
	}
	if p.SlipperyDefault != nil {
		r.slippery = *p.SlipperyDefault
	}
	if p.UIPrefs != nil {
		prefs, err := ValidateUIPrefs(*p.UIPrefs)
		if err != nil {
			return err
		}
		r.uiPrefs = prefs
	}
	return nil
}
