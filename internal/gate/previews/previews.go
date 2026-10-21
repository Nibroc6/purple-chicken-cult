// Package previews builds link previews server-side, per post, after a fixed
// delay bucket (ARCHITECTURE §10.1.6, plan §4.7 previews).
//
// Job preview.fetch{post} (enqueued by gate/posts on create and edit, unless the
// author turned previews off) takes the first MaxPerPost http(s) links that
// mdscan finds — never a <url> autolink or an image — canonicalizes them with
// urlx (tracking parameters stripped), and for each one uses the internal cache
// (link_fetch_cache, TTL 1 h) or, within the per-host limiter (1 request per
// 2 s per registrable domain, burst 3), safefetch.GetHTML. The title
// (og:title | twitter:title | <title>) and description (og:description |
// twitter:description | meta description) go through urlx.CleanText (≤ 200 code
// points each) into the post's own link_previews rows. Viewers never contact
// the target site; the API never accepts preview data from clients.
//
// Preview images (P1): the page's og:image | twitter:image (resolved against
// the final URL, canonical; cached with the page) is fetched by the server
// with safefetch.GetImage (same SSRF policy, raster image types only, at most
// MaxImageBytes, within the per-host limiter) and handed to Media.Ingest, which
// runs the same CSAM scan as an upload. A clean image's media id goes to
// link_previews.image_media_id; every post ingests its own copy (media
// source_post), so deleting a post deletes its image. A matched image is held
// and queued by Ingest and the preview shows no image; with media disabled no
// image is fetched at all.
//
// Timing (fidelity #23): a row's ready_at is always a fixed bucket from the
// version's time t (created_at, or edited_at for an edit): t+PreviewDelay when
// the fetch finished by then, else t+2·PreviewDelay when it finished by that,
// else the row fails (status 2) and is never shown. gate/views shows only
// status 1 rows whose ready_at has passed, so the completion time is never
// observable, and a cache hit looks exactly like a fetch. A fetch failure is
// final for that version (never retried; a crash re-run past bucket 2 fails
// the rows without fetching). PostDeleted drops the rows.
//
// Owner: P0-W09 → P1-W11.
package previews

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/mdscan"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/safefetch"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/urlx"
)

// MaxPerPost is the number of previews per post.
const MaxPerPost = 3

// JobFetch is the job kind (gate/posts enqueues it).
const JobFetch = "preview.fetch"

// Limits of the text fields and the cache.
const (
	MaxTitle       = 200
	MaxDescription = 200
	CacheTTL       = time.Hour
)

// Row statuses (link_previews.status).
const (
	statusPending int16 = 0
	statusOK      int16 = 1
	statusFailed  int16 = 2
)

// Fetcher fetches an HTML page (safefetch.Client; tests substitute their own).
type Fetcher interface {
	GetHTML(ctx context.Context, u *url.URL) (*safefetch.Response, error)
}

// Service is the previews module.
type Service struct {
	d     *core.Deps
	fetch Fetcher
	limit *limiter

	mu      sync.Mutex
	purgeAt time.Time // next opportunistic purge of expired cache rows
}

// New returns the service.
func New(d *core.Deps) *Service {
	return &Service{d: d, fetch: safefetch.ForConfig(d.Cfg), limit: newLimiter()}
}

// Init registers preview.fetch and drops a deleted post's previews.
func (s *Service) Init(context.Context) error {
	if s.d.Jobs != nil {
		s.d.Jobs.Handle(JobFetch, s.handle)
	}
	if s.d.Bus != nil && s.d.DB != nil {
		core.Subscribe(s.d.Bus, func(ev core.PostDeleted) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := s.drop(ctx, ev.ID); err != nil {
				s.log().Error("previews: drop on delete", "err", err.Error())
			}
		})
	}
	return nil
}

// Reset forgets the per-host limiter (test reset; nothing else is in memory).
func (s *Service) Reset() { s.limit.reset() }

