package scan

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gen2brain/webp"

	"github.com/Nibroc6/purple-chicken-cult/internal/platform/config"
)

// Every image here is a generated colour pattern: tests use synthetic hash
// matches only, never real imagery.
func pattern(w, h int, seed uint8) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.NRGBA{R: uint8(x) ^ seed, G: uint8(y), B: seed, A: 255}) //nolint:gosec // test pattern
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func TestDevScanner(t *testing.T) {
	d, err := NewDev("does-not-exist.txt")
	if err != nil {
		t.Fatal(err)
	}
	bad := pattern(8, 8, 1)
	d.Block(bad)
	v, err := d.Scan(context.Background(), bad, "image/png")
	if err != nil || !v.Match || v.Info["driver"] != "dev" || len(v.Info["sha256"]) != 64 {
		t.Fatalf("blocked digest must match: %v %v", v, err)
	}
	v, err = d.Scan(context.Background(), pattern(8, 8, 2), "image/png")
	if err != nil || v.Match {
		t.Fatalf("other image must not match: %v %v", v, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.Scan(ctx, bad, "image/png"); err == nil {
		t.Fatal("a cancelled scan must fail")
	}
}

func TestDevBlocklistFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "blocklist.txt")
	img := pattern(4, 4, 9)
	d0, _ := NewDev("")
	d0.Block(img)
	var sum string
	for k := range d0.block {
		sum = k
	}
	if err := os.WriteFile(p, []byte("# synthetic\n\n"+strings.ToUpper(sum)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := NewDev(p)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := d.Scan(context.Background(), img, "image/png"); !v.Match {
		t.Fatal("blocklist file entry must match (case-insensitive)")
	}
}

// TestRepoBlocklist: the committed dev blocklist parses and holds only
// synthetic test patterns (digests, no image data).
func TestRepoBlocklist(t *testing.T) {
	d, err := NewDev(filepath.Join("..", "..", "..", "testdata", "scan", "blocklist.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.block) == 0 {
		t.Fatal("the dev blocklist must hold the e2e test pattern")
	}
	for k := range d.block {
		if len(k) != 64 {
			t.Fatalf("not a sha256 digest: %q", k)
		}
	}
}

func TestFailClosed(t *testing.T) {
	if _, err := New(&config.Config{Env: config.EnvProd, Scanner: config.ScannerConfig{Driver: config.ScannerDev}}); !errors.Is(err, ErrDevOnly) {
		t.Fatalf("dev scanner in prod must be refused: %v", err)
	}
	s, _ := New(&config.Config{Env: config.EnvProd, Scanner: config.ScannerConfig{Driver: config.ScannerNone}})
	if _, err := s.Scan(context.Background(), []byte("x"), "image/png"); err == nil {
		t.Fatal("none scanner must fail every scan")
	}
	if _, err := New(&config.Config{Env: config.EnvProd, Scanner: config.ScannerConfig{Driver: config.ScannerPhotoDNA}}); !errors.Is(err, ErrDisabled) {
		t.Fatalf("photodna without key must be disabled: %v", err)
	}
	p, err := New(&config.Config{Env: config.EnvProd, Scanner: config.ScannerConfig{Driver: config.ScannerPhotoDNA, PhotoDNAKey: "k", PhotoDNAURL: "http://127.0.0.1:1/Match"}})
	if err != nil || p.Name() != "photodna" {
		t.Fatal(err)
	}
	if _, err := p.Scan(context.Background(), pattern(200, 200, 1), "image/png"); err == nil {
		t.Fatal("an unreachable matcher must fail closed")
	}
}

type fakeDNA struct {
	status int
	body   string
	delay  time.Duration
	got    struct {
		key, ct string
		body    []byte
	}
}

func (f *fakeDNA) server(t *testing.T) *PhotoDNA {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.got.key, f.got.ct = r.Header.Get("Ocp-Apim-Subscription-Key"), r.Header.Get("Content-Type")
		f.got.body, _ = io.ReadAll(r.Body)
		if f.delay > 0 {
			select {
			case <-time.After(f.delay):
			case <-r.Context().Done():
				return
			}
		}
		w.WriteHeader(f.status)
		_, _ = w.Write([]byte(f.body))
	}))
	t.Cleanup(srv.Close)
	return NewPhotoDNA(srv.URL+"/photodna/v1.0/Match", "sub-key").WithClient(srv.Client())
}

