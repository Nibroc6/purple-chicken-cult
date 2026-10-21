package previews

import (
	"context"
	"net/url"

	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/safefetch"
)

// MaxImageBytes caps a preview image (decoded bytes; anything larger is
// refused, never truncated).
const MaxImageBytes = 5 << 20

// ImageFetcher fetches a preview image (safefetch.Client; tests substitute
// their own). It applies the same SSRF policy as GetHTML and returns raster
// image types only.
type ImageFetcher interface {
	GetImage(ctx context.Context, u *url.URL, max int64) (*safefetch.Response, error)
}

// image fetches a page's og:image for post id and ingests it through the media
// pipeline (the same CSAM scan as an upload). It returns the media id of a
// clean image, or uuid.Nil: media disabled, no fetcher, refused by the per-host
// limiter, a fetch failure, or anything Media.Ingest refuses. A CSAM match is
// held and queued inside Ingest (it answers media_rejected), so the preview
// simply shows no image; nothing is dropped silently. Viewers never fetch the
// image: they get signed media URLs for the stored copy.
func (s *Service) image(ctx context.Context, id core.PostID, raw string) uuid.UUID {
	if raw == "" || s.d.Media == nil || s.d.Cfg == nil || !s.d.Cfg.MediaEnabled() {
		return uuid.Nil
	}
	f, ok := s.fetch.(ImageFetcher)
	if !ok {
		return uuid.Nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return uuid.Nil
	}
	if !s.limit.allow(hostKey(u.Hostname()), s.now()) {
		return uuid.Nil
	}
	res, err := f.GetImage(ctx, u, MaxImageBytes)
	if err != nil {
		return uuid.Nil
	}
	src := raw
	if res.FinalURL != nil {
		src = res.FinalURL.String()
	}
	mid, err := s.d.Media.Ingest(ctx, res.Body, id, src)
	if err != nil {
		s.log().Info("previews: image not ingested", "err", err.Error())
		return uuid.Nil
	}
	return mid
}
