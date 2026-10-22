package blob

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func testSigner() *Signer { return NewSigner([]byte("0123456789abcdef0123456789abcdef")) }

func TestNewKeyRandomBase32(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		k, err := NewKey()
		if err != nil {
			t.Fatal(err)
		}
		if len(k) != KeyLen || !ValidKey(k) || seen[k] {
			t.Fatalf("key %q (len %d, valid %v, dup %v)", k, len(k), ValidKey(k), seen[k])
		}
		seen[k] = true
	}
}

func parse(t *testing.T, raw string) (string, url.Values) {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimPrefix(u.Path, "/m/"), u.Query()
}

func TestSignedURLRoundTrip(t *testing.T) {
	s := testSigner()
	key, _ := NewKey()
	raw := s.URL("https://media.example/m", key, t0.Add(time.Hour), s.ViewerTag(7))
	k, q := parse(t, raw)
	exp, err := s.Verify(k, q, t0)
	if err != nil || !exp.Equal(t0.Add(time.Hour)) {
		t.Fatalf("verify %s: %v %v", raw, exp, err)
	}
	if _, err := s.Verify(k, q, t0.Add(time.Hour)); !errors.Is(err, ErrExpired) {
		t.Fatalf("at expiry: %v", err)
	}
	if _, err := s.Verify(k, q, t0.Add(2*time.Hour)); !errors.Is(err, ErrExpired) {
		t.Fatalf("after expiry: %v", err)
	}
}

// Every tampering is ErrSignature: another viewer's tag, another expiry,
// another key, another signer, a truncated or repeated parameter, extras.
func TestSignedURLTampering(t *testing.T) {
	s := testSigner()
	key, _ := NewKey()
	other, _ := NewKey()
	_, q := parse(t, s.URL("https://m.example/m", key, t0.Add(time.Hour), s.ViewerTag(7)))
	mut := func(f func(url.Values)) url.Values {
		c := url.Values{}
		for k, v := range q {
			c[k] = append([]string(nil), v...)
		}
		f(c)
		return c
	}
	otherTag := s.ViewerTag(8)
	cases := map[string]url.Values{
		"other viewer tag": mut(func(c url.Values) { c.Set("v", b64(otherTag[:])) }),
		"later expiry":     mut(func(c url.Values) { c.Set("e", "1767229201") }),
		"leading zero":     mut(func(c url.Values) { c.Set("e", "0"+c.Get("e")) }),
		"no signature":     mut(func(c url.Values) { c.Del("s") }),
		"empty signature":  mut(func(c url.Values) { c.Set("s", "") }),
		"short signature":  mut(func(c url.Values) { c.Set("s", c.Get("s")[:42]) }),
		"repeated e":       mut(func(c url.Values) { c.Add("e", c.Get("e")) }),
		"extra param":      mut(func(c url.Values) { c.Set("x", "1") }),
		"non-digit e":      mut(func(c url.Values) { c.Set("e", "+1767229200") }),
		"unsigned":         {},
	}
	for name, c := range cases {
		if _, err := s.Verify(key, c, t0); !errors.Is(err, ErrSignature) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := s.Verify(other, q, t0); !errors.Is(err, ErrSignature) {
		t.Errorf("other key: %v", err)
	}
	if _, err := NewSigner([]byte("another key of thirty-two bytes!")).Verify(key, q, t0); !errors.Is(err, ErrSignature) {
		t.Errorf("other signer: %v", err)
	}
	if _, err := s.Verify("../"+key[3:], q, t0); !errors.Is(err, ErrSignature) {
		t.Errorf("bad key shape: %v", err)
	}
	// A correctly signed URL that expires too far ahead is refused too.
	_, far := parse(t, s.URL("https://m.example/m", key, t0.Add(3*time.Hour), s.ViewerTag(7)))
	if _, err := s.Verify(key, far, t0); !errors.Is(err, ErrSignature) {
		t.Errorf("far expiry: %v", err)
	}
}

func TestViewerTagsDiffer(t *testing.T) {
	s := testSigner()
	if s.ViewerTag(1) == s.ViewerTag(2) || s.ViewerTag(1) != testSigner().ViewerTag(1) {
		t.Fatal("viewer tags must be per viewer and deterministic")
	}
}

// TestWorkerVectors pins the signature the Cloudflare Worker must reproduce
// (deploy/cloudflare/testvectors.json, checked by media-worker.test.mjs).
func TestWorkerVectors(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "deploy", "cloudflare", "testvectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vs []struct {
		KeyHex string `json:"key_hex"`
		Viewer uint64 `json:"viewer"`
		Blob   string `json:"blob"`
		Exp    int64  `json:"e"`
		Query  string `json:"query"`
	}
	if err := json.Unmarshal(b, &vs); err != nil || len(vs) == 0 {
		t.Fatalf("vectors: %v", err)
	}
	for _, v := range vs {
		k := make([]byte, len(v.KeyHex)/2)
		for i := range k {
			var c byte
			for _, h := range v.KeyHex[2*i : 2*i+2] {
				c <<= 4
				switch {
				case h >= '0' && h <= '9':
					c |= byte(h - '0')
				default:
					c |= byte(h-'a') + 10
				}
			}
			k[i] = c
		}
		s := NewSigner(k)
		got := s.Query(v.Blob, time.Unix(v.Exp, 0), s.ViewerTag(v.Viewer))
		if got != v.Query {
			t.Errorf("vector %s: query %s, want %s", v.Blob, got, v.Query)
		}
	}
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func TestMemStore(t *testing.T) {
	ctx := context.Background()
	m := NewMem("evidence")
	key, _ := NewKey()
	if err := m.Put(ctx, key, strings.NewReader("abc"), 3, "image/png"); err != nil {
		t.Fatal(err)
	}
	r, n, mime, err := m.Get(ctx, key)
	if err != nil || n != 3 || mime != "image/png" {
		t.Fatalf("get %d %q %v", n, mime, err)
	}
	b, _ := io.ReadAll(r)
	if string(b) != "abc" || len(m.Keys()) != 1 {
		t.Fatalf("content %q keys %v", b, m.Keys())
	}
	if err := m.Put(ctx, key, strings.NewReader("abcd"), 3, ""); err == nil {
		t.Fatal("size mismatch must fail")
	}
	_ = m.Delete(ctx, key)
	if _, _, _, err := m.Get(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	m.FailPut = true
	if err := m.Put(ctx, key, strings.NewReader("abc"), 3, ""); err == nil || len(m.Keys()) != 0 {
		t.Fatal("FailPut must refuse")
	}
}

func TestCloudflarePurge(t *testing.T) {
	var got [][]string
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.URL.Path != "/zones/zone1/purge_cache" || r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var in struct{ Files []string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		got = append(got, in.Files)
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer srv.Close()
	c := NewCloudflare("zone1", "tok").WithAPI(srv.URL, srv.Client())
	urls := make([]string, 31)
	for i := range urls {
		urls[i] = PublicURL("https://media.example/m", "k")
	}
	if err := c.Purge(context.Background(), urls); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || len(got[0]) != 30 || len(got[1]) != 1 || auth != "Bearer tok" {
		t.Fatalf("batches %d auth %q", len(got), auth)
	}
	bad := NewCloudflare("other", "tok").WithAPI(srv.URL, srv.Client())
	if err := bad.Purge(context.Background(), urls[:1]); err == nil {
		t.Fatal("a failed purge must be an error")
	}
	r := &Recorder{}
	_ = r.Purge(context.Background(), []string{"a"})
	if len(r.URLs()) != 1 || r.Name() != "none" {
		t.Fatal("recorder")
	}
}
