package media

import (
	"context"
	"errors"
	"io"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/blob"
)

// URL returns a signed per-viewer URL for one variant ("thumb" | "display" |
// "original") of media m and its expiry (GET /api/media/{ref}/url).
//
// The original charges the media_full budget before the ref is resolved (so a
// valid ref and garbage cost the same). The viewer must own the media or be
// able to see a post that carries it (an attachment in post_media, or the post
// whose link preview produced it), checked through core.Visibility now, not
// when the ref was minted: after an unfriend or a deletion the next URL
// request is the uniform not-found.
func (s *Service) URL(ctx context.Context, m gate.Ref, variant string) (string, time.Time, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return "", time.Time{}, err
	}
	if _, err := s.live(); err != nil {
		return "", time.Time{}, err
	}
	kind, ok := variantNames[variant]
	if !ok {
		return "", time.Time{}, gate.NewProblem(gate.StatusBadRequest, "bad_request", "variant")
	}
	if kind == VariantOriginal && s.d.Limits != nil {
		if err := s.d.Limits.ChargeRead(ctx, v, "media_full"); err != nil {
			return "", time.Time{}, err
		}
	}
	id, ok := s.d.Refs.OpenMedia(v.ID, m)
	if !ok {
		return "", time.Time{}, gate.ErrNotFound
	}
	var (
		owner *int64
		src   *uuid.UUID
		key   string
	)
	err = s.d.DB.Q().QueryRow(ctx, `SELECT m.owner_id, m.source_post, mv.blob_key FROM media m
		JOIN media_variants mv ON mv.media_id = m.id AND mv.variant = $2
		WHERE m.id = $1 AND m.status = $3 AND m.deleted_at IS NULL`, id, kind, statusReady).Scan(&owner, &src, &key)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", time.Time{}, gate.ErrNotFound
	}
	if err != nil {
		return "", time.Time{}, err
	}
	if owner == nil || core.UserID(*owner) != v.ID { //nolint:gosec // user ids < 2^32 (DB CHECK)
		visible, err := s.visibleToViewer(ctx, v.ID, id, src)
		if err != nil {
			return "", time.Time{}, err
		}
		if !visible {
			return "", time.Time{}, gate.ErrNotFound
		}
	}
	exp := s.now().Add(URLTTL).Truncate(URLStep)
	sg := s.signer()
	return sg.URL(s.d.Cfg.MediaBaseURL, key, exp, sg.ViewerTag(uint64(v.ID))), exp, nil
}

// visibleToViewer reports whether viewer can see a post that carries media id.
func (s *Service) visibleToViewer(ctx context.Context, viewer core.UserID, id uuid.UUID, src *uuid.UUID) (bool, error) {
	var posts []uuid.UUID
	if src != nil {
		posts = append(posts, *src)
	}
	rows, err := s.d.DB.Q().Query(ctx, `SELECT post_id FROM post_media WHERE media_id = $1`, id)
	if err != nil {
		return false, err
	}
	for rows.Next() {
		var p uuid.UUID
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return false, err
		}
		posts = append(posts, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}
	if len(posts) == 0 || s.d.Vis == nil || s.d.Posts == nil {
		return false, nil
	}
	sr, err := s.d.Vis.SearchFor(ctx, viewer)
	if err != nil {
		return false, err
	}
	for _, p := range posts {
		meta, ok := s.d.Posts.Meta(ctx, p)
		if !ok {
			continue
		}
		if _, ok := s.d.Vis.CheckPost(ctx, sr, meta); ok {
			return true, nil
		}
	}
	return false, nil
}

// Serve errors (the dev/test media origin answers 403 for ErrForbidden and
// 404 for gate.ErrNotFound).
var ErrForbidden = errors.New("media: forbidden")

// Blob is one media variant opened for the dev/test media origin.
type Blob struct {
	Body   io.ReadCloser
	Size   int64
	MIME   string
	MaxAge int // seconds until the URL expires (≤ 1 h)
}

// Serve verifies a signed media URL (key and its query) like the Cloudflare
// Worker does, then opens the variant: dev/test only (prod serves media from
// the CDN). An unsigned, tampered, other-viewer or expired URL is ErrForbidden
// before anything is looked up; a valid signature for deleted or unknown media
// is gate.ErrNotFound.
func (s *Service) Serve(ctx context.Context, key string, q url.Values) (*Blob, error) {
	if s.d.Cfg == nil || !s.d.Cfg.DevLike() {
		return nil, gate.ErrNotFound
	}
	dr, err := s.live()
	if err != nil {
		return nil, gate.ErrNotFound
	}
	now := s.now()
	exp, err := s.signer().Verify(key, q, now)
	if err != nil {
		return nil, ErrForbidden
	}
	var mime string
	err = s.d.DB.Q().QueryRow(ctx, `SELECT mv.mime FROM media_variants mv JOIN media m ON m.id = mv.media_id
		WHERE mv.blob_key = $1 AND m.status = $2 AND m.deleted_at IS NULL`, key, statusReady).Scan(&mime)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, gate.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	body, size, _, err := dr.Blobs.Get(ctx, key)
	if errors.Is(err, blob.ErrNotFound) {
		return nil, gate.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &Blob{Body: body, Size: size, MIME: mime, MaxAge: int(exp.Sub(now) / time.Second)}, nil
}
