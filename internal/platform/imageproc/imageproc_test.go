package imageproc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/color/palette"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"
)

// Every image here is generated in code.

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
	if Resize(img, 100) != img {
		t.Fatal("resize must never upscale")
	}
	tall := Resize(image.NewNRGBA(image.Rect(0, 0, 10, 40)), 20)
	if tall.Bounds().Dx() != 5 || tall.Bounds().Dy() != 20 {
		t.Fatalf("tall: %v", tall.Bounds())
	}
}

// bombPNG is a valid PNG header (correct IHDR CRC) claiming w×h pixels, with
// no pixel data behind it.
func bombPNG(w, h uint32) []byte {
	b := pngOf(1, 1)
	binary.BigEndian.PutUint32(b[16:20], w)
	binary.BigEndian.PutUint32(b[20:24], h)
	binary.BigEndian.PutUint32(b[29:33], crc32.ChecksumIEEE(b[12:29]))
	return b
}

// TestBombRefusedBeforeDecode: a 50,000×50,000 PNG header is refused by the
// header check alone (a decode would need 10 GB).
func TestBombRefusedBeforeDecode(t *testing.T) {
	b := bombPNG(50_000, 50_000)
	if _, err := Check(b); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("check: %v", err)
	}
	if _, _, err := Decode(b); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("decode: %v", err)
	}
	for _, c := range [][2]uint32{{MaxSide + 1, 10}, {10, MaxSide + 1}, {8000, 6000}} {
		if _, err := Check(bombPNG(c[0], c[1])); !errors.Is(err, ErrTooLarge) {
			t.Errorf("%v: %v", c, err)
		}
	}
	if i, err := Check(bombPNG(6000, 6000)); err != nil || i.Width != 6000 || i.MIME() != "image/png" {
		t.Fatalf("36 MP header must pass the check: %v %+v", err, i)
	}
}

func TestGarbage(t *testing.T) {
	for _, b := range [][]byte{[]byte("not an image"), nil, []byte("GIF89a")} {
		if _, _, err := Decode(b); !errors.Is(err, ErrFormat) {
			t.Fatal(err)
		}
	}
	// A BMP-like header is not an accepted format.
	if _, err := Check([]byte("BM\x00\x00")); !errors.Is(err, ErrFormat) {
		t.Fatal(err)
	}
}

func TestGIFFirstFrame(t *testing.T) {
	g := &gif.GIF{}
	for i := range 3 {
		fr := image.NewPaletted(image.Rect(0, 0, 12, 7), palette.Plan9)
		fr.SetColorIndex(0, 0, uint8(i))
		g.Image = append(g.Image, fr)
		g.Delay = append(g.Delay, 10)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		t.Fatal(err)
	}
	img, f, err := Decode(buf.Bytes())
	if err != nil || f != "gif" || img.Bounds().Dx() != 12 {
		t.Fatalf("gif: %v %q", err, f)
	}
}

// ---- EXIF: orientation honoured, metadata gone ----

// gpsMarker is a value only the test's GPS IFD contains.
var gpsMarker = []byte{0x13, 0x57, 0x9B, 0xDF, 0x24, 0x68, 0xAC, 0xE0}

