package scan

import (
	"context"
	"errors"
	"testing"

	"github.com/Nibroc6/purple-chicken-cult/internal/platform/config"
)

func TestDevScanner(t *testing.T) {
	d, err := NewDev("does-not-exist.txt")
	if err != nil {
		t.Fatal(err)
	}
	bad := []byte("bad image")
	d.Block(bad)
	v, err := d.Scan(context.Background(), bad, "image/png")
	if err != nil || !v.Match {
		t.Fatalf("blocked digest must match: %v %v", v, err)
	}
	v, err = d.Scan(context.Background(), []byte("ok"), "image/png")
	if err != nil || v.Match {
		t.Fatalf("other image must not match: %v %v", v, err)
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
	p, err := New(&config.Config{Env: config.EnvProd, Scanner: config.ScannerConfig{Driver: config.ScannerPhotoDNA, PhotoDNAKey: "k", PhotoDNAURL: "https://x"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Scan(context.Background(), []byte("x"), "image/png"); err == nil {
		t.Fatal("unimplemented photodna must fail closed")
	}
}
