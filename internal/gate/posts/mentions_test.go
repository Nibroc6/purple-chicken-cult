package posts

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
)

func TestScanMentions(t *testing.T) {
	for _, c := range []struct {
		body string
		want []mention
	}{
		{"@alice hi", []mention{{0, "alice"}}},
		{"hi @Bob_1, and (@carol).", []mention{{0, "bob_1"}, {1, "carol"}}},
		{"mail x@bob.com or @@dave", nil},
		{"@ab is too short, @abcdefghijklmnopqrstu too long", nil},
		{"`@code` and ``a ` @x2y`` but @after", []mention{{0, "after"}}},
		{"unclosed `@open span", []mention{{0, "open"}}},
		{"```\n@fenced\n```\n@out\n~~~go\n@tilde\n~~~", []mention{{0, "out"}}},
		{"   ```\n@indented_fence\n   ```", nil},
		{"emoji😀@yes but é@nope", []mention{{0, "yes"}, {1, "nope"}}},
		{`\@escaped and @kept`, []mention{{0, "kept"}}},
		{"@first @second @third", []mention{{0, "first"}, {1, "second"}, {2, "third"}}},
	} {
		got := scanMentions(c.body)
		if len(got) == 0 {
			got = nil
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: %v, want %v", c.body, got, c.want)
		}
	}
}

func TestCleanBodyAndLang(t *testing.T) {
	if b, err := cleanBody("\n \n  indented code\nline  \n\n", false); err != nil || b != "  indented code\nline" {
		t.Fatalf("%q %v", b, err)
	}
	if b, _ := cleanBody("é", false); b != "é" {
		t.Fatalf("not NFC: %q", b)
	}
	// NFC can shrink a body back under the limit: count after normalizing.
	if _, err := cleanBody(strings.Repeat("é", MaxBodyCodePoints), false); err != nil {
		t.Fatal(err)
	}
	var p *gate.Problem
	if _, err := cleanBody(strings.Repeat("a", MaxBodyCodePoints+1), false); !errors.As(err, &p) || p.Code != "body_too_long" {
		t.Fatal(err)
	}
	for in, want := range map[string]string{"": "und", "und": "und", "EN": "en", "pt-br": "pt-BR", "zh-Hant-TW": "zh-Hant-TW"} {
		if got, err := cleanLang(in); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	for _, in := range []string{"english", "x", strings.Repeat("a", 40), "en_US!"} {
		if _, err := cleanLang(in); !errors.As(err, &p) || p.Code != "lang_invalid" {
			t.Errorf("%q: %v", in, err)
		}
	}
}
