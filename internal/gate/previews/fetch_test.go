package previews

import (
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
		title, descr := parseMeta([]byte(c.html), "text/html", c.cs)
		if title != c.title || descr != c.descr {
			t.Errorf("%s: %q %q, want %q %q", c.name, title, descr, c.title, c.descr)
		}
	}
}

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