func (s *Service) handle(ctx context.Context, payload json.RawMessage) error {
	var p struct {
		Post string `json:"post"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil // malformed: nothing to retry
	}
	id, err := uuid.Parse(p.Post)
	if err != nil {
		return nil
	}
	return s.Build(ctx, id)
}

// target is one link to preview.
type target struct {
	u    *url.URL
	host string // ASCII host, the trusted field
}

// Targets returns the links of body that get a preview: http(s), not <url> or
// images, canonical, distinct, at most MaxPerPost.
func Targets(body string) []*url.URL {
	var out []*url.URL
	seen := map[string]bool{}
	for _, l := range mdscan.Scan(body).Links {
		if l.NoPreview {
			continue
		}
		u, err := urlx.Canonicalize(l.Raw)
		if err != nil || seen[u.String()] {
			continue
		}
		seen[u.String()] = true
		out = append(out, u)
		if len(out) == MaxPerPost {
			break
		}
	}
	return out
}

// Build (re)builds the previews of a post's current version.
func (s *Service) Build(ctx context.Context, id core.PostID) error {
	if s.d.DB == nil || s.d.Posts == nil {
		return nil
	}
	m, ok := s.d.Posts.Meta(ctx, id)
	if !ok || m.Deleted {
		return s.drop(ctx, id)
	}
	body, ok := s.d.Posts.Bodies(ctx, []core.PostID{id})[id]
	if !ok {
		return s.drop(ctx, id)
	}
	base := m.CreatedAt
	if m.Version > 1 && m.EditedAt != nil {
		base = *m.EditedAt
	}
	delay := s.delay()
	b1, b2 := base.Add(delay), base.Add(2*delay)

	var ts []target
	for _, u := range Targets(body) {
		ts = append(ts, target{u: u, host: u.Hostname()})
	}
	err := s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "DELETE FROM link_previews WHERE post_id = $1", id); err != nil {
			return err
		}
		for i, t := range ts {
			if _, err := tx.Exec(ctx, `INSERT INTO link_previews (post_id, ord, url_canon, site_host, status, ready_at)
				VALUES ($1, $2, $3, $4, $5, $6)`, id, int16(i), t.u.String(), t.host, statusPending, b1); err != nil { //nolint:gosec // i < MaxPerPost
				return err
			}
		}
		return nil
	})
	if err != nil || len(ts) == 0 {
		return err
	}
	if s.now().After(b2) { // a late re-run never fetches: the result could not be shown anyway
		_, err := s.d.DB.Q().Exec(ctx, "UPDATE link_previews SET status = $2 WHERE post_id = $1 AND status = $3",
			id, statusFailed, statusPending)
		return err
	}
	for i, t := range ts {
		r := s.lookup(ctx, t)
		var img uuid.UUID
		if r.ok {
			img = s.image(ctx, id, r.image)
		}
		done := s.now()
		status, ready := statusOK, b1
		switch {
		case !r.ok:
			status = statusFailed
		case !done.After(b1):
		case !done.After(b2):
			ready = b2
		default:
			status = statusFailed
		}
		var (
			title, descr *string
			image        *uuid.UUID
		)
		if status == statusOK {
			title = &r.title
			if r.descr != "" {
				descr = &r.descr
			}
			if img != uuid.Nil {
				image = &img
			}
		}
		if _, err := s.d.DB.Q().Exec(ctx, `UPDATE link_previews SET status = $4, title = $5, description = $6,
			image_media_id = $10, fetched_at = $7, ready_at = $8
			WHERE post_id = $1 AND ord = $2 AND url_canon = $3 AND status = $9`,
			id, int16(i), t.u.String(), status, title, descr, done, ready, statusPending, image); err != nil { //nolint:gosec // i < MaxPerPost
			return err
		}
	}
	// An edit that landed while this job ran was deduplicated into it: build
	// the new version too.
	if m2, ok := s.d.Posts.Meta(ctx, id); ok && !m2.Deleted && m2.Version != m.Version && s.d.Jobs != nil {
		return s.d.Jobs.Enqueue(ctx, nil, JobFetch, map[string]any{"post": id.String(), "version": m2.Version},
			time.Time{}, id.String()+"/v"+strconv.Itoa(m2.Version))
	}
	return nil
}

// drop removes a post's previews.
func (s *Service) drop(ctx context.Context, id core.PostID) error {
	if s.d.DB == nil {
		return nil
	}
	_, err := s.d.DB.Q().Exec(ctx, "DELETE FROM link_previews WHERE post_id = $1", id)
	return err
}

func (s *Service) delay() time.Duration {
	if s.d.Cfg != nil && s.d.Cfg.PreviewDelay > 0 {
		return s.d.Cfg.PreviewDelay
	}
	return 30 * time.Second
}

func (s *Service) now() time.Time { return s.d.Clock.Now().UTC().Truncate(time.Microsecond) }

func (s *Service) log() *slog.Logger {
	if s.d.Log != nil {
		return s.d.Log
	}
	return slog.Default()
}
