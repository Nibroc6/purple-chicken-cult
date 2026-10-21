package themes_test

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/themes"
)

const good = `{"colors":{"bg":"#FBFAFF","surface":"#ffffff","text":"#1b1530","muted":"#5c5470","accent":"#6b2fd6",` +
	`"border":"#e3dcf3","integrity_bg":"#efe8ff","integrity_text":"#2c1470"},"radius":12,"density":"comfortable","font":"system","font_size":16}`

// with replaces one "key":value pair of good.
func with(old, repl string) string {
	if !strings.Contains(good, old) {
		panic(old)
	}
	return strings.Replace(good, old, repl, 1)
}

func problem(t *testing.T, err error) *gate.Problem {
	t.Helper()
	var p *gate.Problem
	if !errors.As(err, &p) {
		t.Fatalf("err = %v, want a Problem", err)
	}
	if p.Status != gate.StatusUnprocessable {
		t.Fatalf("status = %d", p.Status)
	}
	return p
}

func TestContrast(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want float64
	}{
		{"#000000", "#ffffff", 21},
		{"#ffffff", "#ffffff", 1},
		{"#777777", "#ffffff", 4.48}, // the classic just-fails-AA gray
		{"#767676", "#FFFFFF", 4.54},
	} {
		if got := themes.Contrast(c.a, c.b); math.Abs(got-c.want) > 0.01 {
			t.Errorf("Contrast(%s, %s) = %.3f, want %.2f", c.a, c.b, got, c.want)
		}
		if themes.Contrast(c.a, c.b) != themes.Contrast(c.b, c.a) {
			t.Errorf("Contrast(%s, %s) is not symmetric", c.a, c.b)
		}
	}
}

func TestValidateNormalizes(t *testing.T) {
	out, err := themes.Validate([]byte(good))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"bg":"#fbfaff"`) {
		t.Fatalf("colors not lowercased: %s", out)
	}
	// Optional tokens may be left out.
	min := `{"colors":{"bg":"#ffffff","surface":"#ffffff","text":"#000000","muted":"#333333","accent":"#0040c0","border":"#000000","integrity_bg":"#fff2a8","integrity_text":"#000000"}}`
	out, err = themes.Validate([]byte(min))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != min {
		t.Fatalf("minimal theme = %s", out)
	}
}

func TestValidateRejects(t *testing.T) {
	for _, c := range []struct {
		name, raw, code, field string
	}{
		// The acceptance case: a tier header (integrity text on integrity bg) below AA.
		{"low-contrast tier header", with(`"integrity_text":"#2c1470"`, `"integrity_text":"#b8a8e8"`), "theme_contrast", "colors.integrity_text"},
		{"integrity text on its own color", with(`"integrity_text":"#2c1470"`, `"integrity_text":"#efe8ff"`), "theme_contrast", "colors.integrity_text"},
		{"integrity outline lost on the card", with(`"integrity_bg":"#efe8ff","integrity_text":"#2c1470"`, `"integrity_bg":"#000000","integrity_text":"#fefefe"`), "theme_contrast", "colors.integrity_text"},
		{"body text", with(`"text":"#1b1530"`, `"text":"#aaaaaa"`), "theme_contrast", "colors.text"},
		{"muted text", with(`"muted":"#5c5470"`, `"muted":"#a09ab0"`), "theme_contrast", "colors.muted"},
		{"links", with(`"accent":"#6b2fd6"`, `"accent":"#c0a0ff"`), "theme_contrast", "colors.accent"},
		{"font outside the allowlist", with(`"font":"system"`, `"font":"Comic Sans MS"`), "theme_font", "font"},
		{"font with CSS in it", with(`"font":"system"`, `"font":"system;background:url(x)"`), "theme_font", "font"},
		{"below the size floor", with(`"font_size":16`, `"font_size":12`), "theme_invalid", "font_size"},
		{"above the size cap", with(`"font_size":16`, `"font_size":40`), "theme_invalid", "font_size"},
		{"fractional size", with(`"font_size":16`, `"font_size":15.5`), "theme_invalid", "tokens"},
		{"radius", with(`"radius":12`, `"radius":99`), "theme_invalid", "radius"},
		{"density", with(`"density":"comfortable"`, `"density":"tiny"`), "theme_invalid", "density"},
		{"named color", with(`"bg":"#FBFAFF"`, `"bg":"white"`), "theme_invalid", "colors.bg"},
		{"short hex", with(`"bg":"#FBFAFF"`, `"bg":"#fff"`), "theme_invalid", "colors.bg"},
		{"css value", with(`"border":"#e3dcf3"`, `"border":"#e3dcf3;display:none"`), "theme_invalid", "colors.border"},
		{"missing color", with(`"border":"#e3dcf3",`, ``), "theme_invalid", "colors.border"},
		{"unknown token", with(`"radius":12`, `"radius":12,"css":".integrity{display:none}"`), "theme_invalid", "tokens"},
		{"unknown color", with(`"bg":"#FBFAFF"`, `"bg":"#FBFAFF","tier_header":"#ffffff"`), "theme_invalid", "tokens"},
		{"no colors", `{"radius":4}`, "theme_invalid", "colors"},
		{"not an object", `[1]`, "theme_invalid", "tokens"},
		{"empty", ``, "theme_invalid", "tokens"},
		{"trailing data", good + `{}`, "theme_invalid", "tokens"},
		{"too large", `{"colors":{` + strings.Repeat(" ", themes.MaxTokens) + `}}`, "theme_invalid", "tokens"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := themes.Validate([]byte(c.raw))
			p := problem(t, err)
			if p.Code != c.code || p.Field != c.field {
				t.Fatalf("problem = %s/%s, want %s/%s", p.Code, p.Field, c.code, c.field)
			}
		})
	}
}

// TestPresets: every preset the theme page offers passes the server's rules.
func TestPresets(t *testing.T) {
	raw, err := os.ReadFile("../../../web/src/features/themes/presets.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Presets []struct {
			ID     string          `json:"id"`
			Tokens json.RawMessage `json:"tokens"`
		} `json:"presets"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Presets) < 4 {
		t.Fatalf("%d presets", len(f.Presets))
	}
	for _, p := range f.Presets {
		if _, err := themes.Validate(p.Tokens); err != nil {
			t.Errorf("preset %s: %v", p.ID, err)
		}
	}
}
