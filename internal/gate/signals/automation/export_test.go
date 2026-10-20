package automation

import "strings"

// ClassifyForTest is classify of "METHOD /pattern" as {read, feed, seen, tree, chain}.
func ClassifyForTest(route string) [5]bool {
	m, p, _ := strings.Cut(route, " ")
	c := classify(m, p)
	return [5]bool{c.read, c.feed, c.seen, c.tree, c.chain}
}
