package mdscan

import (
	"html"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// This file is mirrored line by line in web/src/markdown/scan.ts; change both
// together (the shared fixtures in testdata/markdown catch any drift).

type itemKind uint8

const (
	itemURL itemKind = iota + 1
	itemMention
)

type item struct {
	kind       itemKind
	start, end int // rune indexes into the run, end exclusive
}

// Mention handle bounds (plan §4.7 posts; handles are [a-z0-9_]{3,20}).
const (
	minHandle = 3
	maxHandle = 20
)

// scanRun finds bare scheme URLs and @mentions in one run of plain text. A
// literal character (lit[i]: written as a backslash escape or a character
// reference, e.g. "\\@name" or "&#64;name") never starts either.
func scanRun(run []rune, lit []bool) []item {
	var out []item
	prev := rune(-1)
	for i := 0; i < len(run); {
		c := run[i]
		if (c == 'h' || c == 'H') && !lit[i] && (prev < 0 || !isWord(prev)) {
			if j := schemeEnd(run, i); j > 0 && j < len(run) && isHostStart(run[j]) {
				k := j
				for k < len(run) && !isStop(run[k]) {
					k++
				}
				if end := trimURL(run, i, k); end > j {
					out = append(out, item{itemURL, i, end})
					prev = run[end-1]
					i = end
					continue
				}
			}
		}
		if c == '@' && !lit[i] && (prev < 0 || (!isWord(prev) && prev != '@')) {
			j := i + 1
			for j < len(run) && j-i-1 <= maxHandle && isWord(run[j]) {
				j++
			}
			if n := j - i - 1; n >= minHandle && n <= maxHandle && (j == len(run) || !isWord(run[j])) {
				out = append(out, item{itemMention, i, j})
				prev = run[j-1]
				i = j
				continue
			}
		}
		prev = c
		i++
	}
	return out
}

// schemeEnd returns the index after "http://" or "https://" at i
// (case-insensitive), or 0.
func schemeEnd(run []rune, i int) int {
	for _, s := range [...]string{"https://", "http://"} {
		if i+len(s) > len(run) {
			continue
		}
		ok := true
		for k := 0; k < len(s); k++ {
			if lower(run[i+k]) != rune(s[k]) {
				ok = false
				break
			}
		}
		if ok {
			return i + len(s)
		}
	}
	return 0
}

// isHTTP reports whether a destination is an http(s) URL with something after
// the scheme that can start a host.
func isHTTP(r []rune) bool {
	j := schemeEnd(r, 0)
	return j > 0 && j < len(r) && isHostStart(r[j])
}

// trimURL drops trailing punctuation from run[start:end] (and a closing
// parenthesis or bracket without its opener), returning the new end.
func trimURL(run []rune, start, end int) int {
	for end > start {
		switch c := run[end-1]; c {
		case '.', ',', ':', ';', '!', '?', '\'', '"', '*', '_', '~':
			end--
			continue
		case ')', ']':
			open := '('
			if c == ']' {
				open = '['
			}
			o, cl := 0, 0
			for _, x := range run[start:end] {
				switch x {
				case open:
					o++
				case c:
					cl++
				}
			}
			if cl > o {
				end--
				continue
			}
		}
		break
	}
	return end
}

func lower(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r + 'a' - 'A'
	}
	return r
}

// isWord: the ASCII word characters of handles.
func isWord(r rune) bool {
	return r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func isHostStart(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '[' ||
		(r >= 0x80 && !isSpace(r))
}

// isStop ends a bare URL: whitespace, controls, '<' and '>'.
func isStop(r rune) bool { return r < 0x20 || r == 0x7f || r == '<' || r == '>' || isSpace(r) }

// isSpace is JavaScript's \s (ECMAScript WhiteSpace + LineTerminator).
func isSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

func isASCIIPunct(b byte) bool {
	return strings.IndexByte("!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~", b) >= 0
}

var (
	digitalRE = regexp.MustCompile(`^&#((?i:x[a-f0-9]{1,6}|[0-9]{1,7}));`)
	namedRE   = regexp.MustCompile(`^&((?i)[a-z][a-z0-9]{1,31});`)
	allRE     = regexp.MustCompile("\\\\([!\"#$%&'()*+,\\-./:;<=>?@\\[\\\\\\]^_`{|}~])|&((?i)[a-z#][a-z0-9]{1,31});")
	digTestRE = regexp.MustCompile(`^#((?i:x[a-f0-9]{1,8}|[0-9]{1,8}))$`)
)

// decodeEntity decodes the character reference at the start of b the way
// markdown-it's inline entity rule does (text: an invalid numeric reference
// becomes U+FFFD) and returns the decoded runes and the bytes consumed (0 when
// b does not start with a reference).
func decodeEntity(b []byte, inText bool) ([]rune, int) {
	if m := digitalRE.FindSubmatch(b); m != nil {
		code := parseCode(string(m[1]))
		if validEntityCode(code) {
			return []rune{rune(code)}, len(m[0])
		}
		if inText {
			return []rune{utf8.RuneError}, len(m[0])
		}
		return nil, 0
	}
	if m := namedRE.FindSubmatch(b); m != nil {
		if dec, ok := namedEntity(string(m[0])); ok {
			return []rune(dec), len(m[0])
		}
	}
	return nil, 0
}

// unescapeAll decodes backslash escapes and character references in a link
// destination, like markdown-it's unescapeAll (an invalid numeric reference
// stays literal).
func unescapeAll(s string) string {
	if !strings.ContainsAny(s, "\\&") {
		return s
	}
	return allRE.ReplaceAllStringFunc(s, func(m string) string {
		if m[0] == '\\' {
			return m[1:]
		}
		name := m[1 : len(m)-1]
		if name[0] == '#' && digTestRE.MatchString(name) {
			if code := parseCode(name[1:]); validEntityCode(code) {
				return string(rune(code))
			}
			return m
		}
		if dec, ok := namedEntity(m); ok {
			return dec
		}
		return m
	})
}

// namedEntity decodes "&name;" only when name (with its semicolon) is an HTML5
// named reference, like markdown-it's decodeHTMLStrict: html.UnescapeString
// also accepts legacy prefixes ("&notit;" → "¬it;"), which leave at least two
// raw characters behind, while a full match yields one or two runes.
func namedEntity(m string) (string, bool) {
	dec := html.UnescapeString(m)
	if dec == m || utf8.RuneCountInString(dec) > 2 {
		return "", false
	}
	return dec, true
}

func parseCode(s string) int64 {
	base := 10
	if s[0] == 'x' || s[0] == 'X' {
		s, base = s[1:], 16
	}
	n, err := strconv.ParseInt(s, base, 64)
	if err != nil {
		return -1
	}
	return n
}

// validEntityCode mirrors markdown-it's isValidEntityCode.
func validEntityCode(c int64) bool {
	switch {
	case c >= 0xd800 && c <= 0xdfff, c >= 0xfdd0 && c <= 0xfdef, c&0xffff == 0xffff, c&0xffff == 0xfffe,
		c >= 0 && c <= 8, c == 11, c >= 14 && c <= 31, c >= 127 && c <= 159, c > 0x10ffff, c < 0:
		return false
	}
	return true
}
