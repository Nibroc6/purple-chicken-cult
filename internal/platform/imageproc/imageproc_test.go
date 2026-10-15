package imageproc

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func pngOf(w, h int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.NRGBA{R: 255, A: 255})
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func TestDecodeResize(t *testing.T) {
	img, f, err := Decode(pngOf(64, 32))
	if err != nil || f != "png" {
		t.Fatalf("decode: %v %q", err, f)
	}
	r := Resize(img, 16)
	if r.Bounds().Dx() != 16 || r.Bounds().Dy() != 8 {
		t.Fatalf("resize: %v", r.Bounds())
	}
}

// TestBombRefusedBeforeDecode builds a PNG header claiming 50,000×50,000 pixels.
func TestBombRefusedBeforeDecode(t *testing.T) {
	b := pngOf(1, 1)
	// IHDR width/height live at byte offsets 16..23; the CRC no longer matches,
	// but DecodeConfig reads the dimensions before checking it.
	b[16], b[17], b[18], b[19] = 0, 0, 0xC3, 0x50
	b[20], b[21], b[22], b[23] = 0, 0, 0xC3, 0x50
	if _, _, err := Decode(b); !errors.Is(err, ErrTooLarge) && !errors.Is(err, ErrFormat) {
		t.Fatalf("bomb header must be refused: %v", err)
	}
}

func TestGarbage(t *testing.T) {
	if _, _, err := Decode([]byte("not an image")); !errors.Is(err, ErrFormat) {
		t.Fatal(err)
	}
}
