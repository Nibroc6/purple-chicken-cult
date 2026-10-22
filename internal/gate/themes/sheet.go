package themes

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
)

// Theme CSS: the advanced mode of a theme (§15.2 A, ARCHITECTURE "Advanced
// themes"). It reads like CSS, but it is a closed dialect, not CSS:
//
//   - A selector is a part name from Parts, optionally with one state
//     (part:hover). There are no element, class, attribute, sibling or
//     descendant selectors, so a rule can only reach the elements the client
//     tags as that part. No part is, or contains only, an integrity element,
//     and the client never matches an integrity element or a locked surface
//     (passkey prompts, the theme page) with any part.
//   - A declaration is a property from Props with a value the property's own
//     grammar accepts. Nothing can position, hide, cover, shrink, transform,
//     filter or fade an element, or add content: no display, position,
//     opacity, transform, content, z-index or max sizes exist here.
//   - The only image source is image(name): one of the theme's own uploads,
//     scanned like every other upload and served from the media origin. No
//     url(), @import, @font-face or any other way to load a resource.
//   - The source is ASCII letters, digits and a few punctuation marks only, so
//     there are no escapes, strings, at-rules or !important.
//
// ParseSheet is the authority; web/src/features/themes/sheet.ts mirrors it for
// the editor's live preview and rebuilds the stylesheet from the normalized
// source, and both pass web/src/features/themes/sheet.vectors.json.
const (
	MaxSheet      = 16 << 10
	MaxRules      = 100
	MaxDecls      = 24
	MaxImages     = 8
	MaxImageName  = 32
	maxSelectors  = 8
	maxGradient   = 6
	minGradient   = 2
	pxPerEm       = 16
	maxBackground = 2000
)

// Part is a named piece of the app a rule may style.
type Part struct {
	// Button parts take the label property (an image can replace the text).
	Button bool
	// FG is the theme color the part's text uses when a rule sets none.
	FG string
}

// Parts are the stylable parts. The client maps each onto its own selector.
var Parts = map[string]Part{
	"page":           {FG: "text"},
	"header":         {FG: "text"},
	"brand":          {Button: true, FG: "text"},
	"nav":            {Button: true, FG: "accent"},
	"card":           {FG: "text"},
	"post-body":      {FG: "text"},
	"link":           {FG: "accent"},
	"input":          {FG: "text"},
	"button":         {Button: true, FG: "text"},
	"primary-button": {Button: true, FG: "text"},
	"like":           {Button: true, FG: "text"},
	"save":           {Button: true, FG: "text"},
	"hide":           {Button: true, FG: "text"},
	"show-less":      {Button: true, FG: "text"},
}

// States are the states a selector may name (pressed: a toggled button such
// as a like; current: the nav link of the page you are on).
var States = []string{"hover", "focus", "active", "pressed", "current", "disabled"}

// Selector is one part, optionally in one state.
type Selector struct {
	Part, State string
}

// Decl is one declaration, normalized.
type Decl struct {
	Prop, Value string
	Line        int
}

// Rule is one rule of a sheet.
type Rule struct {
	Selectors []Selector
	Decls     []Decl
	Line      int
}

var (
	sheetChars = regexp.MustCompile(`^[a-zA-Z0-9 \t\r\n#.,:;(){}%*/_-]*$`)
	nameRe     = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	// ImageName is the shape of a theme image's name.
	ImageName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	hexShort  = regexp.MustCompile(`^#[0-9a-f]{3}$`)
	hexLong   = regexp.MustCompile(`^#[0-9a-f]{6}$`)
	numRe     = regexp.MustCompile(`^(-?(?:[0-9]+(?:\.[0-9]+)?|\.[0-9]+))(px|rem|em|%|deg)?$`)
)

// sheetError is theme_css at a line (field "css.<line>"); line 0 is the sheet.
func sheetError(line int) error {
	return gate.NewProblem(gate.StatusUnprocessable, "theme_css", fmt.Sprintf("css.%d", line))
}

