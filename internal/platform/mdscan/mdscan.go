// Package mdscan is the server's view of the Markdown subset (ARCHITECTURE
// §10.1.6): links (scheme-only autolinks) and @mentions with code-point offsets,
// ignoring code spans/blocks. It must agree with the web renderer on the shared
// fixtures in testdata/markdown/*.json.
//
// Owner: P0-W09 → P1-W11. SCAFFOLD STUB: finds nothing.
package mdscan

// Link is a URL found in a post body.
type Link struct {
	Raw       string
	NoPreview bool // written as <url>
	Offset    int  // code points
}

// Mention is an @handle found in a post body.
type Mention struct {
	Handle string // canonical
	Ord    int
	Offset int // code points
}

// Result is what Scan finds.
type Result struct {
	Links    []Link
	Mentions []Mention
}

// Scan extracts links and mentions from body. Scaffold: returns nothing.
func Scan(body string) Result { return Result{} }
