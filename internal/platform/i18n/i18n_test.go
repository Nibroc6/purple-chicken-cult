package i18n

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Nibroc6/purple-chicken-cult/internal/platform/config"
)

func TestFormat(t *testing.T) {
	got, err := Format("en", "push.post_referenced", map[string]any{"count": 5})
	if err != nil {
		t.Fatal(err)
	}
	if got != "5 new posts reference yours" {
		t.Fatalf("got %q", got)
	}
	one, _ := Format("en", "push.post_referenced", map[string]any{"count": 1})
	if one != "1 new post references yours" {
		t.Fatalf("plural one: %q", one)
	}
	name, _ := Format("en", "push.mention", map[string]any{"name": "@bob"})
	if name != "@bob mentioned you" {
		t.Fatalf("argument: %q", name)
	}
}

func TestFallback(t *testing.T) {
	got, err := Format("de-AT", "push.generic", nil)
	if err != nil || got != "New activity" {
		t.Fatalf("fallback to en expected: %q %v", got, err)
	}
	if _, err := Format("en", "nope.key", nil); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("unknown key: %v", err)
	}
	// Without the generated pseudo-locale (prod), en-XA falls back to en.
	got, _ = Format("en-XA", "push.generic", nil)
	if got != "New activity" {
		t.Fatalf("prod en-XA: %q", got)
	}
}

