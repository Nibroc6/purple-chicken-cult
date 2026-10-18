package media

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/blob"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/imageproc"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/scan"
)

func disabled() error { return gate.Disabled("media") }

func errTooLarge() error { return gate.NewProblem(gate.StatusUnprocessable, "media_too_large", "file") }
func errType() error     { return gate.NewProblem(gate.StatusUnprocessable, "media_type", "file") }
func errRejected() error { return gate.NewProblem(gate.StatusUnprocessable, "media_rejected", "file") }

// Upload scans, stores and returns one image of the signed-in viewer
// (POST /api/media; the route charges the media write class).
//
// Order: account hold → size → header check (format, bounds) → scan → (match:
// hold path) → decode, variants, blobs, rows. Nothing is written anywhere
// before the scan has returned a clean verdict; a scanner error or timeout is
// media_rejected with nothing stored.
func (s *Service) Upload(ctx context.Context, r io.Reader, size int64, _ /* declared type: the bytes decide */, alt string) (gate.MediaView, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return gate.MediaView{}, err
	}
	dr, err := s.live()
	if err != nil {
		return gate.MediaView{}, err
	}
	held, err := s.accountHeld(ctx, v.ID)
	if err != nil {
		return gate.MediaView{}, err
	}
	if held {
		return gate.MediaView{}, errRejected()
	}
	alt, err = cleanAlt(alt)
	if err != nil {
		return gate.MediaView{}, err
	}
	if size > MaxUploadBytes {
		return gate.MediaView{}, errTooLarge()
	}
	img, err := io.ReadAll(io.LimitReader(r, MaxUploadBytes+1))
	if err != nil {
		return gate.MediaView{}, err
	}
	if len(img) > MaxUploadBytes {
		return gate.MediaView{}, errTooLarge()
	}
	info, err := check(img)
	if err != nil {
		return gate.MediaView{}, err
	}
	verdict, err := s.scan(ctx, dr.Scanner, img, info.MIME())
	if err != nil {
		return gate.MediaView{}, errRejected()
	}
	if verdict.Match {
		if err := s.hold(ctx, dr, img, info, verdict, holdSubject{user: v.ID, source: "upload"}); err != nil {
			return gate.MediaView{}, err
		}
		return gate.MediaView{}, errRejected()
	}
	id, w, h, err := s.store(ctx, dr, img, &v.ID, uuid.Nil, alt, dr.Scanner.Name())
	if err != nil {
		return gate.MediaView{}, err
	}
	return gate.MediaView{Ref: s.d.Refs.Media(v.ID, id), W: w, H: h, Alt: alt}, nil
}

// check is the header-only bounds check (before scanning or decoding).
func check(img []byte) (imageproc.Info, error) {
	info, err := imageproc.Check(img)
	switch {
	case errors.Is(err, imageproc.ErrTooLarge):
		return info, errTooLarge()
	case err != nil:
		return info, errType()
	}
	return info, nil
}

// scan runs the scanner under ScanTimeout. Any error (timeout included) means
// the image must not be stored.
func (s *Service) scan(ctx context.Context, sc scan.Scanner, img []byte, mime string) (scan.Verdict, error) {
	ctx, cancel := context.WithTimeout(ctx, ScanTimeout)
	defer cancel()
	v, err := sc.Scan(ctx, img, mime)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil && s.d.Log != nil {
		s.d.Log.Warn("media scan failed", "feature", "media", "name", sc.Name(), "err", err)
	}
	return v, err
}

// cleanAlt trims alt text, drops control and format characters and bounds it.
func cleanAlt(alt string) (string, error) {
	if !utf8.ValidString(alt) {
		return "", gate.NewProblem(gate.StatusBadRequest, "bad_request", "alt")
	}
	alt = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return ' '
		}
		if unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, alt)
	alt = strings.TrimSpace(alt)
	if utf8.RuneCountInString(alt) > MaxAltRunes {
		return "", gate.NewProblem(gate.StatusUnprocessable, "alt_too_long", "alt")
	}
	return alt, nil
}