// ParseSheet parses and validates src. images are the names image() may use.
// Problem: theme_css, field "css.<line>" (the line of the first fault).
func ParseSheet(src string, images map[string]bool) ([]Rule, error) {
	if len(src) > MaxSheet || !sheetChars.MatchString(src) {
		return nil, sheetError(firstBadLine(src))
	}
	src = strings.ToLower(src)
	clean, err := stripComments(src)
	if err != nil {
		return nil, err
	}
	var rules []Rule
	i := 0
	lineAt := func(off int) int { return strings.Count(clean[:off], "\n") + 1 }
	for {
		for i < len(clean) && isSpace(clean[i]) {
			i++
		}
		if i == len(clean) {
			break
		}
		start := i
		open := strings.IndexAny(clean[i:], "{};")
		if open < 0 || clean[i+open] != '{' {
			return nil, sheetError(lineAt(start))
		}
		sels, ok := parseSelectors(clean[i : i+open])
		if !ok {
			return nil, sheetError(lineAt(start))
		}
		i += open + 1
		end := strings.IndexAny(clean[i:], "{}")
		if end < 0 || clean[i+end] != '}' {
			return nil, sheetError(lineAt(start))
		}
		r := Rule{Selectors: sels, Line: lineAt(start)}
		body := clean[i : i+end]
		off := i
		for _, part := range strings.Split(body, ";") {
			line := lineAt(off + len(part) - len(strings.TrimLeft(part, " \t\r\n")))
			off += len(part) + 1
			if strings.TrimSpace(part) == "" {
				continue
			}
			d, ok := parseDecl(part, line, sels, images)
			if !ok {
				return nil, sheetError(line)
			}
			r.Decls = append(r.Decls, d)
		}
		i += end + 1
		if len(r.Decls) > MaxDecls {
			return nil, sheetError(r.Line)
		}
		if err := checkLabel(r); err != nil {
			return nil, err
		}
		rules = append(rules, r)
		if len(rules) > MaxRules {
			return nil, sheetError(r.Line)
		}
	}
	return rules, nil
}

func firstBadLine(src string) int {
	if len(src) > MaxSheet {
		return 0
	}
	line := 1
	for _, c := range src {
		if c == '\n' {
			line++
			continue
		}
		if c > 0x7f || !sheetChars.MatchString(string(c)) {
			return line
		}
	}
	return 0
}

// stripComments blanks /* … */ comments, keeping newlines for line numbers.
func stripComments(src string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(src); {
		if strings.HasPrefix(src[i:], "/*") {
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				return "", sheetError(strings.Count(src[:i], "\n") + 1)
			}
			for _, c := range src[i : i+2+end+2] {
				if c == '\n' {
					b.WriteByte('\n')
				} else {
					b.WriteByte(' ')
				}
			}
			i += 2 + end + 2
			continue
		}
		if src[i] == '*' || (src[i] == '/' && !strings.HasPrefix(src[i:], "/*")) {
			// A stray * or / is not part of any value the dialect accepts.
			return "", sheetError(strings.Count(src[:i], "\n") + 1)
		}
		b.WriteByte(src[i])
		i++
	}
	return b.String(), nil
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

func parseSelectors(s string) ([]Selector, bool) {
	var out []Selector
	for _, raw := range strings.Split(s, ",") {
		raw = strings.TrimSpace(raw)
		part, state, hasState := strings.Cut(raw, ":")
		if _, ok := Parts[part]; !ok {
			return nil, false
		}
		if hasState && !slices.Contains(States, state) {
			return nil, false
		}
		out = append(out, Selector{Part: part, State: state})
	}
	return out, len(out) > 0 && len(out) <= maxSelectors
}

func parseDecl(s string, line int, sels []Selector, images map[string]bool) (Decl, bool) {
	prop, val, ok := strings.Cut(s, ":")
	prop = strings.TrimSpace(prop)
	if !ok || !nameRe.MatchString(prop) {
		return Decl{}, false
	}
	check, ok := Props[prop]
	if !ok {
		return Decl{}, false
	}
	toks, ok := lexValue(val)
	if !ok || len(toks) == 0 {
		return Decl{}, false
	}
	norm, ok := check(toks, images)
	if !ok {
		return Decl{}, false
	}
	if prop == "label" {
		for _, sel := range sels {
			if !Parts[sel.Part].Button {
				return Decl{}, false
			}
		}
	}
	return Decl{Prop: prop, Value: norm, Line: line}, true
}

