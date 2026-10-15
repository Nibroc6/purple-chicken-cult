package urlx

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"pgregory.net/rapid"
)

// TestTrackingListIsShared: the embedded copy equals testdata/tracking-params.json.
func TestTrackingListIsShared(t *testing.T) {
	shared, err := os.ReadFile("../../../testdata/tracking-params.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(shared, trackingJSON) {
		t.Fatal("internal/platform/urlx/tracking-params.json differs from testdata/tracking-params.json")
	}
	var tr Tracking
	if err := json.Unmarshal(shared, &tr); err != nil || len(tr.Exact) == 0 || len(tr.Prefixes) == 0 {
		t.Fatalf("%v %+v", err, tr)
	}
	for _, k := range append(tr.Exact, tr.Prefixes...) {
		if k != strings.ToLower(k) {
			t.Fatalf("%q is not lowercase", k)
		}
	}
}

func TestCanonicalize(t *testing.T) {
	for in, want := range map[string]string{
		"https://Example.COM":                                     "https://example.com/",
		"HTTPS://example.com:443/a#frag":                          "https://example.com/a",
		"http://example.com:80/?":                                 "http://example.com/",
		"http://example.com:8080/x":                               "http://example.com:8080/x",
		"https://example.com./p":                                  "https://example.com/p",
		"https://bücher.example/straße":                           "https://xn--bcher-kva.example/stra%C3%9Fe",
		"https://example.com/?utm_source=a&id=7&fbclid=b&UTM_X=1": "https://example.com/?id=7",
		"https://example.com/?gclid=1&dclid=2&si=3&igshid=4":      "https://example.com/",
		"https://example.com/?b=2&a=1&mc_eid=x&ref_src=tw":        "https://example.com/?b=2&a=1",
		"https://example.com/?q=a%20b&utm%5Fmedium=x":             "https://example.com/?q=a%20b",
		"https://example.com/p?x=%7E&&y":                          "https://example.com/p?x=%7E&y",
		"  https://example.com/trim  ":                            "https://example.com/trim",
		"http://[2001:DB8::1]:8080/":                              "http://[2001:db8::1]:8080/",
		"http://127.0.0.1/":                                       "http://127.0.0.1/",
		"http://2130706433/":                                      "http://2130706433/", // safefetch blocks numeric forms
	} {
		u, err := Canonicalize(in)
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if u.String() != want {
			t.Errorf("%q: %q, want %q", in, u.String(), want)
		}
	}
	for in, want := range map[string]error{
		"":                                       ErrInvalid,
		"ftp://example.com/":                     ErrScheme,
		"javascript:alert(1)":                    ErrScheme,
		"data:text/html,x":                       ErrScheme,
		"file:///etc/passwd":                     ErrScheme,
		"gopher://example.com/":                  ErrScheme,
		"//example.com/":                         ErrScheme,
		"http://user@example.com":                ErrUserinfo,
		"http://u:p@example.com/":                ErrUserinfo,
		"https:///nohost":                        ErrInvalid,
		"http://example.com:0/":                  ErrInvalid,
		"http://example.com:99999":               ErrInvalid,
		"http://[fe80::1%25eth0]/":               ErrInvalid,
		"https://exa mple.com/":                  ErrInvalid,
		"https://" + strings.Repeat("a", MaxURL): ErrInvalid,
	} {
		if _, err := Canonicalize(in); !errors.Is(err, want) {
			t.Errorf("%q: %v, want %v", in, err, want)
		}
	}
}

