package posts

import (
	"strings"
	"unicode/utf8"

	"github.com/Nibroc6/purple-chicken-cult/internal/platform/mdscan"
)

// MaxMentions bounds the mentions resolved (and notified) per post.
const MaxMentions = 50

// mention is one @handle occurrence: its ordinal among all occurrences in the
// body and its canonical handle.
type mention struct {
	Ord    int
	Handle string
}

// scanMentions returns the body's @handle occurrences. It uses platform/mdscan
// (P0-W09) and falls back to a local scanner while mdscan is the scaffold stub
// that finds nothing (adapter, to be dropped at INT-0 once mdscan lands): the
// fallback follows the same subset rules — handles [A-Za-z0-9_]{3,20} after a
// non-word character, nothing inside code spans or fenced code blocks.
func scanMentions(body string) []mention {
	r := mdscan.Scan(body)
	var out []mention
	for _, m := range r.Mentions {
		out = append(out, mention{Ord: m.Ord, Handle: m.Handle})
	}
	if len(out) == 0 && len(r.Links) == 0 {
		out = localMentions(body)
	}
	return out
}

func isWord(r rune) bool {
	return r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// localMentions is the fallback scanner of scanMentions.
func localMentions(body string) []mention {
	var out []mention
	ord := 0
	fence := ""
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimLeft(line, " ")
		if len(line)-len(t) <= 3 {
			for _, f := range []string{"```", "~~~"} {
				if strings.HasPrefix(t, f) {
					switch fence {
					case "":
						fence = f
					case f:
						fence = ""
						t = "" // the closing fence line itself holds no mention
					}
				}
			}
		}
		if fence != "" || t == "" {
			continue
		}
		prev := rune(-1)
		code := 0 // length of the open backtick run, 0 = outside a code span
		for i := 0; i < len(line); {
			c, w := utf8.DecodeRuneInString(line[i:])
			if c == '`' {
				n := 0
				for i+n < len(line) && line[i+n] == '`' {
					n++
				}
				switch code {
				case 0:
					if strings.Contains(line[i+n:], strings.Repeat("`", n)) {
						code = n
					}
				case n:
					code = 0
				}
				i += n
				prev = '`'
				continue
			}
			if code == 0 && c == '@' && (prev < 0 || !isWord(prev) && prev != '@') {
				j := i + 1
				for j < len(line) && j-i-1 < 21 && isWord(rune(line[j])) {
					j++
				}
				n := j - i - 1
				if n >= 3 && n <= 20 && (j == len(line) || !isWord(rune(line[j]))) {
					out = append(out, mention{Ord: ord, Handle: strings.ToLower(line[i+1 : j])})
					ord++
					prev = rune(line[j-1])
					i = j
					continue
				}
			}
			prev = c
			i += w
		}
	}
	return out
}
