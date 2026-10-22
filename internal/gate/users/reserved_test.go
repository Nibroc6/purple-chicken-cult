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

func TestCanonReserved(t *testing.T) {
	bad := []string{
		"admin", "ADMIN", "Admin", "@admin", "ａｄｍｉｎ", "аdmin", "mod", "MOD", "moderator", "staff", "support",
		"system", "root", "official", "pcc", "login", "settings", "people", "invite", "join", "messages",
		"adm1n", "4dmin", "adrnin", "m0d", "r00t", "5taff", "l0gin", "s3ttings", "admin_", "_admin", "a_d_m_i_n",
		"mod1", "mod_team", "the_mod", "the_staff", "staff2", "support_desk", "system_bot", "official_news",
		"superadmin", "admin123", "moderatorbob", "headmoderator", "pcc_team", "genesis_1",
	}
	for _, h := range bad {
		if c, ok := Canon(h); ok {
			t.Errorf("Canon(%q) accepted as %q", h, c)
		}
	}
	ok := []string{"alice", "bob_99", "modern_art", "model", "moderna", "rooted", "staffordshire", "systematic",
		"supportive", "badminton_fan", "carl", "helper", "mode", "joiner", "abc"}
	for _, h := range ok {
		if _, valid := Canon(h); !valid {
			t.Errorf("Canon(%q) rejected", h)
		}
	}
}
