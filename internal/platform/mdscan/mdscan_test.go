package mdscan

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"
)

type fixture struct {
	Name     string    `json:"name"`
	Body     string    `json:"body"`
	Links    []Link    `json:"links"`
	Mentions []Mention `json:"mentions"`
}

func loadFixtures(t *testing.T) []fixture {
	t.Helper()
	b, err := os.ReadFile("../../../testdata/markdown/scan.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct{ Cases []fixture }
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Cases
}

// TestSharedFixtures: the corpus the web client is checked against too.
func TestSharedFixtures(t *testing.T) {
	cases := loadFixtures(t)
	if len(cases) < 60 {
		t.Fatalf("corpus has %d cases, want ≥ 60", len(cases))
	}
	names := map[string]bool{}
	for _, c := range cases {
		if names[c.Name] {
			t.Fatalf("duplicate case name %q", c.Name)
		}
		names[c.Name] = true
		got := Scan(c.Body)
		if got.Links == nil {
			got.Links = []Link{}
		}
		if got.Mentions == nil {
			got.Mentions = []Mention{}
		}
		got.Tables = nil // tables.json checks them
		if d := cmp.Diff(Result{Links: c.Links, Mentions: c.Mentions}, got); d != "" {
			t.Errorf("%s: (-want +got)\n%s", c.Name, d)
		}
	}
}

// TestSharedTableFixtures: the GFM table corpus the web client is checked
// against too (shapes, links and mentions with their offsets).
func TestSharedTableFixtures(t *testing.T) {
	b, err := os.ReadFile("../../../testdata/markdown/tables.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Cases []struct {
			fixture
			Tables []Table `json:"tables"`
		}
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Cases) < 40 {
		t.Fatalf("corpus has %d cases, want ≥ 40", len(doc.Cases))
	}
	names := map[string]bool{}
	for _, c := range doc.Cases {
		if names[c.Name] {
			t.Fatalf("duplicate case name %q", c.Name)
		}
		names[c.Name] = true
		got := Scan(c.Body)
		if got.Links == nil {
			got.Links = []Link{}
		}
		if got.Mentions == nil {
			got.Mentions = []Mention{}
		}
		if got.Tables == nil {
			got.Tables = []Table{}
		}
		want := Result{Links: c.Links, Mentions: c.Mentions, Tables: c.Tables}
		if d := cmp.Diff(want, got); d != "" {
			t.Errorf("%s: (-want +got)\n%s", c.Name, d)
		}
		runes := []rune(c.Body)
		for _, m := range got.Mentions {
			if runes[m.Offset] != '@' {
				t.Errorf("%s: mention offset %d", c.Name, m.Offset)
			}
		}
		for _, l := range got.Links {
			if r := runes[l.Offset]; r != '[' && r != '!' && r != '<' && r != 'h' && r != 'H' {
				t.Errorf("%s: link offset %d (%q)", c.Name, l.Offset, r)
			}
		}
	}
}

// TestTableLimits: at most MaxTableCols columns, top level only.
func TestTableLimits(t *testing.T) {
	row := func(n int) string { return "|" + strings.Repeat(" x |", n) }
	delim := func(n int) string { return "|" + strings.Repeat("-|", n) }
	for n := 1; n <= MaxTableCols+2; n++ {
		got := len(Scan(row(n) + "\n" + delim(n)).Tables)
		if want := map[bool]int{true: 1, false: 0}[n <= MaxTableCols]; got != want {
			t.Errorf("%d columns: %d tables", n, got)
		}
	}
	for _, body := range []string{"> | a |\n> | - |", "- | a |\n  | - |", "1. x\n\n   | a |\n   | - |"} {
		if r := Scan(body); len(r.Tables) != 0 {
			t.Errorf("%q: %+v", body, r.Tables)
		}
	}
}

// TestTableProperties: random table-shaped bodies never panic, cells keep
// valid offsets, and every table has 1..MaxTableCols columns in every row.
func TestTableProperties(t *testing.T) {
	parts := []string{"|", "| ", " |", "-", ":", "a", "@bob", "https://x.example/", "\\|", "`", "[l](https://l.example)",
		"\n", "\n", "\n\n", "> ", "- ", "   ", "\t", "é", "😀", "&amp;", "<https://q.example>", "***", "==="}
	rapid.Check(t, func(t *rapid.T) {
		var b strings.Builder
		for _, p := range rapid.SliceOfN(rapid.SampledFrom(parts), 0, 60).Draw(t, "parts") {
			b.WriteString(p)
		}
		body := "| h | i |\n|---|:-:|\n" + b.String()
		r := Scan(body)
		runes := []rune(body)
		if len(r.Tables) == 0 {
			t.Fatalf("no table in %q", body)
		}
		for _, tb := range r.Tables {
			if len(tb.Align) < 1 || len(tb.Align) > MaxTableCols {
				t.Fatalf("columns %d in %q", len(tb.Align), body)
			}
			for _, row := range tb.Rows {
				if len(row) != len(tb.Align) {
					t.Fatalf("row %v in %q", row, body)
				}
			}
		}
		for _, m := range r.Mentions {
			if m.Offset < 0 || m.Offset >= len(runes) || runes[m.Offset] != '@' {
				t.Fatalf("mention %+v in %q", m, body)
			}
		}
		for _, l := range r.Links {
			if l.Offset < 0 || l.Offset >= len(runes) {
				t.Fatalf("link %+v in %q", l, body)
			}
		}
	})
}

