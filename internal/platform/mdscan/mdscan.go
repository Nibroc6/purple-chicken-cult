// Package mdscan is the server's view of the Markdown subset (ARCHITECTURE
// §10.1.6): the http(s) links and @mentions of a post body, in document order,
// with code-point offsets. It must agree with the web renderer (src/markdown)
// on the shared fixtures in testdata/markdown/*.json.
//
// The structure comes from goldmark (CommonMark) with the same switches as the
// client's markdown-it: raw HTML off (it is plain text), strikethrough with
// exactly two tildes, no linkify extension. Bare URLs and mentions are found by
// one small scanner over each run of plain text, written identically in Go
// (scan.go) and TypeScript (src/markdown/scan.ts):
//
//   - links: explicit [text](url) and reference links, <url> autolinks
//     (NoPreview), ![alt](url) images (rendered as plain links, never fetched:
//     NoPreview) and bare URLs that start with the scheme ("http://" or
//     "https://", case-insensitive) — no "www." or bare-domain guessing on
//     either side. Only http(s) destinations count; everything else renders as
//     text on the client.
//   - tables: GFM tables at the top level, at most MaxTableCols columns, with
//     the stricter rules of table.go on both sides; their cells are scanned
//     like paragraphs.
//   - mentions: "@" + [A-Za-z0-9_]{3,20}, not preceded by a word character or
//     "@" and not followed by a word character; never inside code, links or
//     URLs. Ord numbers the mentions of a body from 0 (posts stores them by ord,
//     the client links them by ord from server-issued refs only).
//
// Offsets and lengths are Unicode code points (JS counts with [...s] /
// codePointAt, never .length).
//
// Owner: P0-W09 → P1-W11.
package mdscan

import (
	"strings"
	"unicode/utf8"

	"github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Link is an http(s) URL found in a post body.
type Link struct {
	Raw       string `json:"raw"`        // the destination as written, escapes and entities decoded
	NoPreview bool   `json:"no_preview"` // written as <url>, or an image
	Offset    int    `json:"offset"`     // code points: '[' / '!' / '<' / the scheme
}

// Mention is an @handle found in a post body.
type Mention struct {
	Handle string `json:"handle"` // canonical (lowercase)
	Ord    int    `json:"ord"`
	Offset int    `json:"offset"` // code points: the '@'
}

// Result is what Scan finds.
type Result struct {
	Links    []Link    `json:"links"`
	Mentions []Mention `json:"mentions"`
	Tables   []Table   `json:"tables,omitempty"` // the shape of each GFM table (table.go)
}

// md is the shared goldmark parser (safe for concurrent use): CommonMark
// without the raw-HTML block and inline parsers, plus two-tilde strikethrough
// and top-level GFM tables of at most MaxTableCols columns (table.go).
var md = func() parser.Parser {
	blocks := withoutHTMLBlock(parser.DefaultBlockParsers())
	inlines := withoutRawHTML(parser.DefaultInlineParsers())
	inlines = append(inlines, util.Prioritized(strikeParser{}, 500))
	return parser.NewParser(
		parser.WithBlockParsers(blocks...),
		parser.WithInlineParsers(inlines...),
		parser.WithParagraphTransformers(append(parser.DefaultParagraphTransformers(),
			util.Prioritized(tableTransformer{}, 200))...),
	)
}()

// The default HTML parsers are singletons, so they are dropped by identity.
func withoutHTMLBlock(bs []util.PrioritizedValue) []util.PrioritizedValue {
	html := parser.NewHTMLBlockParser()
	out := bs[:0:0]
	for _, b := range bs {
		if b.Value == html {
			continue
		}
		out = append(out, b)
	}
	return out
}

func withoutRawHTML(ps []util.PrioritizedValue) []util.PrioritizedValue {
	raw := parser.NewRawHTMLParser()
	out := ps[:0:0]
	for _, p := range ps {
		if p.Value == raw {
			continue
		}
		out = append(out, p)
	}
	return out
}

// Scan extracts the links and mentions of body.
func Scan(body string) Result {
	src := []byte(body)
	pc := parser.NewContext()
	doc := md.Parse(text.NewReader(src), parser.WithContext(pc))
	s := &scanner{src: src, cp: newCPIndex(src), pc: pc}
	s.blocks(doc)
	return s.res
}

type scanner struct {
	src []byte
	cp  cpIndex
	pc  parser.Context // the post's link reference definitions (for table cells)
	res Result
	run []rune
	lit []bool // the rune came from a backslash escape or a character reference
	off []int  // code-point offset of each rune of run
}

func (s *scanner) blocks(n ast.Node) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch c.Kind() {
		case ast.KindCodeBlock, ast.KindFencedCodeBlock, ast.KindHTMLBlock, ast.KindThematicBreak,
			ast.KindLinkReferenceDefinition:
			continue
		case ast.KindParagraph, ast.KindTextBlock, ast.KindHeading:
			s.inlines(c)
			s.flush()
		case kindTable:
			s.table(c.(*tableNode))
		default:
			s.blocks(c)
		}
	}
}