// exifJPEG is a JPEG with an APP1 Exif segment: IFD0 Orientation = o and a GPS
// IFD whose GPSLatitude rational holds gpsMarker.
func exifJPEG(t *testing.T, w, h int, o uint16) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for x := range w {
		img.Set(x, 0, color.NRGBA{R: 250, A: 255}) // red top row
	}
	var jb bytes.Buffer
	if err := jpeg.Encode(&jb, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	le := binary.LittleEndian
	var tiff []byte
	tiff = append(tiff, 'I', 'I', 42, 0, 8, 0, 0, 0)
	// IFD0 at 8: 2 entries.
	ifd0 := make([]byte, 2+2*12+4)
	le.PutUint16(ifd0[0:], 2)
	le.PutUint16(ifd0[2:], 0x0112) // Orientation
	le.PutUint16(ifd0[4:], 3)
	le.PutUint32(ifd0[6:], 1)
	le.PutUint16(ifd0[10:], o)
	gpsOff := 8 + len(ifd0)
	le.PutUint16(ifd0[14:], 0x8825) // GPS IFD pointer
	le.PutUint16(ifd0[16:], 4)
	le.PutUint32(ifd0[18:], 1)
	le.PutUint32(ifd0[22:], uint32(gpsOff)) //nolint:gosec // small test offset
	tiff = append(tiff, ifd0...)
	gps := make([]byte, 2+12+4)
	le.PutUint16(gps[0:], 1)
	le.PutUint16(gps[2:], 0x0002) // GPSLatitude, RATIONAL ×1
	le.PutUint16(gps[4:], 5)
	le.PutUint32(gps[6:], 1)
	le.PutUint32(gps[10:], uint32(gpsOff+len(gps))) //nolint:gosec // small test offset
	tiff = append(tiff, gps...)
	tiff = append(tiff, gpsMarker...)
	app1 := append([]byte("Exif\x00\x00"), tiff...)
	seg := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(app1)+2)) //nolint:gosec // small segment
	seg = append(seg, app1...)
	j := jb.Bytes()
	out := append([]byte{}, j[:2]...)
	out = append(out, seg...)
	return append(out, j[2:]...)
}

func TestEXIFOrientationApplied(t *testing.T) {
	b := exifJPEG(t, 40, 20, 6)
	if !bytes.Contains(b, gpsMarker) || jpegOrientation(b) != 6 {
		t.Fatal("test JPEG must carry its EXIF")
	}
	img, _, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 20 || img.Bounds().Dy() != 40 {
		t.Fatalf("orientation 6 must rotate 40×20 to 20×40: %v", img.Bounds())
	}
	// Rotated 90° clockwise: the red top row is now the right-hand column.
	if r, g, _, _ := img.At(19, 20).RGBA(); r>>8 < 140 || g>>8 > 80 {
		t.Fatalf("right column not red: %v", img.At(19, 20))
	}
	if r, _, _, _ := img.At(0, 20).RGBA(); r>>8 > 60 {
		t.Fatalf("left column red: %v", img.At(0, 20))
	}
	for o := 1; o <= 8; o++ {
		got := Orient(image.NewNRGBA(image.Rect(0, 0, 4, 2)), o).Bounds()
		want := image.Rect(0, 0, 4, 2)
		if o >= 5 {
			want = image.Rect(0, 0, 2, 4)
		}
		if got != want {
			t.Errorf("orient %d: %v", o, got)
		}
	}
	if jpegOrientation(pngOf(2, 2)) != 1 || jpegOrientation([]byte{0xFF, 0xD8, 0xFF}) != 1 {
		t.Fatal("non-EXIF input is orientation 1")
	}
}

// TestReencodeStripsMetadata: EXIF (GPS included) is absent from every WebP
// variant, which carries only VP8/VP8L/VP8X/ALPH chunks.
func TestReencodeStripsMetadata(t *testing.T) {
	img, _, err := Decode(exifJPEG(t, 64, 48, 1))
	if err != nil {
		t.Fatal(err)
	}
	for _, edge := range []int{16, 32, 64} {
		w, err := EncodeWebP(Resize(img, edge), 80)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(w, gpsMarker) || bytes.Contains(w, []byte("Exif")) {
			t.Fatalf("edge %d: metadata survived", edge)
		}
		for _, c := range WebPChunks(w) {
			switch c {
			case "VP8 ", "VP8L", "VP8X", "ALPH":
			default:
				t.Fatalf("edge %d: unexpected chunk %q", edge, c)
			}
		}
		if i, err := Check(w); err != nil || i.Format != "webp" {
			t.Fatalf("variant must decode as webp: %v", err)
		}
	}
}