func TestBaseLanguageAndCase(t *testing.T) {
	c, err := Load(fstest.MapFS{
		"catalogs/en/server.json":    {Data: []byte(`{"k": "hello", "only.en": "x"}`)},
		"catalogs/pt/server.json":    {Data: []byte(`{"k": "olá"}`)},
		"catalogs/pt-BR/server.json": {Data: []byte(`{"k": "oi"}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	for loc, want := range map[string]string{"pt-BR": "oi", "pt-br": "oi", "pt-PT": "olá", "pt": "olá", "fr": "hello", "": "hello"} {
		if got, err := c.Format(loc, "k", nil); err != nil || got != want {
			t.Errorf("%q: %q %v", loc, got, err)
		}
	}
	if got, _ := c.Format("pt-BR", "only.en", nil); got != "x" {
		t.Errorf("missing key falls back to en: %q", got)
	}
	if _, err := Load(fstest.MapFS{"catalogs/de/server.json": {Data: []byte(`{}`)}}); err == nil {
		t.Error("a catalog set without en must fail to load")
	}
	if _, err := Load(fstest.MapFS{"catalogs/en/server.json": {Data: []byte(`{`)}}); err == nil {
		t.Error("malformed JSON must fail to load")
	}
}

func TestAllMessagesCompile(t *testing.T) {
	c, err := ForConfig(&config.Config{Env: config.EnvTest})
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Locales(); len(got) < 2 || !contains(got, PseudoLocale) {
		t.Fatalf("locales %v", got)
	}
	for _, l := range c.Locales() {
		for _, k := range c.Keys() {
			if _, err := c.Format(l, k, map[string]any{"name": "@x", "count": 2}); err != nil {
				t.Errorf("%s/%s: %v", l, k, err)
			}
		}
	}
	prod, err := ForConfig(&config.Config{Env: config.EnvProd})
	if err != nil || contains(prod.Locales(), PseudoLocale) {
		t.Fatalf("prod ships no pseudo-locale: %v %v", prod.Locales(), err)
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func TestPseudoMatchesTheWebGenerator(t *testing.T) {
	// Literal text follows web/scripts/pseudo-locale.mjs exactly (accents, ceil(40%)
	// "~" per literal, brackets around the message); the ICU structure is kept as
	// written (the web printer only normalizes whitespace).
	cases := map[string]string{
		"New activity":                                  "[Ñêŵ àçŧîṽîŧý~~~~~]",
		"{name} mentioned you":                          "[{name} ɱêñŧîôñêð ýôû~~~~~]",
		"Hi {name}, {count, number}!":                   "[Ĥî ~{name}, {count, number}!]",
		"{n, select, a {Yes} other {No}}":               "[{n, select, a {Ýêš~~} other {Ñô~}}]",
		"{count, plural, one {# post} other {# posts}}": "[{count, plural, one {# þôšŧ~~} other {# þôšŧš~~}}]",
		"It''s '{'literal'}'":                           "[Îŧ~''š ~'{'ļîŧêŕàļ~~~'}']",
	}
	for src, want := range cases {
		got, err := Pseudo(src)
		if err != nil {
			t.Errorf("%q: %v", src, err)
			continue
		}
		if got != want {
			t.Errorf("%q:\n got %q\nwant %q", src, got, want)
		}
	}
	for _, bad := range []string{"{", "{name", "{n, plural, one {x}", "{n, select, a x}", "}"} {
		if _, err := Pseudo(bad); err == nil {
			t.Errorf("%q: malformed message accepted", bad)
		}
	}
}

func TestPseudoLocaleFormats(t *testing.T) {
	c, err := ForConfig(&config.Config{Env: config.EnvTest})
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Format("en-XA", "push.post_referenced", map[string]any{"count": 3})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "[") || !strings.HasSuffix(got, "]") || !strings.Contains(got, "3 ñêŵ") || regexp.MustCompile(`[A-Za-z]`).MatchString(got) {
		t.Fatalf("en-XA plural: %q", got)
	}
	one, _ := c.Format("en-xa", "push.post_referenced", map[string]any{"count": 1})
	if !strings.Contains(one, "1 ñêŵ þôšŧ ŕêƒêŕêñçêš") {
		t.Fatalf("en-XA plural one (case-insensitive tag): %q", one)
	}
	named, _ := c.Format("en-XA", "push.mention", map[string]any{"name": "@bob"})
	if !strings.HasPrefix(named, "[@bob ɱêñŧîôñêð") {
		t.Fatalf("en-XA keeps arguments as given: %q", named)
	}
}

// ---- the "no prose" rule for push payload builders (plan §1.1 A9, §4.2) ----

var prose = regexp.MustCompile(`\p{L}[ \t\n]+\p{L}`)

const (
	modPath    = "github.com/Nibroc6/purple-chicken-cult"
	i18nPkg    = modPath + "/internal/platform/i18n"
	webpushPkg = modPath + "/internal/platform/webpush"
	corePkg    = modPath + "/internal/gate/internal/core"
)

type violation struct {
	pos token.Position
	msg string
}

// allowedCall reports calls whose literal arguments may be prose: errors and
// log messages.
func allowedCall(call *ast.CallExpr, imports map[string]string) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	x, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	switch imports[x.Name] {
	case "errors", "fmt":
		return sel.Sel.Name == "New" || sel.Sel.Name == "Errorf"
	case "log/slog":
		return true
	}
	switch sel.Sel.Name { // (*slog.Logger) methods on a logger value
	case "Debug", "Info", "Warn", "Error", "DebugContext", "InfoContext", "WarnContext", "ErrorContext":
		return true
	}
	return false
}

// literals reports every prose-like string literal under n, skipping the
// arguments of allowed calls.
func literals(fset *token.FileSet, n ast.Node, imports map[string]string, why string, out *[]violation) {
	ast.Inspect(n, func(c ast.Node) bool {
		switch x := c.(type) {
		case *ast.CallExpr:
			if allowedCall(x, imports) {
				return false
			}
		case *ast.BasicLit:
			if x.Kind != token.STRING {
				return false
			}
			s, err := strconv.Unquote(x.Value)
			if err == nil && prose.MatchString(s) {
				*out = append(*out, violation{fset.Position(x.Pos()), why + ": prose-like literal " + strconv.Quote(s)})
			}
		}
		return true
	})
}

// scanFile applies the rule to one file. Inside platform/i18n and
// platform/webpush every literal outside errors and logs counts; elsewhere the
// payload builders do: calls into those packages, Format / Send / Marshal calls
// in files importing them, webpush.Message and core.Event literals. A literal
// catalog key must exist in the en catalog.
func scanFile(fset *token.FileSet, f *ast.File, pkgPath string, cat *Catalog) []violation {
	imports := map[string]string{}
	for _, im := range f.Imports {
		p, _ := strconv.Unquote(im.Path.Value)
		name := p[strings.LastIndexByte(p, '/')+1:]
		if im.Name != nil {
			name = im.Name.Name
		}
		imports[name] = p
	}
	var out []violation
	if pkgPath == i18nPkg || pkgPath == webpushPkg || strings.HasPrefix(pkgPath, webpushPkg+"/") {
		for _, d := range f.Decls {
			if g, ok := d.(*ast.GenDecl); ok && g.Tok == token.IMPORT {
				continue
			}
			literals(fset, d, imports, "push/i18n package", &out)
		}
		return out
	}
	alias := func(path string) string {
		for n, p := range imports {
			if p == path {
				return n
			}
		}
		return ""
	}
	i18nA, pushA, coreA := alias(i18nPkg), alias(webpushPkg), alias(corePkg)
	if pkgPath == corePkg {
		coreA = "\x00self"
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			sel, ok := x.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			id, _ := sel.X.(*ast.Ident)
			intoPkg := id != nil && (id.Name == i18nA || id.Name == pushA) && id.Name != ""
			builder := (i18nA != "" || pushA != "") && (sel.Sel.Name == "Format" || sel.Sel.Name == "Send" || sel.Sel.Name == "Marshal")
			if !intoPkg && !builder {
				return true
			}
			for _, a := range x.Args {
				literals(fset, a, imports, "payload builder", &out)
			}
			if sel.Sel.Name == "Format" && i18nA != "" && len(x.Args) >= 2 {
				if lit, ok := x.Args[1].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					k, _ := strconv.Unquote(lit.Value)
					if !cat.Has(k) {
						out = append(out, violation{fset.Position(lit.Pos()), "catalog key " + strconv.Quote(k) + " is not in catalogs/en/server.json"})
					}
				}
			}
		case *ast.CompositeLit:
			sel, ok := x.Type.(*ast.SelectorExpr)
			if !ok {
				if id, ok := x.Type.(*ast.Ident); ok && coreA == "\x00self" && id.Name == "Event" {
					literals(fset, x, imports, "core.Event", &out)
				}
				return true
			}
			id, _ := sel.X.(*ast.Ident)
			if id == nil {
				return true
			}
			if (id.Name == pushA && sel.Sel.Name == "Message") || (id.Name == coreA && sel.Sel.Name == "Event") {
				literals(fset, x, imports, id.Name+"."+sel.Sel.Name, &out)
			}
		}
		return true
	})
	return out
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repo root not found from %s", root)
	}
	return root
}

func TestNoProseInPushPayloadBuilders(t *testing.T) {
	cat, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	root := repoRoot(t)
	fset := token.NewFileSet()
	files := 0
	err = filepath.WalkDir(filepath.Join(root, "internal"), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, filepath.Dir(p))
		files++
		for _, v := range scanFile(fset, f, modPath+"/"+filepath.ToSlash(rel), cat) {
			t.Errorf("%s: %s (push text lives in catalogs/<locale>/server.json)", v.pos, v.msg)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files < 100 {
		t.Fatalf("scanned only %d files", files)
	}
}

// TestNoProseScannerCatches plants violations so the scan cannot silently pass.
func TestNoProseScannerCatches(t *testing.T) {
	cat, _ := Default()
	src := `package notify
import (
	"errors"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/i18n"
	wp "github.com/Nibroc6/purple-chicken-cult/internal/platform/webpush"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)
func f(c *i18n.Catalog, s wp.Sender) {
	_ = errors.New("this error is fine")
	_, _ = i18n.Format("en", "push.generic", map[string]any{"name": "Bob Smith"})
	_, _ = c.Format("en", "push.not_there", nil)
	_, _ = wp.Message{Title: "Someone liked it", URL: "/notifications"}.Marshal()
	_ = core.Event{Kind: "mention", Data: map[string]any{"why": "a long story"}}
	_ = core.Event{Kind: "mention"}
	_, _ = s.Send(nil, wp.Subscription{}, []byte("hello there"), 0)
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "planted.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := scanFile(fset, f, modPath+"/internal/gate/notify", cat)
	var msgs []string
	for _, v := range got {
		msgs = append(msgs, v.msg)
	}
	joined := strings.Join(msgs, "\n")
	for _, want := range []string{`"Bob Smith"`, `"push.not_there"`, `"Someone liked it"`, `"a long story"`, `"hello there"`} {
		if !strings.Contains(joined, want) {
			t.Errorf("scanner missed %s; got:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "this error is fine") || len(got) != 5 {
		t.Errorf("unexpected findings (%d):\n%s", len(got), joined)
	}
	own, err := parser.ParseFile(fset, "own.go", `package webpush
import "errors"
var a = errors.New("errors may be prose")
var b = "but not this one"
`, 0)
	if err != nil {
		t.Fatal(err)
	}
	if v := scanFile(fset, own, webpushPkg, cat); len(v) != 1 || !strings.Contains(v[0].msg, "but not this one") {
		t.Errorf("package rule: %+v", v)
	}
}
