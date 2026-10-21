package mdscan

import (
	"strings"
	"unicode/utf8"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// GFM tables (P1-W11), mirrored in web/src/markdown/table.ts. Both sides use
// the same rules, a stricter subset of GFM, so that goldmark's paragraph-based
// view and markdown-it's block rule cannot disagree:
//
//   - top level only (never inside a blockquote or a list);
//   - the header row and the delimiter row start with "|" (after at most 3
//     spaces of indentation), so no other block can start on either line;
//   - the delimiter row is "|", "-", ":", spaces and tabs only, its cells are
//     :?-+:? and it has at most MaxTableCols columns; the header row has
//     exactly as many cells;
//   - body rows follow until a blank line (JavaScript trim), or a line that
//     starts another block and may interrupt a paragraph (ATX heading, fence,
//     blockquote, thematic break, a list item that may interrupt a paragraph),
//     or a setext underline ("===" or "---" alone); an indented line is a row;
//   - cells split on "|" not preceded by "\" (the backslash is removed, also
//     inside code spans), are trimmed like String.prototype.trim, padded to
//     the header's count and cut past it, and parsed as inline content.
//
// A table that breaks any rule stays paragraph text.

// MaxTableCols is the most columns a table may have.
const MaxTableCols = 10

// Table is the shape of a table: alignment per column ("", "left", "center",
// "right") and the cells' plain text, header row first. The shared fixtures
// compare it with the client's.
type Table struct {
	Align []string   `json:"align"`
	Rows  [][]string `json:"rows"`
}

var kindTable = ast.NewNodeKind("PCCTable")

// tableNode replaces the paragraph lines that form a table. Its cells are
// parsed (inline) only by the scanner, from their own sources.
type tableNode struct {
	ast.BaseBlock
	align []string
	rows  [][]tableCell
}

func (n *tableNode) Kind() ast.NodeKind { return kindTable }

func (n *tableNode) Dump(src []byte, level int) { ast.DumpHelper(n, src, level, nil, nil) }

// tableCell is a cell's inline source as markdown-it sees it (escaped pipes
// without their backslash) and the source byte offset of each of its bytes
// (one extra entry for the end).
type tableCell struct {
	src  []byte
	orig []int
}

// tableTransformer turns a top-level paragraph that contains a header and a
// delimiter row into [paragraph] table [paragraph]. The paragraph is always
// replaced (never shortened in place), so a setext underline after the table
// does not turn the text before it into a heading.
type tableTransformer struct{}

func (tableTransformer) Transform(p *ast.Paragraph, reader text.Reader, pc parser.Context) {
	parent := p.Parent()
	if parent == nil || parent.Kind() != ast.KindDocument {
		return
	}
	src := reader.Source()
	lines := p.Lines()
	for i := 1; i < lines.Len(); i++ {
		align := parseDelimiter(rawLine(src, lines.At(i)))
		if align == nil {
			continue
		}
		head := splitRow(rawLine(src, lines.At(i-1)), true)
		if head == nil || len(head) != len(align) {
			continue
		}
		t := &tableNode{align: align, rows: [][]tableCell{head}}
		j := i + 1
		for ; j < lines.Len(); j++ {
			row := splitRow(rawLine(src, lines.At(j)), false)
			if row == nil {
				break // blank after trimming: the table ends here
			}
			for len(row) < len(align) {
				row = append(row, tableCell{orig: []int{0}})
			}
			t.rows = append(t.rows, row[:len(align)])
		}
		if i > 1 {
			parent.InsertBefore(parent, p, paragraphOf(src, lines, 0, i-1))
		}
		parent.InsertBefore(parent, p, t)
		var rest *ast.Paragraph
		if j < lines.Len() {
			rest = paragraphOf(src, lines, j, lines.Len())
			parent.InsertBefore(parent, p, rest)
		}
		parent.RemoveChild(parent, p)
		if rest != nil { // the lines after a whitespace-only line are a new paragraph, which may hold a table
			tableTransformer{}.Transform(rest, reader, pc)
		}
		return
	}
}

// paragraphOf is a closed paragraph of lines [from, to), trimmed as
// goldmark's paragraph parser trims on close.
func paragraphOf(src []byte, lines *text.Segments, from, to int) *ast.Paragraph {
	np := ast.NewParagraph()
	for k := from; k < to; k++ {
		seg := lines.At(k)
		seg = seg.TrimLeftSpace(src)
		if k == to-1 {
			seg = seg.TrimRightSpace(src)
		}
		np.Lines().Append(seg)
	}
	return np
}

// line is one source line as markdown-it's getLine sees it: the text after
// the leading spaces and tabs (without the line break), its source offset and
// the indentation width (tabs to the next multiple of 4).
type line struct {
	text   string
	off    int
	indent int
}

func rawLine(src []byte, seg text.Segment) line {
	start := seg.Start
	for start > 0 && src[start-1] != '\n' {
		start--
	}
	end := start
	for end < len(src) && src[end] != '\n' {
		end++
	}
	if end > start && src[end-1] == '\r' {
		end--
	}
	w, i := 0, start
	for ; i < end && (src[i] == ' ' || src[i] == '\t'); i++ {
		if src[i] == '\t' {
			w += 4 - w%4
		} else {
			w++
		}
	}
	return line{text: string(src[i:end]), off: i, indent: w}
}

// parseDelimiter returns the column alignments of a delimiter row, or nil.
func parseDelimiter(l line) []string {
	s := l.text
	if l.indent >= 4 || len(s) < 2 || s[0] != '|' {
		return nil
	}
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '|', '-', ':', ' ', '\t':
		default:
			return nil
		}
	}
	cols := strings.Split(s, "|")
	var align []string
	for i, c := range cols {
		t := jsTrim(c)
		if t == "" {
			if i == 0 || i == len(cols)-1 {
				continue
			}
			return nil
		}
		left, right := t[0] == ':', t[len(t)-1] == ':'
		dashes := strings.TrimSuffix(strings.TrimPrefix(t, ":"), ":")
		if dashes == "" || strings.Trim(dashes, "-") != "" {
			return nil
		}
		switch {
		case left && right:
			align = append(align, "center")
		case right:
			align = append(align, "right")
		case left:
			align = append(align, "left")
		default:
			align = append(align, "")
		}
	}
	if len(align) == 0 || len(align) > MaxTableCols {
		return nil
	}
	return align
}