const (
	okNoMatch = `{"Status":{"Code":3000,"Description":"OK"},"IsMatch":false,"TrackingId":"t1","MatchDetails":{"MatchFlags":[]}}`
	okMatch   = `{"Status":{"Code":3000,"Description":"OK"},"IsMatch":true,"TrackingId":"t2",` +
		`"MatchDetails":{"MatchFlags":[{"Source":"Test","Violations":["A1"]}]}}`
)

func TestPhotoDNAVerdicts(t *testing.T) {
	ctx := context.Background()
	img := pattern(200, 180, 3)
	f := &fakeDNA{status: 200, body: okNoMatch}
	p := f.server(t)
	v, err := p.Scan(ctx, img, "image/png")
	if err != nil || v.Match {
		t.Fatalf("no match: %v %v", v, err)
	}
	if f.got.key != "sub-key" || f.got.ct != "image/png" || !bytes.Equal(f.got.body, img) {
		t.Fatalf("request: key %q ct %q", f.got.key, f.got.ct)
	}
	f.body = okMatch
	v, err = p.Scan(ctx, img, "image/png")
	if err != nil || !v.Match || v.Info["tracking_id"] != "t2" || v.Info["sources"] != "Test" || v.Info["violations"] != "A1" {
		t.Fatalf("match: %v %v", v, err)
	}
}

// Every unusable answer is an error (the upload is rejected, nothing stored).
func TestPhotoDNAFailsClosed(t *testing.T) {
	img := pattern(200, 200, 4)
	for name, f := range map[string]*fakeDNA{
		"http 500":       {status: 500, body: okNoMatch},
		"http 429":       {status: 429, body: ""},
		"api error code": {status: 200, body: `{"Status":{"Code":3002,"Description":"Invalid"},"IsMatch":false}`},
		"no verdict":     {status: 200, body: `{"Status":{"Code":3000}}`},
		"garbage":        {status: 200, body: `<html>`},
	} {
		if _, err := f.server(t).Scan(context.Background(), img, "image/png"); err == nil {
			t.Errorf("%s: must fail", name)
		}
	}
	slow := &fakeDNA{status: 200, body: okNoMatch, delay: 5 * time.Second}
	p := slow.server(t)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := p.Scan(ctx, img, "image/png"); err == nil {
		t.Fatal("timeout must fail")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("the caller's deadline must bound the call")
	}
	if _, err := (&fakeDNA{status: 200, body: okNoMatch}).server(t).Scan(context.Background(), []byte("junk"), "image/png"); err == nil {
		t.Fatal("undecodable bytes must fail")
	}
}

// WebP and images under 160 px are converted to PNG (≥ 160 px) for matching.
func TestPhotoDNAPrepare(t *testing.T) {
	f := &fakeDNA{status: 200, body: okNoMatch}
	p := f.server(t)
	src := image.NewNRGBA(image.Rect(0, 0, 40, 20))
	var wb bytes.Buffer
	if err := webp.Encode(&wb, src, webp.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Scan(context.Background(), wb.Bytes(), "image/webp"); err != nil {
		t.Fatal(err)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(f.got.body))
	if err != nil || format != "png" || f.got.ct != "image/png" || cfg.Width < minSide || cfg.Height < minSide {
		t.Fatalf("prepared %q %dx%d %v", format, cfg.Width, cfg.Height, err)
	}
}
