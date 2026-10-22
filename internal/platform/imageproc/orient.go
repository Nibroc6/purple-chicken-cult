package imageproc

import (
	"encoding/binary"
	"image"
	"image/draw"
)

// jpegOrientation returns the EXIF Orientation (1..8) of a JPEG, 1 when absent
// or unreadable. It walks the markers up to the first scan and reads only the
// IFD0 Orientation tag of an APP1 "Exif" segment; nothing else is parsed.
//
//nolint:gosec // G602 false positives: every index is bounds-checked first.
func jpegOrientation(b []byte) int {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return 1
	}
	for i := 2; i+4 <= len(b); {
		hdr := b[i : i+4]
		if hdr[0] != 0xFF {
			return 1
		}
		marker := hdr[1]
		if marker == 0xD8 || (marker >= 0xD0 && marker <= 0xD7) || marker == 0x01 || marker == 0xFF {
			i++
			continue
		}
		if marker == 0xDA || marker == 0xD9 { // start of scan / end of image
			return 1
		}
		n := int(binary.BigEndian.Uint16(hdr[2:4]))
		if n < 2 || i+2+n > len(b) {
			return 1
		}
		seg := b[i+4 : i+2+n]
		if marker == 0xE1 && len(seg) > 6 && string(seg[:6]) == "Exif\x00\x00" {
			if o := tiffOrientation(seg[6:]); o != 0 {
				return o
			}
		}
		i += 2 + n
	}
	return 1
}

// tiffOrientation reads the IFD0 Orientation of a TIFF header (0 = none).
//
//nolint:gosec // G602 false positives: every slice is bounds-checked first.
func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 0
	}
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 0
	}
	head := t[:8]
	off := int(bo.Uint32(head[4:8]))
	if off < 8 || off+2 > len(t) {
		return 0
	}
	ifd := t[off:]
	count := int(bo.Uint16(ifd[:2]))
	for k := range count {
		e := 2 + 12*k
		if e+12 > len(ifd) {
			return 0
		}
		ent := ifd[e : e+12]
		if bo.Uint16(ent[0:2]) == 0x0112 && bo.Uint16(ent[2:4]) == 3 { // Orientation, SHORT
			if v := int(bo.Uint16(ent[8:10])); v >= 1 && v <= 8 {
				return v
			}
			return 0
		}
	}
	return 0
}

// Orient returns img transformed for EXIF orientation o (1 = as is).
func Orient(img image.Image, o int) image.Image {
	if o <= 1 || o > 8 {
		return img
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if o >= 5 {
		w, h = h, w
	}
	src := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(src, src.Bounds(), img, b.Min, draw.Src)
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	sw, sh := b.Dx(), b.Dy()
	for y := range sh {
		for x := range sw {
			var dx, dy int
			switch o {
			case 2: // mirror horizontal
				dx, dy = sw-1-x, y
			case 3: // rotate 180
				dx, dy = sw-1-x, sh-1-y
			case 4: // mirror vertical
				dx, dy = x, sh-1-y
			case 5: // transpose
				dx, dy = y, x
			case 6: // rotate 90 CW
				dx, dy = sh-1-y, x
			case 7: // transverse
				dx, dy = sh-1-y, sw-1-x
			case 8: // rotate 90 CCW
				dx, dy = y, sw-1-x
			}
			si := src.PixOffset(x, y)
			di := dst.PixOffset(dx, dy)
			copy(dst.Pix[di:di+4], src.Pix[si:si+4])
		}
	}
	return dst
}
