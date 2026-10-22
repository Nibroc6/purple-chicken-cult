// Package imageproc decodes, bounds-checks, resizes and re-encodes images
// (ARCHITECTURE §6.2). DecodeConfig runs first and refuses decompression bombs
// before any pixel buffer is allocated. Re-encoding strips all metadata. WebP is
// the shipped format (D14); AVIF sits behind a flag.
//
// Owner: P1-W04.
package imageproc

import (
	"bytes"
	"errors"
	"image"
	_ "image/gif" // register decoders
	_ "image/jpeg"
	_ "image/png"

	"github.com/gen2brain/avif"
	"github.com/gen2brain/webp"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// Format is a decoded source format.
type Format string

// Limits (decompression-bomb guard).
const (
	MaxPixels = 40_000_000
	MaxSide   = 12_000
)

// Errors.
var (
	ErrTooLarge = errors.New("imageproc: image too large")
	ErrFormat   = errors.New("imageproc: unsupported format")
)

// Info is what the header says about an image, read without decoding pixels.
type Info struct {
	Format Format
	Width  int
	Height int
}

// MIME returns the source media type.
func (i Info) MIME() string { return "image/" + string(i.Format) }

// Check reads only the header (image.DecodeConfig) and refuses unsupported
// formats (ErrFormat) and anything over MaxSide per side or MaxPixels in total
// (ErrTooLarge) before a single pixel buffer is allocated. Callers run it
// before scanning or decoding anything.
func Check(b []byte) (Info, error) {
	cfg, f, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return Info{}, ErrFormat
	}
	switch f {
	case "jpeg", "png", "webp", "gif":
	default:
		return Info{}, ErrFormat
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > MaxSide || cfg.Height > MaxSide ||
		int64(cfg.Width)*int64(cfg.Height) > MaxPixels {
		return Info{}, ErrTooLarge
	}
	return Info{Format: Format(f), Width: cfg.Width, Height: cfg.Height}, nil
}

// Decode decodes jpeg/png/webp/gif (first frame) after Check, and applies the
// JPEG EXIF orientation so the pixels are upright: the re-encoded variants
// carry no metadata at all, orientation included.
func Decode(b []byte) (image.Image, Format, error) {
	info, err := Check(b)
	if err != nil {
		return nil, "", err
	}
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, "", ErrFormat
	}
	if info.Format == "jpeg" {
		img = Orient(img, jpegOrientation(b))
	}
	return img, info.Format, nil
}

// Resize scales img so its longer edge is at most maxEdge (never upscales).
func Resize(img image.Image, maxEdge int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= maxEdge && h <= maxEdge {
		return img
	}
	if w >= h {
		h = h * maxEdge / w
		w = maxEdge
	} else {
		w = w * maxEdge / h
		h = maxEdge
	}
	dst := image.NewNRGBA(image.Rect(0, 0, max(w, 1), max(h, 1)))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
	return dst
}

// EncodeWebP encodes img as lossy WebP at quality q (no metadata).
func EncodeWebP(img image.Image, q int) ([]byte, error) {
	var buf bytes.Buffer
	if err := webp.Encode(&buf, img, webp.Options{Quality: q}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// EncodeAVIF encodes img as AVIF at quality q (behind a flag; slow in pure Go).
func EncodeAVIF(img image.Image, q int) ([]byte, error) {
	var buf bytes.Buffer
	if err := avif.Encode(&buf, img, avif.Options{Quality: q, Speed: 8}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// WebPChunks lists the RIFF chunk FourCCs of a WebP file (nil if it is not
// one). Variants must carry image chunks only, never EXIF or XMP.
func WebPChunks(b []byte) []string {
	if len(b) < 12 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WEBP" {
		return nil
	}
	var out []string
	for i := 12; i+8 <= len(b); {
		out = append(out, string(b[i:i+4]))
		n := int(uint32(b[i+4]) | uint32(b[i+5])<<8 | uint32(b[i+6])<<16 | uint32(b[i+7])<<24)
		i += 8 + n + n&1
	}
	return out
}
