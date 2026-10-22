// Package scan is the pre-storage CSAM scanner abstraction (ARCHITECTURE §6.1).
// Drivers: "photodna" (PhotoDNA Cloud Matching API), "dev" (SHA-256 blocklist,
// constructible only when cfg.DevLike()), "none" (reports disabled), "unscanned"
// (never matches; only with PCC_ALLOW_UNSCANNED_MEDIA=1). There is no "cloudflare"
// driver: Cloudflare's tool scans after storage.
//
// A scanner error or timeout must reject the upload (fail closed): callers write
// nothing to any store unless Scan returned a non-matching Verdict and nil error.
//
// Owner: P1-W04.
package scan

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strings"

	"github.com/Nibroc6/purple-chicken-cult/internal/platform/config"
)

// Verdict is a scan result. Info carries scanner metadata only (what moderators see).
type Verdict struct {
	Match bool
	Info  map[string]string
}

// Scanner checks an image before it is stored anywhere.
type Scanner interface {
	Name() string
	Scan(ctx context.Context, img []byte, mime string) (Verdict, error)
}

// Errors.
var (
	ErrDisabled = errors.New("scan: scanner disabled")
	ErrDevOnly  = errors.New("scan: the dev scanner is only available in dev/test")
)

// DevBlocklistEnv names the variable holding the dev blocklist path.
const DevBlocklistEnv = "PCC_DEV_SCAN_BLOCKLIST"

// New builds the configured scanner.
func New(c *config.Config) (Scanner, error) {
	switch c.Scanner.Driver {
	case config.ScannerDev:
		if !c.DevLike() {
			return None{}, ErrDevOnly
		}
		path := os.Getenv(DevBlocklistEnv)
		if path == "" {
			path = "testdata/scan/blocklist.txt"
		}
		return NewDev(path)
	case config.ScannerPhotoDNA:
		if c.Scanner.PhotoDNAKey == "" || c.Scanner.PhotoDNAURL == "" {
			return None{}, ErrDisabled
		}
		return NewPhotoDNA(c.Scanner.PhotoDNAURL, c.Scanner.PhotoDNAKey), nil
	case config.ScannerUnscanned:
		if !c.UnscannedMedia() {
			return None{}, ErrDisabled
		}
		return Unscanned{}, nil
	}
	return None{}, nil
}

// Unscanned is the operator-chosen no-scan driver: every image passes. Matches can
// only come from outside the app (Cloudflare's CSAM tool email, a user report).
type Unscanned struct{}

// Name identifies the driver.
func (Unscanned) Name() string { return config.ScannerUnscanned }

// Scan never matches.
func (Unscanned) Scan(ctx context.Context, _ []byte, _ string) (Verdict, error) {
	return Verdict{}, ctx.Err()
}

// None is the disabled scanner: every scan fails, so nothing is ever stored.
type None struct{}

// Name identifies the driver.
func (None) Name() string { return config.ScannerNone }

// Scan always fails closed.
func (None) Scan(context.Context, []byte, string) (Verdict, error) { return Verdict{}, ErrDisabled }

// Dev matches SHA-256 digests from a blocklist file (one lowercase hex digest per
// line, # comments). A missing file means an empty list.
type Dev struct{ block map[string]bool }

// NewDev loads the blocklist at path.
func NewDev(path string) (*Dev, error) {
	d := &Dev{block: map[string]bool{}}
	f, err := os.Open(path) //nolint:gosec // operator-configured blocklist path (dev scanner only)
	if errors.Is(err, os.ErrNotExist) {
		return d, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		d.block[strings.ToLower(line)] = true
	}
	return d, s.Err()
}

// Block adds a digest (tests).
func (d *Dev) Block(img []byte) {
	h := sha256.Sum256(img)
	d.block[hex.EncodeToString(h[:])] = true
}

// Name identifies the driver.
func (d *Dev) Name() string { return config.ScannerDev }

// Scan matches the image digest against the blocklist.
func (d *Dev) Scan(ctx context.Context, img []byte, _ string) (Verdict, error) {
	if err := ctx.Err(); err != nil {
		return Verdict{}, err
	}
	h := sha256.Sum256(img)
	if sum := hex.EncodeToString(h[:]); d.block[sum] {
		return Verdict{Match: true, Info: map[string]string{"driver": "dev", "list": "blocklist", "sha256": sum}}, nil
	}
	return Verdict{}, nil
}