type variant struct {
	kind  int
	key   string
	data  []byte
	w, h  int
	edge  int
	quali int
}

// variants decodes img (EXIF orientation applied) and encodes the three WebP
// variants. Re-encoding drops every metadata block.
func (s *Service) variants(ctx context.Context, img []byte) ([]variant, int, int, error) {
	select {
	case s.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, 0, 0, ctx.Err()
	}
	defer func() { <-s.sem }()
	src, _, err := imageproc.Decode(img)
	if err != nil {
		if errors.Is(err, imageproc.ErrTooLarge) {
			return nil, 0, 0, errTooLarge()
		}
		return nil, 0, 0, errType()
	}
	b := src.Bounds()
	out := []variant{
		{kind: VariantOriginal, edge: MaxOriginalEdge, quali: 90},
		{kind: VariantDisplay, edge: DisplayEdge, quali: 82},
		{kind: VariantThumb, edge: ThumbEdge, quali: 75},
	}
	prev := src
	for i := range out {
		scaled := imageproc.Resize(prev, out[i].edge) // each from the previous (smaller) one: cheaper
		data, err := imageproc.EncodeWebP(scaled, out[i].quali)
		if err != nil {
			return nil, 0, 0, err
		}
		key, err := blob.NewKey()
		if err != nil {
			return nil, 0, 0, err
		}
		out[i].data, out[i].key = data, key
		out[i].w, out[i].h = scaled.Bounds().Dx(), scaled.Bounds().Dy()
		prev = scaled
	}
	return out, b.Dx(), b.Dy(), nil
}

// store writes the variants' blobs, then the media and variant rows. If a row
// write fails, the blobs are deleted again (best effort; the keys are random,
// so a leftover is unreachable).
func (s *Service) store(ctx context.Context, dr *Drivers, img []byte, owner *core.UserID, sourcePost uuid.UUID, alt, driver string) (uuid.UUID, int, int, error) {
	vs, w, h, err := s.variants(ctx, img)
	if err != nil {
		return uuid.Nil, 0, 0, err
	}
	written := make([]string, 0, len(vs))
	cleanup := func() {
		for _, k := range written {
			_ = dr.Blobs.Delete(context.WithoutCancel(ctx), k)
		}
	}
	for _, v := range vs {
		if err := dr.Blobs.Put(ctx, v.key, bytes.NewReader(v.data), int64(len(v.data)), "image/webp"); err != nil {
			cleanup()
			return uuid.Nil, 0, 0, err
		}
		written = append(written, v.key)
	}
	id := s.d.IDs.New()
	now := s.now()
	var ownerArg any
	if owner != nil {
		ownerArg = int64(*owner)
	}
	var altArg, srcArg any
	if alt != "" {
		altArg = alt
	}
	if sourcePost != uuid.Nil {
		srcArg = sourcePost
	}
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO media (id, owner_id, source_post, status, kind, width, height, alt, scan_driver, scanned_at, created_at)
			VALUES ($1, $2, $3, $4, 0, $5, $6, $7, $8, $9, $9)`, id, ownerArg, srcArg, statusReady, w, h, altArg, driver, now); err != nil {
			return err
		}
		for _, v := range vs {
			if _, err := tx.Exec(ctx, `INSERT INTO media_variants (media_id, variant, blob_key, mime, width, height, bytes)
				VALUES ($1, $2, $3, 'image/webp', $4, $5, $6)`, id, v.kind, v.key, v.w, v.h, len(v.data)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		cleanup()
		return uuid.Nil, 0, 0, err
	}
	return id, w, h, nil
}

// Ready answers whether the signed-in viewer may upload now: the uniform
// not-found when signed out, feature_disabled when media is off. The upload
// handler calls it before it reads the request body.
func (s *Service) Ready(ctx context.Context) error {
	if _, err := core.RequireUser(ctx); err != nil {
		return err
	}
	_, err := s.live()
	return err
}
