package media

import (
	"context"

	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// Ingest scans and stores a preview image fetched for sourcePost's link
// (P1-W11's preview job; no viewer). It takes the SAME path as an upload
// (security #7): a match is preserved as evidence, held with the source URL,
// queued at priority 0 and holds the linking post's author; it is never
// dropped silently. A held or failed image is Problem media_rejected, so the
// preview shows no image; the media id of a clean image goes to
// link_previews.image_media_id.
func (s *Service) Ingest(ctx context.Context, img []byte, sourcePost core.PostID, sourceURL string) (uuid.UUID, error) {
	dr, err := s.live()
	if err != nil {
		return uuid.Nil, err
	}
	if len(img) > MaxUploadBytes {
		return uuid.Nil, errTooLarge()
	}
	info, err := check(img)
	if err != nil {
		return uuid.Nil, err
	}
	verdict, err := s.scan(ctx, dr.Scanner, img, info.MIME())
	if err != nil {
		return uuid.Nil, errRejected()
	}
	if verdict.Match {
		sub := holdSubject{post: sourcePost, sourceURL: sourceURL, source: "preview"}
		if s.d.Posts != nil {
			if m, ok := s.d.Posts.Meta(ctx, sourcePost); ok {
				sub.user = m.Author
			}
		}
		if err := s.hold(ctx, dr, img, info, verdict, sub); err != nil {
			return uuid.Nil, err
		}
		return uuid.Nil, errRejected()
	}
	id, _, _, err := s.store(ctx, dr, img, nil, sourcePost, "", dr.Scanner.Name())
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}
