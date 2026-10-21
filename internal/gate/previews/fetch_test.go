package previews

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestParseMeta(t *testing.T) {
	for _, c := range []struct {
		name, html, cs string
		title, descr   string
	}{
		{"og wins", `<title>T</title><meta property="og:title" content="OG"><meta name="twitter:title" content="TW">`, "", "OG", ""},
		{"twitter before title", `<head><title>T</title><meta name="twitter:title" content="TW"></head>`, "", "TW", ""},
		{"title tag", `<html><head><title>Hello &amp; welcome</title></head></html>`, "", "Hello & welcome", ""},
		{"first title only", `<title>One</title><title>Two</title>`, "", "One", ""},
		{"descriptions", `<meta name="description" content="D"><meta name="twitter:description" content="TD">`, "", "", "TD"},
		{"og description", `<meta name="description" content="D"><meta property="og:description" content="OGD">`, "", "", "OGD"},
		{"first occurrence", `<meta property="og:title" content="A"><meta property="og:title" content="B">`, "", "A", ""},
		{"empty content ignored", `<meta property="og:title" content=" "><title>T</title>`, "", "T", ""},
		{"case-insensitive names", `<META PROPERTY="OG:TITLE" CONTENT="Up">`, "", "Up", ""},
		{"stops at body", `<head><title>H</title></head><body><meta property="og:title" content="Body"></body>`, "", "H", ""},
		{"latin-1 header", "<title>Caf\xe9</title>", "ISO-8859-1", "Café", ""},
		{"latin-1 meta", "<meta charset=\"windows-1252\"><title>\x93q\x94</title>", "", "“q”", ""},
		{"script is not title", `<script>var t="<title>x</title>"</script><title>Real</title>`, "", "Real", ""},
		{"nothing", `<p>text</p>`, "", "", ""},
	} {
		m := parseMeta([]byte(c.html), "text/html", c.cs)
		if m.title != c.title || m.descr != c.descr {
			t.Errorf("%s: %q %q, want %q %q", c.name, m.title, m.descr, c.title, c.descr)
		}
	}
}

func TestParseMetaImage(t *testing.T) {
	for _, c := range []struct{ name, html, image string }{
		{"og image", `<meta property="og:image" content="/a.png"><meta name="twitter:image" content="/b.png">`, "/a.png"},
		{"twitter image", `<meta name="twitter:image" content="https://cdn.example/b.png">`, "https://cdn.example/b.png"},
		{"first wins", `<meta property="og:image" content="1.png"><meta property="og:image" content="2.png">`, "1.png"},
		{"none", `<title>T</title>`, ""},
		{"in body ignored", `<head><title>T</title></head><body><meta property="og:image" content="x.png"></body>`, ""},
	} {
		if got := parseMeta([]byte(c.html), "text/html", "").image; got != c.image {
			t.Errorf("%s: %q, want %q", c.name, got, c.image)
		}
	}
}

func TestImageURL(t *testing.T) {
	page := mustParse(t, "https://news.example/a/b?id=1")
	for raw, want := range map[string]string{
		"/img/x.png":                         "https://news.example/img/x.png",
		"x.png":                              "https://news.example/a/x.png",
		"//cdn.example/x.png?utm_source=og":  "https://cdn.example/x.png",
		"  https://CDN.example/x.png#frag  ": "https://cdn.example/x.png",
		"http://cdn.example:80/x.png":        "http://cdn.example/x.png",
		"javascript:alert(1)":                "",
		"data:image/png;base64,AAAA":         "",
		"https://user:pw@cdn.example/x.png":  "",
		"ftp://cdn.example/x.png":            "",
		"":                                   "",
		"https://exa mple.com/x.png":         "",
		"https://cdn.example/" + long(3000):  "",
	} {
		if got := imageURL(page, page, raw); got != want {
			t.Errorf("%q: %q, want %q", raw, got, want)
		}
	}
	// The final URL after redirects is the base.
	if got := imageURL(mustParse(t, "https://moved.example/p/"), page, "i.png"); got != "https://moved.example/p/i.png" {
		t.Error(got)
	}
}

func mustParse(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func long(n int) string { return strings.Repeat("a", n) }

func TestLimiter(t *testing.T) {
	l := newLimiter()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range 3 {
		if !l.allow("a.example", t0) {
			t.Fatalf("burst %d refused", i)
		}
	}
	if l.allow("a.example", t0.Add(time.Second)) {
		t.Fatal("a fourth request within 2 s")
	}
	if !l.allow("b.example", t0) {
		t.Fatal("hosts share a bucket")
	}
	if !l.allow("a.example", t0.Add(2*time.Second)) || l.allow("a.example", t0.Add(2*time.Second)) {
		t.Fatal("one token per 2 s")
	}
	if !l.allow("a.example", t0.Add(time.Hour)) {
		t.Fatal("refill")
	}
	l.gc(t0.Add(2 * time.Hour))
	if len(l.buckets) != 0 {
		t.Fatalf("gc kept %d", len(l.buckets))
	}
}

func TestHostKey(t *testing.T) {
	for in, want := range map[string]string{
		"a.b.example.com": "example.com", "www.bbc.co.uk": "bbc.co.uk", "example.com": "example.com",
		"127.0.0.1": "127.0.0.1", "2001:db8::1": "2001:db8::1", "x.github.io": "x.github.io",
	} {
		if got := hostKey(in); got != want {
			t.Errorf("%s: %s, want %s", in, got, want)
		}
	}
}