// checkLabel: a rule that hides a button's text must give it an image, so the
// button never turns invisible (its accessible name stays the text).
func checkLabel(r Rule) error {
	hidden, image := 0, false
	for _, d := range r.Decls {
		switch {
		case d.Prop == "label" && d.Value == "hidden":
			hidden = d.Line
		case d.Prop == "label":
			hidden = 0
		case d.Prop == "background-image":
			image = strings.HasPrefix(d.Value, "image(")
		}
	}
	if hidden != 0 && !image {
		return sheetError(hidden)
	}
	return nil
}

// FormatSheet is the normalized source of rules: what the server stores and
// the client rebuilds the stylesheet from.
func FormatSheet(rules []Rule) string {
	var b strings.Builder
	for i, r := range rules {
		if i > 0 {
			b.WriteByte('\n')
		}
		for j, s := range r.Selectors {
			if j > 0 {
				b.WriteString(", ")
			}
			b.WriteString(s.Part)
			if s.State != "" {
				b.WriteString(":" + s.State)
			}
		}
		b.WriteString(" {\n")
		for _, d := range r.Decls {
			b.WriteString("  " + d.Prop + ": " + d.Value + ";\n")
		}
		b.WriteString("}\n")
	}
	return b.String()
}

// SheetImages are the image names rules use.
func SheetImages(rules []Rule) map[string]bool {
	out := map[string]bool{}
	for _, r := range rules {
		for _, d := range r.Decls {
			if name, ok := strings.CutPrefix(d.Value, "image("); ok && d.Prop == "background-image" {
				out[strings.TrimSuffix(name, ")")] = true
			}
		}
	}
	return out
}

// CheckSheet applies the contrast rule to rules under the theme's colors: a
// rule that sets a text or background color must keep the part's text at
// WCAG AA on its background (the rule's own, or the theme's card and page
// backgrounds). Text a rule hides behind an image is not checked, and neither
// is text over an image: the integrity elements, which no rule can reach, are
// what the floor protects. Problem: theme_contrast, field "css.<line>".
func CheckSheet(rules []Rule, c *Colors) error {
	for _, r := range rules {
		var fg, bg Decl
		hidden := false
		for _, d := range r.Decls {
			switch d.Prop {
			case "color":
				fg = d
			case "background-color":
				bg = d
			case "label":
				hidden = d.Value == "hidden"
			}
		}
		if hidden || (fg.Value == "" && bg.Value == "") {
			continue
		}
		line := fg.Line
		if line == 0 {
			line = bg.Line
		}
		bgs := []string{c.Surface, c.Bg}
		if bg.Value != "" && bg.Value != "transparent" {
			bgs = []string{bg.Value}
		}
		for _, s := range r.Selectors {
			f := fg.Value
			if f == "" {
				f = c.Text
				if Parts[s.Part].FG == "accent" {
					f = c.Accent
				}
			}
			for _, b := range bgs {
				if Contrast(f, b) < AA {
					return gate.NewProblem(gate.StatusUnprocessable, "theme_contrast", fmt.Sprintf("css.%d", line))
				}
			}
		}
	}
	return nil
}

// --- values

type tok struct {
	kind byte // 'i' ident, 'n' number, 'h' hash, 'f' function, ','
	text string
	num  float64
	unit string
	args []tok
}

func lexValue(s string) ([]tok, bool) {
	var out []tok
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case isSpace(c):
			i++
		case c == ',':
			out = append(out, tok{kind: ','})
			i++
		case c == '(' || c == ')':
			return nil, false
		default:
			j := i
			for j < len(s) && !isSpace(s[j]) && s[j] != ',' && s[j] != '(' && s[j] != ')' {
				j++
			}
			word := s[i:j]
			if j < len(s) && s[j] == '(' {
				if !nameRe.MatchString(word) {
					return nil, false
				}
				end := strings.IndexByte(s[j:], ')')
				if end < 0 {
					return nil, false
				}
				inner := s[j+1 : j+end]
				if strings.ContainsAny(inner, "(") {
					return nil, false
				}
				args, ok := lexValue(inner)
				if !ok {
					return nil, false
				}
				out = append(out, tok{kind: 'f', text: word, args: args})
				i = j + end + 1
				continue
			}
			t, ok := word1(word)
			if !ok {
				return nil, false
			}
			out = append(out, t)
			i = j
		}
	}
	return out, true
}