// splitRow splits a row into cells (markdown-it's escapedSplit, then the
// leading and trailing empty cells dropped). A header row must not be
// indented 4 or more and must start with "|"; nil means "not a row": a
// refused header, or a body line that is blank after trimming.
func splitRow(l line, header bool) []tableCell {
	s, off := jsTrimOff(l.text, l.off)
	if s == "" || (header && (l.indent >= 4 || s[0] != '|')) {
		return nil
	}
	var (
		cells []tableCell
		cur   tableCell
	)
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '|' {
			if !escaped {
				cells = append(cells, cur)
				cur = tableCell{}
				escaped = false
				continue
			}
			// "\|": drop the backslash, keep the pipe.
			cur.src, cur.orig = cur.src[:len(cur.src)-1], cur.orig[:len(cur.orig)-1]
		}
		cur.src = append(cur.src, c)
		cur.orig = append(cur.orig, off+i)
		escaped = c == '\\'
	}
	cells = append(cells, cur)
	if len(cells) > 0 && len(cells[0].src) == 0 {
		cells = cells[1:]
	}
	if len(cells) > 0 && len(cells[len(cells)-1].src) == 0 {
		cells = cells[:len(cells)-1]
	}
	for k := range cells {
		cells[k] = cells[k].trimmed(off + len(s))
	}
	return cells
}

// trimmed trims the cell like String.prototype.trim and closes its offsets
// (end is the source offset after the row, for an empty cell).
func (c tableCell) trimmed(end int) tableCell {
	a, b := 0, len(c.src)
	for a < b {
		r, w := utf8.DecodeRune(c.src[a:b])
		if !isSpace(r) {
			break
		}
		a += w
	}
	for b > a {
		r, w := utf8.DecodeLastRune(c.src[a:b])
		if !isSpace(r) {
			break
		}
		b -= w
	}
	out := tableCell{src: c.src[a:b], orig: append([]int(nil), c.orig[a:b]...)}
	switch {
	case b < len(c.orig):
		out.orig = append(out.orig, c.orig[b])
	case len(c.orig) > 0:
		out.orig = append(out.orig, c.orig[len(c.orig)-1]+1)
	default:
		out.orig = append(out.orig, end)
	}
	return out
}

