package previews

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
	"golang.org/x/net/publicsuffix"

	"github.com/Nibroc6/purple-chicken-cult/internal/platform/urlx"
)

// result is what a preview shows (ok) or that it failed.
type result struct {
	ok           bool
	title, descr string
}

// lookup answers from the cache, else fetches within the per-host limiter and
// caches the outcome (failures too, so a broken link is not refetched for
// every post within the TTL). A limiter refusal is not cached.
func (s *Service) lookup(ctx context.Context, t target) result {
	now := s.now()
	var (
		st           int16
		title, descr *string
	)
	err := s.d.DB.Q().QueryRow(ctx, `SELECT status, title, description FROM link_fetch_cache
		WHERE url_canon = $1 AND expires_at > $2`, t.u.String(), now).Scan(&st, &title, &descr)
	switch {
	case err == nil:
		return result{ok: st == statusOK && deref(title) != "", title: deref(title), descr: deref(descr)}
	case !errors.Is(err, pgx.ErrNoRows):
		s.log().Error("previews: cache read", "err", err.Error())
		return result{}
	}
	if !s.limit.allow(hostKey(t.host), now) {
		return result{}
	}
	r := s.fetchMeta(ctx, t)
	st = statusFailed
	if r.ok {
		st = statusOK
	}
	if _, err := s.d.DB.Q().Exec(ctx, `INSERT INTO link_fetch_cache (url_canon, status, title, description, site_host,
		fetched_at, expires_at) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (url_canon) DO UPDATE SET status = EXCLUDED.status, title = EXCLUDED.title,
		description = EXCLUDED.description, site_host = EXCLUDED.site_host, fetched_at = EXCLUDED.fetched_at,
		expires_at = EXCLUDED.expires_at`,
		t.u.String(), st, nilIfEmpty(r.title), nilIfEmpty(r.descr), t.host, now, now.Add(CacheTTL)); err != nil {
		s.log().Error("previews: cache write", "err", err.Error())
	}
	s.purge(ctx, now)
	return r
}

// purge deletes expired cache rows, at most every ten minutes per process.
func (s *Service) purge(ctx context.Context, now time.Time) {
	s.mu.Lock()
	due := !now.Before(s.purgeAt)
	if due {
		s.purgeAt = now.Add(10 * time.Minute)
	}
	s.mu.Unlock()
	if !due {
		return
	}
	if _, err := s.d.DB.Q().Exec(ctx, "DELETE FROM link_fetch_cache WHERE expires_at <= $1", now); err != nil {
		s.log().Error("previews: cache purge", "err", err.Error())
	}
}

func (s *Service) fetchMeta(ctx context.Context, t target) result {
	res, err := s.fetch.GetHTML(ctx, t.u)
	if err != nil {
		return result{}
	}
	title, descr := parseMeta(res.Body, res.ContentType, res.Charset)
	title, descr = urlx.CleanText(title, MaxTitle), urlx.CleanText(descr, MaxDescription)
	return result{ok: title != "", title: title, descr: descr}
}

// parseMeta reads the title and description of an HTML document:
// og:title | twitter:title | <title>, og:description | twitter:description |
// <meta name=description>. The document's charset (header, BOM or <meta>) is
// honored.
func parseMeta(body []byte, mediaType, cs string) (title, descr string) {
	ct := mediaType
	if cs != "" {
		ct = mime.FormatMediaType(mediaType, map[string]string{"charset": cs})
	}
	r, err := charset.NewReader(bytes.NewReader(body), ct)
	if err != nil {
		r = bytes.NewReader(body)
	}
	found := map[string]string{}
	var titleTag strings.Builder
	inTitle, haveTitle := false, false
	z := html.NewTokenizer(io.LimitReader(r, 2<<20))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return pick(found, titleTag.String(), "og:title", "twitter:title"),
				pick(found, "", "og:description", "twitter:description", "description")
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			switch string(name) {
			case "title":
				inTitle = !haveTitle
			case "meta":
				if hasAttr {
					meta(z, found)
				}
			case "body":
				if len(found) > 0 || haveTitle {
					return pick(found, titleTag.String(), "og:title", "twitter:title"),
						pick(found, "", "og:description", "twitter:description", "description")
				}
			}
		case html.EndTagToken:
			if name, _ := z.TagName(); string(name) == "title" && inTitle {
				inTitle, haveTitle = false, true
			}
		case html.TextToken:
			if inTitle && titleTag.Len() < 4096 {
				titleTag.Write(z.Text())
			}
		}
	}
}

// meta records og:/twitter: properties and the description meta tag (first
// occurrence wins).
func meta(z *html.Tokenizer, found map[string]string) {
	var key, content string
	hasContent := false
	for {
		k, v, more := z.TagAttr()
		switch string(k) {
		case "property", "name":
			if n := strings.ToLower(strings.TrimSpace(string(v))); n != "" && key == "" {
				key = n
			}
		case "content":
			content, hasContent = string(v), true
		}
		if !more {
			break
		}
	}
	switch key {
	case "og:title", "twitter:title", "og:description", "twitter:description", "description":
		if _, dup := found[key]; !dup && hasContent && strings.TrimSpace(content) != "" {
			found[key] = content
		}
	}
}

// pick returns the first key found, else fallback.
func pick(found map[string]string, fallback string, keys ...string) string {
	for _, k := range keys {
		if v, ok := found[k]; ok {
			return v
		}
	}
	return fallback
}

// hostKey is the limiter key: the registrable domain, so subdomains of one
// site share a budget; IP literals and unknown suffixes key on the host.
func hostKey(host string) string {
	if d, err := publicsuffix.EffectiveTLDPlusOne(host); err == nil {
		return d
	}
	return host
}

// limiter is a token bucket per host: 1 request per 2 s, burst 3, in memory
// (plan §3.2: not stored).
type limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	at     time.Time
}

const (
	limitBurst  = 3
	limitPeriod = 2 * time.Second
	limitMax    = 10_000 // hosts tracked before full buckets are forgotten
)

func newLimiter() *limiter { return &limiter{buckets: map[string]*bucket{}} }

func (l *limiter) allow(host string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[host]
	if !ok {
		if len(l.buckets) >= limitMax {
			l.gc(now)
		}
		b = &bucket{tokens: limitBurst, at: now}
		l.buckets[host] = b
	}
	if el := now.Sub(b.at); el > 0 {
		b.tokens = min(limitBurst, b.tokens+el.Seconds()/limitPeriod.Seconds())
		b.at = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// gc forgets buckets that would be full again by now.
func (l *limiter) gc(now time.Time) {
	for h, b := range l.buckets {
		if b.tokens+now.Sub(b.at).Seconds()/limitPeriod.Seconds() >= limitBurst {
			delete(l.buckets, h)
		}
	}
}

func (l *limiter) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buckets = map[string]*bucket{}
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
