package users

// Reserved handles can never be registered.
var Reserved = map[string]bool{
	"admin": true, "administrator": true, "mod": true, "mods": true, "moderator": true, "moderators": true,
	"genesis": true, "pcc": true, "root": true, "system": true, "support": true, "staff": true, "help": true,
	"security": true, "official": true, "deleted": true, "anonymous": true, "everyone": true, "null": true,
	"undefined": true, "api": true, "www": true, "terms": true, "abuse": true,
}

// ReservedGlyphs are code points reserved for integrity elements (§15.5): display
// names may not contain them, so user content cannot imitate a badge.
var ReservedGlyphs = []rune{'✓', '✔', '☑', '🛡', '✅', '☒', '⛨'}
