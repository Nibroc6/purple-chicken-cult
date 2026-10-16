// Package media is the image pipeline (ARCHITECTURE §6, §17.3): scan BEFORE any
// storage write (platform/scan), evidence preservation on a match, WebP variants
// (platform/imageproc), random blob keys (platform/blob) and short-lived signed
// per-viewer URLs. It implements core.Media.
//
// FAIL CLOSED: whenever cfg.MediaEnabled() is false (prod without PhotoDNA, a blob
// store and an evidence store), every upload is gate.Disabled("media") and
// preview images are skipped.
//
// Owner: P1-W04. Scaffold stub: fail-closed checks, then gate.ErrNotImplemented.
package media

import (
	"context"
	"io"
	"time"

	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coreref"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// Limits.
const (
	MaxUploadBytes = 15 << 20
	URLTTL         = time.Hour
)

// Service is the media module.
type Service struct {
	d   *core.Deps
	ref *coreref.MemMedia // without D: the fail-closed placeholder (no media id reaches the database)
}

var _ core.Media = (*Service)(nil)

// New returns the service.
func New(d *core.Deps) *Service { return &Service{d: d, ref: &coreref.MemMedia{}} }

// Init logs whether media is enabled (fail closed).
func (s *Service) Init(context.Context) error {
	if s.d.Cfg != nil && !s.d.Cfg.MediaEnabled() && s.d.Log != nil {
		s.d.Log.Warn("media disabled", "feature", "media")
	}
	return nil
}

// Enabled reports cfg.MediaEnabled().
func (s *Service) Enabled() bool { return s.d.Cfg != nil && s.d.Cfg.MediaEnabled() }

// Upload scans, stores and returns a media item.
func (s *Service) Upload(ctx context.Context, r io.Reader, size int64, mime, alt string) (gate.MediaView, error) {
	if _, err := core.RequireUser(ctx); err != nil {
		return gate.MediaView{}, err
	}
	if !s.Enabled() {
		return gate.MediaView{}, gate.Disabled("media")
	}
	return gate.MediaView{}, gate.ErrNotImplemented
}

// URL returns a signed per-viewer URL for a variant ("thumb" | "display" | "original").
func (s *Service) URL(ctx context.Context, m gate.Ref, variant string) (string, time.Time, error) {
	if _, err := core.RequireUser(ctx); err != nil {
		return "", time.Time{}, err
	}
	if !s.Enabled() {
		return "", time.Time{}, gate.Disabled("media")
	}
	return "", time.Time{}, gate.ErrNotImplemented
}

// ---- core.Media ----

// Attach validates owned, ready media for a post.
func (s *Service) Attach(ctx context.Context, tx store.Q, owner core.UserID, refs []gate.Ref) ([]uuid.UUID, error) {
	return s.ref.Attach(ctx, tx, owner, refs)
}

// Views renders media views for a viewer.
func (s *Service) Views(ctx context.Context, sr core.Search, ids []uuid.UUID) ([]gate.MediaView, error) {
	return s.ref.Views(ctx, sr, ids)
}

// Ingest scans and stores a preview image.
func (s *Service) Ingest(ctx context.Context, img []byte, sourcePost core.PostID, sourceURL string) (uuid.UUID, error) {
	return s.ref.Ingest(ctx, img, sourcePost, sourceURL)
}