// jsTrim is String.prototype.trim (JavaScript's \s at both ends).
func jsTrim(s string) string {
	t, _ := jsTrimOff(s, 0)
	return t
}

func jsTrimOff(s string, off int) (string, int) {
	for s != "" {
		r, w := utf8.DecodeRuneInString(s)
		if !isSpace(r) {
			break
		}
		s, off = s[w:], off+w
	}
	for s != "" {
		r, w := utf8.DecodeLastRuneInString(s)
		if !isSpace(r) {
			break
		}
		s = s[:len(s)-w]
	}
	return s, off
}

// cellMD parses a cell's source as inline content: one paragraph, no other
// blocks, no link reference definitions (references come from the post).
var cellMD = func() parser.Parser {
	inlines := withoutRawHTML(parser.DefaultInlineParsers())
	inlines = append(inlines, util.Prioritized(strikeParser{}, 500))
	return parser.NewParser(
		parser.WithBlockParsers(util.Prioritized(parser.NewParagraphParser(), 1000)),
		parser.WithInlineParsers(inlines...),
	)
}()

// table scans the cells of t in order and records its shape.
func (s *scanner) table(t *tableNode) {
	shape := Table{Align: t.align}
	for _, row := range t.rows {
		texts := make([]string, len(row))
		for k, c := range row {
			texts[k] = s.cell(c)
		}
		shape.Rows = append(shape.Rows, texts)
	}
	s.res.Tables = append(s.res.Tables, shape)
}

// cell scans one cell (links and mentions continue the post's numbering)
// and returns its plain text.
func (s *scanner) cell(c tableCell) string {
	if len(c.src) == 0 {
		return ""
	}
	cp := make(cpIndex, len(c.src)+1)
	for i, o := range c.orig {
		cp[i] = int32(s.cp.at(o)) //nolint:gosec // offsets of a post body
	}
	pc := parser.NewContext()
	for _, r := range s.pc.References() {
		pc.AddReference(r)
	}
	doc := cellMD.Parse(text.NewReader(c.src), parser.WithContext(pc))
	sub := &scanner{src: c.src, cp: cp, res: s.res, pc: s.pc}
	sub.blocks(doc)
	s.res = sub.res
	var b strings.Builder
	for n := doc.FirstChild(); n != nil; n = n.NextSibling() {
		plainText(&b, n, c.src)
	}
	return b.String()
}

// plainText appends the text of inline content: text with escapes and
// character references decoded, code span contents, link and image text (an
// image without alt text shows its destination), autolink labels.
func plainText(b *strings.Builder, n ast.Node, src []byte) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch v := c.(type) {
		case *ast.Text:
			b.WriteString(decodeText(src[v.Segment.Start:v.Segment.Stop]))
		case *ast.String:
			b.Write(v.Value)
		case *ast.CodeSpan:
			for t := v.FirstChild(); t != nil; t = t.NextSibling() {
				switch tv := t.(type) {
				case *ast.Text:
					b.Write(tv.Segment.Value(src))
				case *ast.String:
					b.Write(tv.Value)
				}
			}
		case *ast.Image:
			if c.HasChildren() {
				plainText(b, c, src)
			} else {
				b.WriteString(unescapeAll(string(v.Destination)))
			}
		case *ast.AutoLink:
			b.Write(v.Label(src))
		default:
			plainText(b, c, src)
		}
	}
}

// decodeText decodes backslash escapes and character references of text.
func decodeText(raw []byte) string {
	var b strings.Builder
	for i := 0; i < len(raw); {
		r, w := utf8.DecodeRune(raw[i:])
		if r == '\\' && i+1 < len(raw) && raw[i+1] < 0x80 && isASCIIPunct(raw[i+1]) {
			b.WriteByte(raw[i+1])
			i += 2
			continue
		}
		if r == '&' {
			if dec, n := decodeEntity(raw[i:], true); n > 0 {
				b.WriteString(string(dec))
				i += n
				continue
			}
		}
		b.WriteRune(r)
		i += w
	}
	return b.String()
}
