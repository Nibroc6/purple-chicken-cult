package blob

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/platform/config"
)

// Purger removes media URLs from the CDN edge cache after the media is deleted
// (security #27). The dev/test driver records what it was asked to purge.
type Purger interface {
	Name() string
	Purge(ctx context.Context, urls []string) error
}

// NewPurger builds the configured purger: cloudflare, or the recording driver
// when no CDN purge is configured (dev/test have no CDN; in prod the edge cache
// TTL is capped at the URL lifetime and gate/media logs a WARN).
func NewPurger(c *config.Config) Purger {
	if c.CDN.Purge == config.CDNPurgeCloudflare {
		return NewCloudflare(c.CDN.ZoneID, c.CDN.APIToken)
	}
	return &Recorder{}
}

// Recorder is the no-CDN purger: it keeps the purged URLs in memory.
type Recorder struct {
	mu   sync.Mutex
	urls []string
	// Fail makes every purge fail (tests of retries).
	Fail bool
}

// Name identifies the driver.
func (r *Recorder) Name() string { return "none" }

// Purge records urls.
func (r *Recorder) Purge(_ context.Context, urls []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Fail {
		return errors.New("blob: purge refused")
	}
	r.urls = append(r.urls, urls...)
	return nil
}

// URLs returns the purged URLs so far.
func (r *Recorder) URLs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.urls...)
}

// CloudflareAPI is the Cloudflare API base URL.
const CloudflareAPI = "https://api.cloudflare.com/client/v4"

// cloudflareBatch is the number of files purged per request.
const cloudflareBatch = 30

// Cloudflare purges by URL through the zone purge API with a scoped API token
// (permission "Zone.Cache Purge" on the media zone only).
type Cloudflare struct {
	zone, token, api string
	hc               *http.Client
}

// NewCloudflare returns the Cloudflare purger of zone.
func NewCloudflare(zone, token string) *Cloudflare {
	return &Cloudflare{zone: zone, token: token, api: CloudflareAPI, hc: &http.Client{Timeout: 10 * time.Second}}
}

// WithAPI points the purger at another API base (tests).
func (c *Cloudflare) WithAPI(base string, hc *http.Client) *Cloudflare {
	c.api, c.hc = base, hc
	return c
}

// Name identifies the driver.
func (c *Cloudflare) Name() string { return config.CDNPurgeCloudflare }

// Purge purges urls in batches; any failed batch is an error (the caller retries).
func (c *Cloudflare) Purge(ctx context.Context, urls []string) error {
	for len(urls) > 0 {
		n := min(len(urls), cloudflareBatch)
		if err := c.purge(ctx, urls[:n]); err != nil {
			return err
		}
		urls = urls[n:]
	}
	return nil
}

func (c *Cloudflare) purge(ctx context.Context, files []string) error {
	body, err := json.Marshal(map[string][]string{"files": files})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.api+"/zones/"+c.zone+"/purge_cache", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.hc.Do(req) //nolint:gosec // fixed operator-configured API host
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	var out struct {
		Success bool `json:"success"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&out); err != nil || res.StatusCode != http.StatusOK || !out.Success {
		return fmt.Errorf("blob: cloudflare purge: status %d", res.StatusCode)
	}
	return nil
}