// inlines walks the inline children of n, building runs of plain text. Every
// non-text node ends the current run; links, images, autolinks and code are
// not scanned for mentions or bare URLs.
func (s *scanner) inlines(n ast.Node) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch v := c.(type) {
		case *ast.Text:
			s.text(v.Segment.Start, v.Segment.Stop)
			if v.SoftLineBreak() || v.HardLineBreak() {
				s.flush()
			}
		case *ast.String:
			// goldmark only creates these for typographer-style replacements,
			// which are off; keep them as plain text without offsets drift.
			s.flush()
		case *ast.Link:
			s.flush()
			s.link(string(v.Destination), false, c.Pos())
		case *ast.Image:
			s.flush()
			s.link(string(v.Destination), true, c.Pos())
		case *ast.AutoLink:
			s.flush()
			if v.AutoLinkType == ast.AutoLinkURL {
				s.link(string(v.URL(s.src)), true, c.Pos())
			}
		case *ast.CodeSpan, *ast.RawHTML:
			s.flush()
		case *ast.Emphasis, *east.Strikethrough:
			s.flush()
			s.inlines(c)
			s.flush()
		default:
			s.flush()
			s.inlines(c)
			s.flush()
		}
	}
}

// link records an explicit link, image or autolink destination.
func (s *scanner) link(dest string, noPreview bool, pos int) {
	raw := unescapeAll(dest)
	if !isHTTP([]rune(raw)) {
		return
	}
	off := 0
	if pos >= 0 {
		off = s.cp.at(pos)
	}
	s.res.Links = append(s.res.Links, Link{Raw: raw, NoPreview: noPreview, Offset: off})
}

// text appends the decoded source bytes [start, stop) to the current run.
func (s *scanner) text(start, stop int) {
	for i := start; i < stop; {
		r, w := utf8.DecodeRune(s.src[i:stop])
		if r == '\\' && i+1 < stop {
			n, nw := utf8.DecodeRune(s.src[i+1 : stop])
			if n < 0x80 && isASCIIPunct(byte(n)) {
				s.push(n, true, i)
				i += 1 + nw
				continue
			}
		}
		if r == '&' {
			if dec, n := decodeEntity(s.src[i:stop], true); n > 0 {
				for _, d := range dec {
					s.push(d, true, i)
				}
				i += n
				continue
			}
		}
		s.push(r, false, i)
		i += w
	}
}

func (s *scanner) push(r rune, lit bool, byteOff int) {
	s.run = append(s.run, r)
	s.lit = append(s.lit, lit)
	s.off = append(s.off, s.cp.at(byteOff))
}

// flush scans the current run and starts a new one.
func (s *scanner) flush() {
	if len(s.run) == 0 {
		return
	}
	for _, it := range scanRun(s.run, s.lit) {
		switch it.kind {
		case itemURL:
			s.res.Links = append(s.res.Links, Link{Raw: string(s.run[it.start:it.end]), Offset: s.off[it.start]})
		case itemMention:
			s.res.Mentions = append(s.res.Mentions, Mention{
				Handle: strings.ToLower(string(s.run[it.start+1 : it.end])), Ord: len(s.res.Mentions), Offset: s.off[it.start],
			})
		}
	}
	s.run, s.lit, s.off = s.run[:0], s.lit[:0], s.off[:0]
}

// cpIndex converts byte offsets of a UTF-8 source to code-point offsets.
type cpIndex []int32

func newCPIndex(src []byte) cpIndex {
	idx := make(cpIndex, len(src)+1)
	n := int32(0)
	for i := 0; i < len(src); {
		_, w := utf8.DecodeRune(src[i:])
		for k := 0; k < w; k++ {
			idx[i+k] = n
		}
		i += w
		n++
	}
	idx[len(src)] = n
	return idx
}

func (c cpIndex) at(b int) int {
	if b < 0 {
		return 0
	}
	if b >= len(c) {
		return int(c[len(c)-1])
	}
	return int(c[b])
}

// strikeParser is goldmark's strikethrough restricted to exactly two tildes on
// both sides, like markdown-it's.
type strikeParser struct{}

type strikeDelims struct{}

func (strikeDelims) IsDelimiter(b byte) bool                   { return b == '~' }
func (strikeDelims) CanOpenCloser(o, c *parser.Delimiter) bool { return o.Char == c.Char }
func (strikeDelims) OnMatch(int) ast.Node                      { return east.NewStrikethrough() }
func (strikeParser) Trigger() []byte                           { return []byte{'~'} }
func (strikeParser) CloseBlock(ast.Node, parser.Context)       {}
func (strikeParser) Parse(_ ast.Node, block text.Reader, pc parser.Context) ast.Node {
	before := block.PrecendingCharacter()
	line, segment := block.PeekLine()
	node := parser.ScanDelimiter(line, before, 2, strikeDelims{})
	if node == nil || node.OriginalLength != 2 || before == '~' {
		return nil
	}
	node.Segment = segment.WithStop(segment.Start + node.OriginalLength)
	block.Advance(node.OriginalLength)
	pc.PushDelimiter(node)
	return node
}