func word1(w string) (tok, bool) {
	switch {
	case strings.HasPrefix(w, "#"):
		if hexShort.MatchString(w) {
			return tok{kind: 'h', text: "#" + string([]byte{w[1], w[1], w[2], w[2], w[3], w[3]})}, true
		}
		if hexLong.MatchString(w) {
			return tok{kind: 'h', text: w}, true
		}
		return tok{}, false
	case nameRe.MatchString(w):
		return tok{kind: 'i', text: w}, true
	}
	m := numRe.FindStringSubmatch(w)
	if m == nil {
		return tok{}, false
	}
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil || math.IsInf(n, 0) {
		return tok{}, false
	}
	return tok{kind: 'n', num: n, unit: m[2]}, true
}

func numText(t tok) string {
	if t.num == 0 && t.unit != "deg" && t.unit != "%" {
		return "0"
	}
	return strconv.FormatFloat(t.num, 'f', -1, 64) + t.unit
}

// propCheck validates a value and returns its normalized text.
type propCheck func(toks []tok, images map[string]bool) (string, bool)

func color(allowTransparent bool) propCheck {
	return func(toks []tok, _ map[string]bool) (string, bool) {
		if len(toks) != 1 {
			return "", false
		}
		return colorTok(toks[0], allowTransparent)
	}
}

func colorTok(t tok, allowTransparent bool) (string, bool) {
	if t.kind == 'h' {
		return t.text, true
	}
	if allowTransparent && t.kind == 'i' && t.text == "transparent" {
		return t.text, true
	}
	return "", false
}

func keywords(words ...string) propCheck {
	return func(toks []tok, _ map[string]bool) (string, bool) {
		if len(toks) != 1 || toks[0].kind != 'i' || !slices.Contains(words, toks[0].text) {
			return "", false
		}
		return toks[0].text, true
	}
}

// lengthTok accepts px, rem or em (or a bare 0) within [lo, hi] px, and a
// percentage within [0, pct] when pct > 0.
func lengthTok(t tok, lo, hi, pct float64) (string, bool) {
	if t.kind != 'n' {
		return "", false
	}
	switch t.unit {
	case "px":
		if t.num < lo || t.num > hi {
			return "", false
		}
	case "rem", "em":
		if t.num*pxPerEm < lo || t.num*pxPerEm > hi {
			return "", false
		}
	case "%":
		if pct <= 0 || t.num < 0 || t.num > pct {
			return "", false
		}
	case "":
		if t.num != 0 {
			return "", false
		}
	default:
		return "", false
	}
	return numText(t), true
}

func lengths(n int, lo, hi, pct float64, words ...string) propCheck {
	return func(toks []tok, _ map[string]bool) (string, bool) {
		if len(toks) < 1 || len(toks) > n {
			return "", false
		}
		parts := make([]string, len(toks))
		for i, t := range toks {
			if t.kind == 'i' && slices.Contains(words, t.text) {
				parts[i] = t.text
				continue
			}
			v, ok := lengthTok(t, lo, hi, pct)
			if !ok {
				return "", false
			}
			parts[i] = v
		}
		return strings.Join(parts, " "), true
	}
}