// TestOffsetsAreCodePoints: offsets index code points of the source, so the
// rune at a mention's offset is its '@' even after astral characters.
func TestOffsetsAreCodePoints(t *testing.T) {
	body := "😀😀 é @alice [x](https://a.example) 日本 https://b.example"
	r := Scan(body)
	runes := []rune(body)
	if len(r.Mentions) != 1 || runes[r.Mentions[0].Offset] != '@' || r.Mentions[0].Offset != 5 {
		t.Fatalf("mentions: %+v", r.Mentions)
	}
	if len(r.Links) != 2 || runes[r.Links[0].Offset] != '[' || string(runes[r.Links[1].Offset:r.Links[1].Offset+5]) != "https" {
		t.Fatalf("links: %+v", r.Links)
	}
}

func TestDecodeEntity(t *testing.T) {
	for in, want := range map[string]string{
		"&amp;": "&", "&lt;": "<", "&#64;": "@", "&#x40;": "@", "&#X40;": "@", "&ngE;": "≧̸", "&semi;": ";",
		"&#0;": "\uFFFD", "&#xd800;": "\uFFFD", "&#1114112;": "\uFFFD",
	} {
		got, n := decodeEntity([]byte(in+"tail"), true)
		if string(got) != want || n != len(in) {
			t.Errorf("%q: %q %d", in, string(got), n)
		}
	}
	// Strict names: legacy prefixes and unknown names stay literal.
	for _, in := range []string{"&notit;", "&bogus;", "&amp", "&#;", "&#x;", "&#12345678;", "& amp;"} {
		if got, n := decodeEntity([]byte(in), true); n != 0 {
			t.Errorf("%q decoded to %q", in, string(got))
		}
	}
	// In link destinations an invalid numeric reference stays as written.
	if got := unescapeAll(`a\_b&amp;c&#0;d\q&notit;`); got != `a_b&c&#0;d\q&notit;` {
		t.Fatalf("unescapeAll: %q", got)
	}
}

func TestStrikethroughNeedsTwoTildes(t *testing.T) {
	// "~x~" stays text, so the URL keeps its tilde path; "~~x~~" strikes.
	if r := Scan("https://e.example/~a~b"); len(r.Links) != 1 || r.Links[0].Raw != "https://e.example/~a~b" {
		t.Fatalf("%+v", r.Links)
	}
	if r := Scan("~~@gone~~ and ~~~@three~~~"); len(r.Mentions) != 2 {
		t.Fatalf("%+v", r.Mentions)
	}
}

func TestRawHTMLIsText(t *testing.T) {
	r := Scan("<script>@x_y https://s.example</script>\n\n<!-- @hidden -->")
	if len(r.Mentions) != 2 || r.Mentions[0].Handle != "x_y" || r.Mentions[1].Handle != "hidden" || len(r.Links) != 1 {
		t.Fatalf("%+v", r)
	}
}

// TestScanProperties: Scan never panics, every offset is in range and points
// at the item's first character, ords count from 0 and handles are canonical.
func TestScanProperties(t *testing.T) {
	alphabet := []rune("ab@_ :/.()[]<>*~`\\&#;!\n -hHtTps1é😀")
	rapid.Check(t, func(t *rapid.T) {
		body := string(rapid.SliceOfN(rapid.SampledFrom(alphabet), 0, 80).Draw(t, "body"))
		if rapid.Bool().Draw(t, "prefix") {
			body = "https://x.example/" + body
		}
		r := Scan(body)
		runes := []rune(body)
		for i, m := range r.Mentions {
			if m.Ord != i || m.Offset < 0 || m.Offset >= len(runes) || m.Handle != strings.ToLower(m.Handle) ||
				utf8.RuneCountInString(m.Handle) < minHandle || utf8.RuneCountInString(m.Handle) > maxHandle {
				t.Fatalf("mention %d: %+v in %q", i, m, body)
			}
		}
		for _, l := range r.Links {
			if l.Offset < 0 || l.Offset >= len(runes) || !isHTTP([]rune(l.Raw)) {
				t.Fatalf("link %+v in %q", l, body)
			}
		}
	})
}

func BenchmarkScan(b *testing.B) {
	body := strings.Repeat("Hello @alice, see https://example.com/a?b=c and [x](https://x.example) `code @no`.\n\n", 200)
	b.ReportAllocs()
	for b.Loop() {
		_ = Scan(body)
	}
}
