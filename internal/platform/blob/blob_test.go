package blob

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Nibroc6/purple-chicken-cult/internal/platform/config"
)

func TestFSRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, err := NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key := "abcdefghijklmnopqrstuvwxyz234567"
	if err := s.Put(ctx, key, strings.NewReader("hello"), 5, "image/webp"); err != nil {
		t.Fatal(err)
	}
	r, n, _, err := s.Get(ctx, key)
	if err != nil || n != 5 {
		t.Fatalf("get: %v %d", err, n)
	}
	b, _ := io.ReadAll(r)
	_ = r.Close()
	if string(b) != "hello" {
		t.Fatalf("content %q", b)
	}
	if err := s.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.Get(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
	if err := s.Put(ctx, "../etc/passwd", strings.NewReader("x"), 1, ""); !errors.Is(err, ErrBadKey) {
		t.Fatalf("path traversal key must be rejected: %v", err)
	}
}

func TestProdFSFailsClosed(t *testing.T) {
	c := &config.Config{Env: config.EnvProd, Scanner: config.ScannerConfig{Driver: config.ScannerDev}}
	b := config.BlobConfig{Driver: config.BlobFS, FSDir: t.TempDir()}
	if _, err := New(c, b, nil); err == nil {
		t.Fatal("prod fs blob without allow flag and real scanner must fail")
	}
	c.AllowFSBlob = true
	if _, err := New(c, b, nil); err == nil {
		t.Fatal("prod fs blob with the dev scanner must fail")
	}
	c.Scanner.Driver = config.ScannerPhotoDNA
	if _, err := New(c, b, nil); err != nil {
		t.Fatalf("allowed fs blob: %v", err)
	}
	if _, err := New(c, config.BlobConfig{}, nil); !errors.Is(err, ErrDisabled) {
		t.Fatalf("unconfigured store: %v", err)
	}
}
