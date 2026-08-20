package imageproc

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/jpeg"
)

// FixOrientation reads a JPEG's EXIF orientation tag and, if it says the
// pixels need rotating/flipping to display upright, bakes that transform
// into the pixels and re-encodes without the tag. This matters because
// most DLNA renderers (TVs, media players) display the raw pixel grid and
// ignore EXIF orientation entirely - so a portrait photo whose sensor
// recorded it "sideways" with a rotate-90 tag (the common case for phone
// photos) shows up sideways on the TV even though apps that honor EXIF
// display it correctly.
//
// Only JPEG is supported (the format that carries EXIF here); anything
// else, or a JPEG with no rotation needed, passes through unchanged.
// Errors fall back to passthrough - orientation correction is a
// nice-to-have, never a reason to fail serving the photo.
func FixOrientation(data []byte) (out []byte, changed bool, err error) {
	o := jpegOrientation(data)
	if o <= 1 || o > 8 {
		return data, false, nil
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return data, false, nil //nolint:nilerr // best-effort: undecodable input passes through
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, applyOrientation(img, o), &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, false, fmt.Errorf("imageproc: jpeg encode failed: %w", err)
	}
	return buf.Bytes(), true, nil
}

// jpegOrientation scans a JPEG's APP1/Exif segment for the orientation tag
// (0x0112) and returns its value (1-8), or 1 if there's no Exif segment,
// no orientation tag, or the data isn't parseable JPEG/Exif.
func jpegOrientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 1
	}
	for p := 2; p+4 <= len(data); {
		if data[p] != 0xFF {
			return 1
		}
		marker := data[p+1]
		if marker == 0xD8 || marker == 0xD9 || (marker >= 0xD0 && marker <= 0xD7) {
			p += 2 // SOI/EOI/RSTn: no length field
			continue
		}
		if marker == 0xDA {
			return 1 // SOS: compressed scan data follows, nothing more to scan
		}
		segLen := int(binary.BigEndian.Uint16(data[p+2 : p+4]))
		if segLen < 2 || p+2+segLen > len(data) {
			return 1
		}
		segData := data[p+4 : p+2+segLen]
		if marker == 0xE1 && bytes.HasPrefix(segData, []byte("Exif\x00\x00")) {
			if o := parseExifOrientation(segData[6:]); o > 0 {
				return o
			}
			return 1
		}
		p += 2 + segLen
	}
	return 1
}

// parseExifOrientation reads the orientation tag (0x0112) out of a raw
// TIFF/Exif block (starting at the "II"/"MM" byte-order marker). Returns 0
// if the block is malformed or has no orientation tag.
func parseExifOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 0
	}
	var bo binary.ByteOrder
	switch string(tiff[0:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 0
	}
	p := int(bo.Uint32(tiff[4:8]))
	if p < 0 || p+2 > len(tiff) {
		return 0
	}
	numEntries := int(bo.Uint16(tiff[p : p+2]))
	p += 2
	for i := 0; i < numEntries; i++ {
		if p+12 > len(tiff) {
			return 0
		}
		if bo.Uint16(tiff[p:p+2]) == 0x0112 {
			return int(bo.Uint16(tiff[p+8 : p+10]))
		}
		p += 12
	}
	return 0
}

// applyOrientation returns a copy of img with the given EXIF orientation
// (2-8) baked into the pixels so it displays upright with the tag
// effectively reset to 1 (normal).
func applyOrientation(img image.Image, o int) *image.NRGBA {
	switch o {
	case 2:
		return flipH(img)
	case 3:
		return rotate180(img)
	case 4:
		return flipV(img)
	case 5:
		return transpose(img)
	case 6:
		return rotate90CW(img)
	case 7:
		return transverse(img)
	case 8:
		return rotate90CCW(img)
	default:
		return toNRGBA(img) // unreachable in practice: FixOrientation only calls this for o in [2,8]
	}
}

func toNRGBA(img image.Image) *image.NRGBA {
	b := img.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			dst.Set(x, y, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

func flipH(img image.Image) *image.NRGBA {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(w-1-x, y, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

func flipV(img image.Image) *image.NRGBA {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(x, h-1-y, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

func rotate180(img image.Image) *image.NRGBA {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(w-1-x, h-1-y, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

// rotate90CW rotates the image 90 degrees clockwise (width/height swap).
func rotate90CW(img image.Image) *image.NRGBA {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewNRGBA(image.Rect(0, 0, h, w))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(h-1-y, x, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

// rotate90CCW rotates the image 90 degrees counter-clockwise (width/height
// swap).
func rotate90CCW(img image.Image) *image.NRGBA {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewNRGBA(image.Rect(0, 0, h, w))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(y, w-1-x, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

// transpose mirrors across the top-left/bottom-right diagonal (width/height
// swap).
func transpose(img image.Image) *image.NRGBA {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewNRGBA(image.Rect(0, 0, h, w))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(y, x, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

// transverse mirrors across the top-right/bottom-left diagonal (width/height
// swap).
func transverse(img image.Image) *image.NRGBA {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewNRGBA(image.Rect(0, 0, h, w))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(h-1-y, w-1-x, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}
