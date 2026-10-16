package posts

import (
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

// scanMentions returns the body's @handle occurrences, as platform/mdscan
// (P0-W09) finds them: the server and client Markdown subset agree on which
// text is a mention (nothing inside code, nothing escaped as `\@`).
func scanMentions(body string) []mention {
	r := mdscan.Scan(body)
	out := make([]mention, 0, len(r.Mentions))
	for _, m := range r.Mentions {
		out = append(out, mention{Ord: m.Ord, Handle: m.Handle})
	}
	return out
}