func TestStripTracking(t *testing.T) {
	for in, want := range map[string]string{
		"":                                     "",
		"utm_source=x":                         "",
		"a=1&utm_campaign=2&b=3":               "a=1&b=3",
		"pk_kwd=1&mtm_x=2&hsa_a=3&_ga=4&_gl=5": "",
		"fbclid":                               "",
		"FBCLID=1&keep":                        "keep",
	} {
		if got := StripTracking(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func host(t *testing.T, raw string) (string, bool) {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return DisplayHost(u)
}

func TestDisplayHost(t *testing.T) {
	for raw, want := range map[string]struct {
		host      string
		lookalike bool
	}{
		"https://example.com/":                     {"example.com", false},
		"https://xn--bcher-kva.example/":           {"bücher.example", false},
		"https://xn--80ak6aa92e.com/":              {"аррӏе.com", true},  // Cyrillic letters that all read as Latin
		"https://xn--pple-43d.com/":                {"аpple.com", true},  // Cyrillic а + Latin
		"https://xn--e1afmkfd.xn--p1ai/":           {"пример.рф", false}, // ordinary Cyrillic
		"https://xn--mxahbxey0c.gr/":               {"εχαμπλε.gr", false},
		"https://xn--wgv71a119e.jp/":               {"日本語.jp", false},
		"https://xn--eckwd4c7c.xn--zckzah/":        {"ドメイン.テスト", false},
		"https://xn--hxajbheg2az3al.xn--jxalpdlp/": {"παράδειγμα.δοκιμή", false},
		"https://xn--fsq.xn--3e0b707e/":            {"例.한국", false},
		"https://xn--n3h.example/":                 {"☃.example", false},
		"https://xn--invalid-.example/":            {"xn--invalid-.example", true},
		"http://127.0.0.1:8080/":                   {"127.0.0.1", false},
		"http://[::1]/":                            {"::1", false},
	} {
		h, l := host(t, raw)
		if h != want.host || l != want.lookalike {
			t.Errorf("%s: %q %v, want %q %v", raw, h, l, want.host, want.lookalike)
		}
	}
}

func TestLookalike(t *testing.T) {
	for label, want := range map[string]bool{
		"paypal": false, "раураl": true, "раураӏ": true, "gοogle": true, "οο": true, "café": false,
		"日本ご": false, "テストtest": false, "中文abc": false, "한국abc": false, "한국テスト": true,
		"օօ": true, "123": false, "": false, "xn": false, "اختبار": false, "testاختبار": true,
	} {
		if got := Lookalike(label); got != want {
			t.Errorf("%q: %v, want %v", label, got, want)
		}
	}
}

func TestCleanText(t *testing.T) {
	for _, c := range []struct {
		in   string
		max  int
		want string
	}{
		{"  Hello\n\tworld  ", 200, "Hello world"},
		{"evil\u202Egnp.exe", 200, "evilgnp.exe"},               // RLO removed
		{"a\u200Bb\u200Cc\u200Dd\u2060e\uFEFFf", 200, "abcdef"}, // zero-width and BOM
		{"\u2066iso\u2069 \u200Emark\u200F", 200, "iso mark"},
		{"soft\u00ADhyphen", 200, "softhyphen"},
		{"bell\x07 nul\x00 del\x7f", 200, "bell nul del"},
		{"é", 200, "é"}, // NFC
		{"\u3164\u3164", 200, ""},
		{"abcdef", 3, "abc"},
		{"ab cd", 3, "ab"},
		{"😀😀😀😀", 2, "😀😀"},
		{"line\u2028sep", 200, "line sep"},
		{"tag\U000E0041chars", 200, "tagchars"},
	} {
		if got := CleanText(c.in, c.max); got != c.want {
			t.Errorf("%q/%d: %q, want %q", c.in, c.max, got, c.want)
		}
	}
	rapid.Check(t, func(t *rapid.T) {
		s := rapid.String().Draw(t, "s")
		max := rapid.IntRange(0, 50).Draw(t, "max")
		got := CleanText(s, max)
		if utf8.RuneCountInString(got) > max || strings.TrimSpace(got) != got || strings.Contains(got, "  ") {
			t.Fatalf("%q/%d → %q", s, max, got)
		}
		for _, r := range got {
			if invisible(r) {
				t.Fatalf("%q/%d → %q keeps %U", s, max, got, r)
			}
		}
	})
}
