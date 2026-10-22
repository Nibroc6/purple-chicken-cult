package themes_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/themes"
)

type sheetVectors struct {
	Colors themes.Colors `json:"colors"`
	Images []string      `json:"images"`
	Cases  []struct {
		Name  string `json:"name"`
		CSS   string `json:"css"`
		Out   string `json:"out"`
		Error string `json:"error"`
		Field string `json:"field"`
	} `json:"cases"`
}

// The vectors are shared with the client mirror (sheet.ts).
func TestSheetVectors(t *testing.T) {
	raw, err := os.ReadFile("../../../web/src/features/themes/sheet.vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v sheetVectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	images := map[string]bool{}
	for _, n := range v.Images {
		images[n] = true
	}
	for _, c := range v.Cases {
		rules, err := themes.ParseSheet(c.CSS, images)
		if err == nil {
			err = themes.CheckSheet(rules, &v.Colors)
		}
		if c.Error != "" {
			if err == nil {
				t.Errorf("%s: accepted, want %s %s", c.Name, c.Error, c.Field)
				continue
			}
			p := problem(t, err)
			if p.Code != c.Error || p.Field != c.Field {
				t.Errorf("%s: %s %s, want %s %s", c.Name, p.Code, p.Field, c.Error, c.Field)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.Name, err)
			continue
		}
		if got := themes.FormatSheet(rules); got != c.Out {
			t.Errorf("%s:\n got %q\nwant %q", c.Name, got, c.Out)
			continue
		}
		// The normalized source parses to itself.
		again, err := themes.ParseSheet(c.Out, images)
		if err != nil || themes.FormatSheet(again) != c.Out {
			t.Errorf("%s: normalized source does not round-trip (%v)", c.Name, err)
		}
	}
}

func TestSheetLimits(t *testing.T) {
	if _, err := themes.ParseSheet(strings.Repeat(" ", themes.MaxSheet+1), nil); problem(t, err).Field != "css.0" {
		t.Error("an oversized sheet is refused as a whole")
	}
	many := strings.Repeat("card { border-width: 1px }\n", themes.MaxRules+1)
	if _, err := themes.ParseSheet(many, nil); problem(t, err).Field != "css.101" {
		t.Errorf("rule %d is one too many: %v", themes.MaxRules+1, err)
	}
	decls := "card {" + strings.Repeat(" border-width: 1px;", themes.MaxDecls+1) + "}"
	if _, err := themes.ParseSheet(decls, nil); err == nil {
		t.Error("too many declarations in a rule")
	}
}

// Every part and every property has a client mapping; a part named like an
// integrity element would be a way to restyle one.
func TestPartsAreNotIntegrity(t *testing.T) {
	for _, n := range []string{"tier-header", "chain", "inviter", "why", "audience", "edited", "report", "integrity"} {
		if _, ok := themes.Parts[n]; ok {
			t.Errorf("part %q names an integrity element", n)
		}
	}
	for _, p := range []string{"display", "position", "opacity", "visibility", "transform", "filter", "content", "z-index",
		"clip-path", "max-block-size", "max-inline-size", "font-size", "inset", "overflow", "mask", "cursor", "pointer-events"} {
		if _, ok := themes.Props[p]; ok {
			t.Errorf("property %q could hide, move or cover an element", p)
		}
	}
}
