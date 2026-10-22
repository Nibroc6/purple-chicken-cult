// Package media is the image pipeline (ARCHITECTURE §6, §17.3; plan §4.7). It
// implements core.Media and core.CSAMHolds.
//
//   - Upload: header check (decompression bombs refused before any decode) →
//     pre-storage scan (platform/scan) → on a match: evidence store (1-year
//     preservation) + csam_holds + a priority-0 moderation item + the uploader's
//     account held (later uploads media_rejected), and nothing in the media
//     store; otherwise decode → WebP thumb 320 / display 1080 / original
//     (≤ 4096, re-encoded, no metadata) under random 128-bit keys.
//   - Ingest (preview images): the same scan and the same match path, with the
//     source URL recorded and the linking post's author held.
//   - URL: the gate checks the viewer may see the media (owns it, or can see a
//     post that carries it), then signs a per-viewer URL for ~1 h; original
//     charges media_full. Dev/test serve /m/<key> through Serve; prod uses the
//     Cloudflare Worker (deploy/cloudflare).
//   - Deletion: post deletion, account deletion and the unattached-upload sweep
//     mark media deleted; media.process deletes the blobs and purges the CDN.
//
// FAIL CLOSED: whenever cfg.MediaEnabled() is false (prod without PhotoDNA, a
// blob store, an evidence store and a media-URL key), every upload, URL and
// ingest is gate.Disabled("media"). A scanner error or timeout rejects the
// upload and writes nothing to any store.
//
// Owner: P1-W04.
package media

import (
	"context"
	"sync"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/blob"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/config"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/scan"
)

// Limits and timings.
const (
	MaxUploadBytes  = 15 << 20
	MaxAltRunes     = 1000
	URLTTL          = time.Hour
	URLStep         = 10 * time.Minute // URLs are stable within a step, so browsers can cache them
	ScanTimeout     = 15 * time.Second
	ThumbEdge       = 320
	DisplayEdge     = 1080
	MaxOriginalEdge = 4096
	Retention       = 365 * 24 * time.Hour // evidence preservation (REPORT Act 2024)
	OrphanTTL       = 2 * time.Hour        // unattached uploads are deleted after this
	ProcessEvery    = 10 * time.Minute
	// JobProcess is the deletion job: marks deleted posts' and accounts' media,
	// sweeps unattached uploads, deletes blobs, purges the CDN, and deletes
	// evidence past its retention.
	JobProcess = "media.process"
	// workers bounds concurrent decode/encode work (CPU and memory).
	workers = 2
)

// media.status values (2003_media.sql).
const (
	statusReceived = 0
	statusReady    = 1
	statusRejected = 2
	statusHeld     = 3
)

// Variants (media_variants.variant).
const (
	VariantThumb    = 0
	VariantDisplay  = 1
	VariantOriginal = 2
)

var variantNames = map[string]int{"thumb": VariantThumb, "display": VariantDisplay, "original": VariantOriginal}

// Drivers are the external services behind the pipeline.
type Drivers struct {
	Scanner  scan.Scanner
	Blobs    blob.Store
	Evidence blob.Store
	Purger   blob.Purger
}

// Service is the media module.
type Service struct {
	d   *core.Deps
	sem chan struct{}

	once sync.Once
	mu   sync.Mutex
	dr   *Drivers
	derr error
}

var (
	_ core.Media     = (*Service)(nil)
	_ core.CSAMHolds = (*Service)(nil)
)

// New returns the service.
func New(d *core.Deps) *Service { return &Service{d: d, sem: make(chan struct{}, workers)} }

// UseDrivers replaces the configured drivers (tests; wiring keeps the
// configured ones). Media still needs cfg.MediaEnabled().
func (s *Service) UseDrivers(dr Drivers) {
	s.once.Do(func() {})
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dr, s.derr = &dr, nil
}

// Init registers the deletion job and clean-ups and logs whether media is
// enabled (fail closed). The stores are opened lazily on first use, so a
// process that never handles media touches no directory or bucket.
func (s *Service) Init(context.Context) error {
	if s.d.Bus != nil {
		core.Subscribe(s.d.Bus, func(e core.PostDeleted) { s.enqueue(process{Post: e.ID.String()}) })
		core.Subscribe(s.d.Bus, func(e core.AccountDeleted) { s.enqueue(process{Owner: int64(e.ID)}) })
	}
	if s.d.Jobs != nil {
		s.d.Jobs.Handle(JobProcess, s.runProcess)
		s.d.Jobs.Every(JobProcess, ProcessEvery)
	}
	if s.d.Log == nil || s.d.Cfg == nil {
		return nil
	}
	if !s.d.Cfg.MediaEnabled() {
		if s.d.Cfg.Prod() {
			s.d.Log.Error("media disabled", "feature", "media") // PCC_SCANNER=dev|none, or a missing store, in prod (D20)
		} else {
			s.d.Log.Warn("media disabled", "feature", "media")
		}
		return nil
	}
	if s.d.Cfg.UnscannedMedia() {
		s.d.Log.Warn("media enabled without a CSAM scanner (PCC_SCANNER=unscanned)", "feature", "media")
	}
	if s.d.Cfg.Prod() && s.d.Cfg.Blob.Driver == config.BlobS3 && s.d.Cfg.CDN.Purge == config.CDNPurgeNone {
		s.d.Log.Warn("media CDN purge not configured", "feature", "media")
	}
	return nil
}

// Enabled reports cfg.MediaEnabled().
func (s *Service) Enabled() bool { return s.d.Cfg != nil && s.d.Cfg.MediaEnabled() }

// drivers returns the drivers, building them from the configuration once.
func (s *Service) drivers() (*Drivers, error) {
	s.once.Do(func() {
		dr, err := s.build()
		s.mu.Lock()
		s.dr, s.derr = dr, err
		s.mu.Unlock()
		if err != nil && s.d.Log != nil {
			s.d.Log.Error("media drivers", "feature", "media", "err", err)
		}
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dr, s.derr
}

func (s *Service) build() (*Drivers, error) {
	c := s.d.Cfg
	sc, err := scan.New(c)
	if err != nil {
		return nil, err
	}
	bs, err := blob.New(c, c.Blob, s.d.Log)
	if err != nil {
		return nil, err
	}
	ev, err := blob.New(c, c.EvidenceBlob, s.d.Log)
	if err != nil {
		return nil, err
	}
	return &Drivers{Scanner: sc, Blobs: bs, Evidence: ev, Purger: blob.NewPurger(c)}, nil
}

// live returns the drivers when media is enabled, else gate.Disabled("media").
func (s *Service) live() (*Drivers, error) {
	if !s.Enabled() || s.d.DB == nil {
		return nil, disabled()
	}
	dr, err := s.drivers()
	if err != nil {
		return nil, disabled()
	}
	return dr, nil
}

func (s *Service) now() time.Time { return s.d.Clock.Now().UTC() }

func (s *Service) signer() *blob.Signer { return blob.NewSigner(s.d.Keys.MediaURL) }
