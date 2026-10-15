// Package urlx canonicalizes URLs (tracking parameters stripped, IDNA→ASCII), derives
// the trusted display host with a mixed-script "lookalike" flag, and cleans untrusted
// text (ARCHITECTURE §10.1.6). The tracking-parameter list lives in
// testdata/tracking-params.json, shared with the web client.
//
// Owner: P0-W09 → P1-W11. SCAFFOLD STUB: Canonicalize refuses everything.
package urlx

import (
	"errors"
	"net/url"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// ErrNotImplemented is returned by scaffold stubs.
var ErrNotImplemented = errors.New("urlx: not implemented")

// Canonicalize parses and canonicalizes raw. Scaffold: always fails.
func Canonicalize(raw string) (*url.URL, error) { return nil, ErrNotImplemented }

// DisplayHost returns the Unicode host of u and whether it mixes scripts.
// Scaffold: returns the ASCII host and lookalike=true (fail safe).
func DisplayHost(u *url.URL) (string, bool) { return u.Hostname(), true }

// CleanText NFC-normalizes s and truncates it to max runes. The owning WP adds
// stripping of control, format, bidi and zero-width characters.
func CleanText(s string, max int) string {
	s = norm.NFC.String(strings.TrimSpace(s))
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max])
}