func backgroundImage(toks []tok, images map[string]bool) (string, bool) {
	if len(toks) != 1 {
		return "", false
	}
	t := toks[0]
	switch {
	case t.kind == 'i' && t.text == "none":
		return "none", true
	case t.kind == 'f' && t.text == "image":
		if len(t.args) != 1 || t.args[0].kind != 'i' || !ImageName.MatchString(t.args[0].text) || !images[t.args[0].text] {
			return "", false
		}
		return "image(" + t.args[0].text + ")", true
	case t.kind == 'f' && t.text == "linear-gradient":
		var groups [][]tok
		cur := []tok{}
		for _, a := range t.args {
			if a.kind == ',' {
				groups = append(groups, cur)
				cur = []tok{}
				continue
			}
			cur = append(cur, a)
		}
		groups = append(groups, cur)
		var parts []string
		colors := 0
		for i, g := range groups {
			if len(g) != 1 {
				return "", false
			}
			if i == 0 && g[0].kind == 'n' && g[0].unit == "deg" {
				if g[0].num < 0 || g[0].num > 360 {
					return "", false
				}
				parts = append(parts, numText(g[0]))
				continue
			}
			c, ok := colorTok(g[0], true)
			if !ok {
				return "", false
			}
			parts = append(parts, c)
			colors++
		}
		if colors < minGradient || colors > maxGradient {
			return "", false
		}
		return "linear-gradient(" + strings.Join(parts, ", ") + ")", true
	}
	return "", false
}

func boxShadow(toks []tok, _ map[string]bool) (string, bool) {
	if len(toks) == 1 && toks[0].kind == 'i' && toks[0].text == "none" {
		return "none", true
	}
	if len(toks) < 3 || len(toks) > 5 {
		return "", false
	}
	c, ok := colorTok(toks[len(toks)-1], true)
	if !ok {
		return "", false
	}
	bounds := [][2]float64{{-32, 32}, {-32, 32}, {0, 64}, {-16, 16}}
	parts := make([]string, 0, len(toks))
	for i, t := range toks[:len(toks)-1] {
		v, ok := lengthTok(t, bounds[i][0], bounds[i][1], 0)
		if !ok {
			return "", false
		}
		parts = append(parts, v)
	}
	return strings.Join(append(parts, c), " "), true
}

func fontWeight(toks []tok, _ map[string]bool) (string, bool) {
	if len(toks) != 1 {
		return "", false
	}
	t := toks[0]
	if t.kind == 'i' && (t.text == "normal" || t.text == "bold") {
		return t.text, true
	}
	if t.kind == 'n' && t.unit == "" && slices.Contains([]float64{400, 500, 600, 700, 800}, t.num) {
		return numText(t), true
	}
	return "", false
}

func letterSpacing(toks []tok, _ map[string]bool) (string, bool) {
	if len(toks) != 1 {
		return "", false
	}
	t := toks[0]
	if t.kind == 'i' && t.text == "normal" {
		return "normal", true
	}
	if t.kind == 'n' && (t.unit == "em" || (t.unit == "" && t.num == 0)) && t.num >= -0.05 && t.num <= 0.3 {
		return numText(t), true
	}
	return "", false
}

// Props are the properties a rule may set, each with its value grammar.
var Props = map[string]propCheck{
	"color":               color(false),
	"background-color":    color(true),
	"border-color":        color(true),
	"background-image":    backgroundImage,
	"background-size":     lengths(2, 0, maxBackground, 100, "cover", "contain", "auto"),
	"background-position": lengths(2, 0, 0, 100, "center", "top", "bottom", "left", "right"),
	"background-repeat":   keywords("no-repeat", "repeat", "repeat-x", "repeat-y", "space", "round"),
	"border-width":        lengths(1, 0, 8, 0),
	"border-style":        keywords("none", "solid", "dashed", "dotted", "double"),
	"border-radius":       lengths(1, 0, 999, 50),
	"box-shadow":          boxShadow,
	"font-weight":         fontWeight,
	"font-style":          keywords("normal", "italic"),
	"text-transform":      keywords("none", "uppercase", "lowercase", "capitalize"),
	"text-decoration":     keywords("none", "underline"),
	"letter-spacing":      letterSpacing,
	"padding-block":       lengths(2, 0, 48, 0),
	"padding-inline":      lengths(2, 0, 48, 0),
	"min-block-size":      lengths(1, 0, 240, 0),
	"min-inline-size":     lengths(1, 0, 240, 0),
	"label":               keywords("visible", "hidden"),
}
