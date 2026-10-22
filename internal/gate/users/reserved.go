package users

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Reserved handles can never be registered, nor can their lookalikes (see
// reservedHandle).
var Reserved = map[string]bool{
	"admin": true, "administrator": true, "mod": true, "mods": true, "moderator": true, "moderators": true,
	"genesis": true, "pcc": true, "root": true, "system": true, "support": true, "staff": true, "help": true,
	"security": true, "official": true, "deleted": true, "anonymous": true, "everyone": true, "null": true,
	"undefined": true, "api": true, "www": true, "terms": true, "abuse": true,
	"admins": true, "sysadmin": true, "superuser": true, "moderation": true, "owner": true, "operator": true,
	"safety": true, "trust_and_safety": true, "verified": true, "webmaster": true, "postmaster": true,
	"hostmaster": true, "noreply": true, "no_reply": true, "contact": true, "announcements": true,
	"purplechickencult": true, "purple_chicken_cult": true,
	// The SPA's top-level routes (web/src/features/*/routes.tsx) and server paths.
	"about": true, "blame": true, "compose": true, "cosign": true, "enroll": true, "friends": true,
	"invite": true, "invites": true, "join": true, "login": true, "logout": true, "media": true,
	"messages": true, "notifications": true, "people": true, "recover": true, "recovery": true,
	"report": true, "reports": true, "saves": true, "seen": true, "settings": true, "sound": true,
	"suggestions": true, "tree": true, "assets": true, "auth": true, "static": true, "feed": true,
	"signup": true, "register": true,
}

// reservedRoots may not appear as any underscore-separated part of a handle,
// with or without trailing digits ("mod_team", "the_staff", "admin2").
var reservedRoots = []string{"admin", "administrator", "admins", "mod", "mods", "moderator", "moderators",
	"moderation", "staff", "support", "system", "sysadmin", "root", "official", "security", "pcc", "genesis"}

// reservedAffixes may not start or end a handle at all ("superadmin", "admin_x").
var reservedAffixes = []string{"admin", "moderator"}

var reservedSkeletons, rootSkeletons = func() (map[string]bool, map[string]bool) {
	all, roots := map[string]bool{}, map[string]bool{}
	for h := range Reserved {
		all[skeleton(h)] = true
	}
	for _, r := range reservedRoots {
		roots[skeleton(r)] = true
	}
	return all, roots
}()

// skeleton folds a canonical handle to its look: underscores dropped, digits
// read as the letters they imitate, l as i, rn as m and vv as w.
func skeleton(c string) string {
	c = strings.NewReplacer("_", "", "0", "o", "1", "i", "l", "i", "3", "e", "4", "a", "5", "s", "7", "t",
		"8", "b", "9", "g").Replace(c)
	return strings.NewReplacer("rn", "m", "vv", "w").Replace(c)
}

// reservedHandle reports whether canonical handle c is reserved or imitates a
// reserved handle.
func reservedHandle(c string) bool {
	sk := skeleton(c)
	if Reserved[c] || reservedSkeletons[sk] {
		return true
	}
	for _, a := range reservedAffixes {
		if strings.HasPrefix(sk, a) || strings.HasSuffix(sk, a) {
			return true
		}
	}
	for _, part := range strings.Split(c, "_") {
		if rootSkeletons[skeleton(strings.TrimRight(part, "0123456789"))] {
			return true
		}
	}
	return false
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
// settings reach it through core.DisplayNames).
func (s *Service) CleanDisplayName(name string) (string, bool) { return CleanDisplayName(name) }
