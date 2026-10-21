package themes

import (
	"bytes"
	"encoding/json"
	"math"
	"regexp"
	"slices"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
)

// Limits of a theme (§15.2 A). A theme is design tokens only: a complete
// palette, a corner radius, a density and a font from the allowlist at a size
// no smaller than the floor. Nothing in it is CSS: the client maps each value
// onto its own custom properties, so a theme can never hide, move or restyle
// an integrity element beyond its colors, radius, spacing and font.
const (
	MaxTokens   = 2 << 10
	MinRadius   = 0
	MaxRadius   = 24
	MinFontSize = 14 // the size floor (integrity text stays at least 12 px: 0.875 × 14)
	MaxFontSize = 24
	// AA is the WCAG AA ratio for normal-size text; NonText is the WCAG 2.1
	// ratio for borders of controls (1.4.11).
	AA      = 4.5
	NonText = 3.0
)

// Fonts is the font allowlist: ids the client maps onto local font stacks (no
// font is ever downloaded, and no font name from the user reaches CSS).
var Fonts = []string{"system", "serif", "humanist", "rounded", "mono", "hyperlegible"}

// Densities are the spacing scales.
var Densities = []string{"compact", "comfortable", "spacious"}

// Colors is a complete palette, each "#rrggbb".
type Colors struct {
	Bg            string `json:"bg"`
	Surface       string `json:"surface"`
	Text          string `json:"text"`
	Muted         string `json:"muted"`
	Accent        string `json:"accent"`
	Border        string `json:"border"`
	IntegrityBg   string `json:"integrity_bg"`
	IntegrityText string `json:"integrity_text"`
}

// Tokens is the theme JSON schema (unknown keys are rejected). Optional
// fields left out keep the app's defaults.
type Tokens struct {
	Colors   *Colors `json:"colors"`
	Radius   *int    `json:"radius,omitempty"`
	Density  *string `json:"density,omitempty"`
	Font     *string `json:"font,omitempty"`
	FontSize *int    `json:"font_size,omitempty"`
}

// pair is a foreground that must stay legible on a background. Integrity
// elements come first: they decide the field a rejection names.
type pair struct {
	field  string
	fg, bg func(c *Colors) string
	min    float64
}

var pairs = []pair{
	// Every integrity element (tier headers, chain, inviter label, why, audience,
	// edited, report) is integrity_text on integrity_bg.
	{"colors.integrity_text", func(c *Colors) string { return c.IntegrityText }, func(c *Colors) string { return c.IntegrityBg }, AA},
	// Integrity buttons are outlined in integrity_text on the card and the page.
	{"colors.integrity_text", func(c *Colors) string { return c.IntegrityText }, func(c *Colors) string { return c.Surface }, NonText},
	{"colors.integrity_text", func(c *Colors) string { return c.IntegrityText }, func(c *Colors) string { return c.Bg }, NonText},
	{"colors.text", func(c *Colors) string { return c.Text }, func(c *Colors) string { return c.Surface }, AA},
	{"colors.text", func(c *Colors) string { return c.Text }, func(c *Colors) string { return c.Bg }, AA},
	{"colors.muted", func(c *Colors) string { return c.Muted }, func(c *Colors) string { return c.Surface }, AA},
	{"colors.muted", func(c *Colors) string { return c.Muted }, func(c *Colors) string { return c.Bg }, AA},
	{"colors.accent", func(c *Colors) string { return c.Accent }, func(c *Colors) string { return c.Surface }, AA},
	{"colors.accent", func(c *Colors) string { return c.Accent }, func(c *Colors) string { return c.Bg }, AA},
}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func invalid(field string) error {
	return gate.NewProblem(gate.StatusUnprocessable, "theme_invalid", field)
}

// Validate checks raw against the theme schema, the size floor, the font
// allowlist and the contrast rules, and returns its normalized JSON (lowercase
// colors). Problems: theme_invalid, theme_font, theme_contrast (the field
// names the color that failed).
func Validate(raw []byte) ([]byte, error) {
	if len(raw) > MaxTokens {
		return nil, invalid("tokens")
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, invalid("tokens")
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	var t Tokens
	if err := dec.Decode(&t); err != nil || dec.More() {
		return nil, invalid("tokens")
	}
	if t.Colors == nil {
		return nil, invalid("colors")
	}
	c := t.Colors
	for _, f := range []struct {
		name string
		v    *string
	}{
		{"bg", &c.Bg}, {"surface", &c.Surface}, {"text", &c.Text}, {"muted", &c.Muted},
		{"accent", &c.Accent}, {"border", &c.Border}, {"integrity_bg", &c.IntegrityBg}, {"integrity_text", &c.IntegrityText},
	} {
		if !hexColor.MatchString(*f.v) {
			return nil, invalid("colors." + f.name)
		}
		*f.v = string(bytes.ToLower([]byte(*f.v)))
	}
	if t.Radius != nil && (*t.Radius < MinRadius || *t.Radius > MaxRadius) {
		return nil, invalid("radius")
	}
	if t.Density != nil && !slices.Contains(Densities, *t.Density) {
		return nil, invalid("density")
	}
	if t.FontSize != nil && (*t.FontSize < MinFontSize || *t.FontSize > MaxFontSize) {
		return nil, invalid("font_size")
	}
	if t.Font != nil && !slices.Contains(Fonts, *t.Font) {
		return nil, gate.NewProblem(gate.StatusUnprocessable, "theme_font", "font")
	}
	for _, p := range pairs {
		if Contrast(p.fg(c), p.bg(c)) < p.min {
			return nil, gate.NewProblem(gate.StatusUnprocessable, "theme_contrast", p.field)
		}
	}
	return json.Marshal(t)
}

// Contrast is the WCAG 2 contrast ratio of two "#rrggbb" colors (1 to 21).
func Contrast(a, b string) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func luminance(hex string) float64 {
	ch := func(i int) float64 {
		v := float64(unhex(hex[i])<<4|unhex(hex[i+1])) / 255
		if v <= 0.04045 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*ch(1) + 0.7152*ch(3) + 0.0722*ch(5)
}

func unhex(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	default:
		return int(c-'A') + 10
	}
}
