package users

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Reserved handles can never be registered.
var Reserved = map[string]bool{
	"admin": true, "administrator": true, "mod": true, "mods": true, "moderator": true, "moderators": true,
	"genesis": true, "pcc": true, "root": true, "system": true, "support": true, "staff": true, "help": true,
	"security": true, "official": true, "deleted": true, "anonymous": true, "everyone": true, "null": true,
	"undefined": true, "api": true, "www": true, "terms": true, "abuse": true,
}

// ReservedGlyphs are code points reserved for integrity elements (§15.5): display
// names may not contain them, so user content cannot imitate a badge.
var ReservedGlyphs = []rune{'✓', '✔', '☑', '🛡', '✅', '☒', '⛨', '✗', '✘', '🔒', '🔐', '⚠'}

// MaxDisplayName is the display-name limit in code points (users.display_name CHECK).
const MaxDisplayName = 50

func reservedGlyph(r rune) bool {
	for _, g := range ReservedGlyphs {
		if r == g {
			return true
		}
	}
	return false
}

// CleanDisplayName validates and normalizes a display name: NFC, trimmed, at
// most MaxDisplayName code points, only graphic code points and plain spaces
// (no control, format — bidi overrides, zero-width —, private-use, surrogate or
// unassigned code points, no other whitespace), none of the ReservedGlyphs and
// no emoji variation selectors. The empty name is valid (the handle shows).
func CleanDisplayName(s string) (string, bool) {
	if !utf8.ValidString(s) {
		return "", false
	}
	s = norm.NFC.String(strings.TrimSpace(s))
	if utf8.RuneCountInString(s) > MaxDisplayName {
		return "", false
	}
	for _, r := range s {
		switch {
		case r == ' ':
		case unicode.IsSpace(r), !unicode.IsGraphic(r):
			return "", false
		case r == '️' || r == '︎':
			return "", false
		case reservedGlyph(r):
			return "", false
		}
	}
	return s, true
}

// CleanDisplayName exposes the display-name rule on the directory (account
// settings reach it through core.Users; see docs/contract-requests/p0-w11-1.md).
func (s *Service) CleanDisplayName(name string) (string, bool) { return CleanDisplayName(name) }
