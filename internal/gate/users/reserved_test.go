package users

import (
	"strings"
	"testing"
)

func TestCleanDisplayName(t *testing.T) {
	ok := map[string]string{
		"":                                  "",
		"  Alice  ":                         "Alice",
		"Zoë Ⅱ":                             "Zoë Ⅱ",
		"e\u0301":                           "\u00e9", // NFC
		"李小龍":                               "李小龍",
		strings.Repeat("a", MaxDisplayName): strings.Repeat("a", MaxDisplayName),
	}
	for in, want := range ok {
		got, valid := CleanDisplayName(in)
		if !valid || got != want {
			t.Errorf("CleanDisplayName(%q) = %q, %v; want %q", in, got, valid, want)
		}
	}
	bad := []string{
		strings.Repeat("a", MaxDisplayName+1),
		"Alice ✓", "Bob ✔", "☑ verified", "🛡 mod", "✅", "Alice\u202Eecila", "Al\u200Bice", "Al\u200Dice",
		"tab\tname", "new\nline", "nbsp\u00a0name", "\uFEFFbom", "priv\uE000", "\u2713\uFE0F", "bad\xffutf8",
	}
	for _, in := range bad {
		if got, valid := CleanDisplayName(in); valid {
			t.Errorf("CleanDisplayName(%q) accepted as %q", in, got)
		}
	}
	for _, g := range ReservedGlyphs {
		if _, valid := CleanDisplayName("x" + string(g)); valid {
			t.Errorf("reserved glyph %q accepted", g)
		}
	}
}
