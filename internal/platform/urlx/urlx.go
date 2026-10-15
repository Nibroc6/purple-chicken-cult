// Package urlx canonicalizes URLs (tracking parameters stripped, IDNA→ASCII),
// derives the trusted display host with a mixed-script "lookalike" flag, and
// cleans untrusted text (ARCHITECTURE §10.1.6, plan §4.2).
//
// The tracking-parameter list is testdata/tracking-params.json, shared with the
// web client; this package embeds a copy (tracking-params.json) and its tests
// fail when the copy drifts.
//
// The lookalike flag is mixed-script detection plus "whole label in a script
// whose letters all look Latin" (e.g. Cyrillic "аррӏе"); it is not a UTS #39
// confusables check (no such table ships with x/text), and the UI says "mixed
// scripts", never "safe". web/src/markdown/host.ts implements the same rules.
//
// Owner: P0-W09 → P1-W11.
package urlx

import (
	_ "embed"
	"encoding/json"
	"errors"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/idna"
	"golang.org/x/text/unicode/norm"
)

// MaxURL is the longest URL accepted (bytes).
const MaxURL = 2048

// Errors returned by Canonicalize.
var (
	ErrInvalid  = errors.New("urlx: invalid URL")
	ErrScheme   = errors.New("urlx: not http(s)")
	ErrUserinfo = errors.New("urlx: userinfo in URL")
)

//go:embed tracking-params.json
var trackingJSON []byte

// Tracking is the shared tracking-parameter list.
type Tracking struct {
	Exact    []string `json:"exact"`
	Prefixes []string `json:"prefixes"`
}

var tracking = func() Tracking {
	var t Tracking
	if err := json.Unmarshal(trackingJSON, &t); err != nil {
		panic("urlx: tracking-params.json: " + err.Error())
	}
	return t
}()

// TrackingParams returns the embedded tracking-parameter list.
func TrackingParams() Tracking { return tracking }

// IsTracking reports whether a query key is a tracking parameter.
func IsTracking(key string) bool {
	k := strings.ToLower(key)
	for _, e := range tracking.Exact {
		if k == e {
			return true
		}
	}
	for _, p := range tracking.Prefixes {
		if strings.HasPrefix(k, p) {
			return true
		}
	}
	return false
}

var lookup = idna.New(idna.MapForLookup(), idna.Transitional(false), idna.BidiRule())

// Canonicalize parses raw and returns its canonical form: http(s) only, no
// userinfo, lowercase ASCII (IDNA) host without a trailing dot, no default
// port, no fragment, "/" for an empty path, tracking parameters removed (the
// other parameters keep their order and encoding).
func Canonicalize(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > MaxURL {
		return nil, ErrInvalid
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, ErrInvalid
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, ErrScheme
	}
	if u.Opaque != "" {
		return nil, ErrInvalid
	}
	if u.User != nil {
		return nil, ErrUserinfo
	}
	host, port := u.Hostname(), u.Port()
	if host == "" {
		return nil, ErrInvalid
	}
	if strings.HasPrefix(u.Host, "[") {
		a, err := netip.ParseAddr(host)
		if err != nil || !a.Is6() || a.Zone() != "" {
			return nil, ErrInvalid
		}
		host = "[" + a.String() + "]"
	} else {
		host = strings.TrimSuffix(strings.ToLower(host), ".")
		if host, err = lookup.ToASCII(host); err != nil || host == "" {
			return nil, ErrInvalid
		}
	}
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return nil, ErrInvalid
		}
		port = strconv.Itoa(n)
		if (u.Scheme == "http" && n == 80) || (u.Scheme == "https" && n == 443) {
			port = ""
		}
	}
	u.Host = host
	if port != "" {
		u.Host += ":" + port
	}
	u.Fragment, u.RawFragment = "", ""
	if u.Path == "" {
		u.Path, u.RawPath = "/", ""
	}
	u.RawQuery = StripTracking(u.RawQuery)
	u.ForceQuery = false
	return u, nil
}

// StripTracking removes tracking parameters from a raw query string, keeping
// the others as written and in order.
func StripTracking(rawQuery string) string {
	if rawQuery == "" {
		return ""
	}
	parts := strings.Split(rawQuery, "&")
	kept := parts[:0]
	for _, p := range parts {
		if p == "" {
			continue
		}
		k, _, _ := strings.Cut(p, "=")
		if dk, err := url.QueryUnescape(k); err == nil {
			k = dk
		}
		if IsTracking(k) {
			continue
		}
		kept = append(kept, p)
	}
	return strings.Join(kept, "&")
}

