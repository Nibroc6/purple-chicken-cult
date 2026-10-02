// Package safefetch is the SSRF-guarded fetcher for link previews (ARCHITECTURE
// §10.1.6, plan §4.2): http/https on ports 80/443 only, no userinfo, no proxy, no
// cookies, private/loopback/link-local/CGNAT/metadata addresses denied on every dial
// (rebinding-proof), ≤3 redirects re-checked, 3 s deadline, 512 KiB body cap after
// decompression, text/html only.
//
// Owner: P0-W09 → P1-W11. SCAFFOLD STUB: every fetch fails closed with ErrBlocked.
package safefetch

import (
	"context"
	"errors"
	"net/url"
)

// Response is a fetched document.
type Response struct {
	FinalURL    *url.URL
	ContentType string
	Body        []byte
}

// Errors.
var (
	ErrBlocked   = errors.New("safefetch: blocked")
	ErrTooLarge  = errors.New("safefetch: too large")
	ErrType      = errors.New("safefetch: content type")
	ErrRedirects = errors.New("safefetch: too many redirects")
)

// Client fetches untrusted URLs.
type Client struct {
	allowLoopback bool //nolint:unused // used by newForTest (P0-W09)
}

// New returns a production client.
func New() *Client { return &Client{} }

//nolint:unused // used by the owning WP's in-package tests
func newForTest(allowLoopback bool) *Client { return &Client{allowLoopback: allowLoopback} }

// GetHTML fetches an HTML page. Scaffold: fails closed.
func (c *Client) GetHTML(ctx context.Context, u *url.URL) (*Response, error) {
	return nil, ErrBlocked
}

// GetImage fetches an image of at most max bytes (P1 preview images). Scaffold: fails closed.
func (c *Client) GetImage(ctx context.Context, u *url.URL, max int64) (*Response, error) {
	return nil, ErrBlocked
}