// DisplayHost returns the Unicode form of u's host and whether it looks like
// another (mixed scripts, or a whole label of Latin lookalikes). An IP literal
// is shown as is; a host that does not decode is shown in ASCII and flagged.
func DisplayHost(u *url.URL) (string, bool) {
	host := strings.ToLower(u.Hostname())
	if _, err := netip.ParseAddr(host); err == nil {
		return host, false
	}
	uni, err := idna.Display.ToUnicode(host)
	if err != nil {
		return host, true
	}
	for _, label := range strings.Split(uni, ".") {
		if Lookalike(label) {
			return uni, true
		}
	}
	return uni, false
}

// Scripts considered by Lookalike, in a fixed order shared with host.ts. A
// letter outside all of them counts as script "other".
var scripts = []struct {
	name string
	tab  *unicode.RangeTable
}{
	{"Latin", unicode.Latin}, {"Cyrillic", unicode.Cyrillic}, {"Greek", unicode.Greek},
	{"Armenian", unicode.Armenian}, {"Georgian", unicode.Georgian}, {"Cherokee", unicode.Cherokee},
	{"Hebrew", unicode.Hebrew}, {"Arabic", unicode.Arabic}, {"Devanagari", unicode.Devanagari},
	{"Bengali", unicode.Bengali}, {"Thai", unicode.Thai}, {"Ethiopic", unicode.Ethiopic},
	{"Han", unicode.Han}, {"Hiragana", unicode.Hiragana}, {"Katakana", unicode.Katakana},
	{"Hangul", unicode.Hangul}, {"Bopomofo", unicode.Bopomofo},
}

// latinLike are the letters of other scripts that read as Latin letters; a
// label written only with them (and digits/hyphens) imitates a Latin name.
var latinLike = map[string]string{
	"Cyrillic": "аеорсухіјѕԁԛԝһӏ",
	"Greek":    "οανικρυχϲϳ",
	"Armenian": "օսցհո",
	"Cherokee": "ꭺꭰꮃꮪꮯꮇꭼꮋꮲꮶ",
}

// CJK combinations a single name legitimately mixes (UTS #39 "highly
// restrictive"): Latin with Han+Hiragana+Katakana, Han+Bopomofo or Han+Hangul.
var allowedMixes = [][]string{
	{"Latin", "Han", "Hiragana", "Katakana"},
	{"Latin", "Han", "Bopomofo"},
	{"Latin", "Han", "Hangul"},
}

func scriptOf(r rune) string {
	for _, s := range scripts {
		if unicode.Is(s.tab, r) {
			return s.name
		}
	}
	return "other"
}

// Lookalike reports whether one host label mixes scripts or imitates Latin.
func Lookalike(label string) bool {
	seen := map[string]bool{}
	for _, r := range label {
		if unicode.IsLetter(r) {
			seen[scriptOf(r)] = true
		}
	}
	if len(seen) > 1 {
		for _, mix := range allowedMixes {
			ok := true
			for s := range seen {
				if !contains(mix, s) {
					ok = false
					break
				}
			}
			if ok {
				return false
			}
		}
		return true
	}
	for s := range seen {
		set, ok := latinLike[s]
		if !ok {
			return false
		}
		for _, r := range label {
			if unicode.IsLetter(r) && !strings.ContainsRune(set, r) {
				return false
			}
		}
		return true
	}
	return false
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// CleanText NFC-normalizes untrusted text (preview titles and descriptions,
// display names), removes control and format characters — bidi overrides and
// isolates, zero-width characters, the BOM, soft hyphens, invisible fillers —
// collapses whitespace to single spaces, trims, and truncates to max code points.
func CleanText(s string, max int) string {
	out := make([]rune, 0, min(len(s), max+1))
	space := false
	for _, r := range norm.NFC.String(s) {
		switch {
		case unicode.IsSpace(r):
			space = true
			continue
		case r == utf8.RuneError || invisible(r):
			continue
		}
		if space && len(out) > 0 {
			out = append(out, ' ')
		}
		space = false
		out = append(out, r)
		if len(out) >= max {
			break
		}
	}
	if len(out) > max {
		out = out[:max]
	}
	return strings.TrimRight(string(out), " ")
}

// invisible: control, format (bidi controls, zero-width, BOM, soft hyphen, tag
// characters), private-use and surrogate code points, and the blank "letters"
// used to fake empty names.
func invisible(r rune) bool {
	switch r {
	case 0x115f, 0x1160, 0x3164, 0xffa0, 0x2800, 0x180e:
		return true
	}
	return unicode.In(r, unicode.Cc, unicode.Cf, unicode.Co, unicode.Cs)
}
