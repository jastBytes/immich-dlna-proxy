// Package imageproc downscales photos that exceed a configured maximum
// resolution, using only the Go standard library (no external
// dependencies). It's intentionally simple: a box filter for downsampling,
// applied only when the source image is larger than the target bounds.
package imageproc

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/gif" // registers GIF decoding with image.Decode / image.DecodeConfig
	"image/jpeg"
	"image/png"
)

// jpegQuality is used when re-encoding downscaled JPEGs. 85 is a common
// "visually lossless enough" default that keeps file sizes reasonable.
const jpegQuality = 85

// MaybeDownscale returns data unchanged (resized=false) if:
//   - maxWidth or maxHeight is <= 0 (feature disabled),
//   - the image already fits within maxWidth x maxHeight, or
//   - the image format can't be decoded/re-encoded by this package
//     (only JPEG and PNG are supported; anything else - HEIC, WebP, GIF,
//     TIFF, ... - is passed through untouched rather than dropped).
//
// Otherwise it decodes the image, downsamples it to fit within
// maxWidth x maxHeight (preserving aspect ratio) using a box filter, and
// re-encodes it in its original format.
func MaybeDownscale(data []byte, maxWidth, maxHeight int) (out []byte, resized bool, err error) {
	if maxWidth <= 0 || maxHeight <= 0 {
		return data, false, nil
	}

	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		// Can't even read dimensions (corrupt data, or a format this
		// package doesn't recognize at all) - pass through untouched.
		return data, false, nil //nolint:nilerr // deliberate: unsupported/undecodable input falls back to passthrough
	}
	if cfg.Width <= maxWidth && cfg.Height <= maxHeight {
		return data, false, nil
	}
	if format != "jpeg" && format != "png" {
		// We can decode dimensions (e.g. GIF) but don't have an encoder
		// for it below, or it's a format we don't want to touch.
		return data, false, nil
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return data, false, nil //nolint:nilerr // same fallback reasoning as above
	}

	dst := downscale(img, maxWidth, maxHeight)

	var buf bytes.Buffer
	switch format {
	case "jpeg":
		if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: jpegQuality}); err != nil {
			return nil, false, fmt.Errorf("imageproc: jpeg encode failed: %w", err)
		}
	case "png":
		if err := png.Encode(&buf, dst); err != nil {
			return nil, false, fmt.Errorf("imageproc: png encode failed: %w", err)
		}
	}

	return buf.Bytes(), true, nil
}

// downscale resamples src down to fit within maxW x maxH (preserving
// aspect ratio) using a box filter: each destination pixel is the average
// of the block of source pixels it corresponds to. This is a deliberately
// simple algorithm - good enough for "smaller file for an old TV", not a
// replacement for a real image library.
func downscale(src image.Image, maxW, maxH int) *image.NRGBA {
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()

	scale := float64(maxW) / float64(sw)
	if hScale := float64(maxH) / float64(sh); hScale < scale {
		scale = hScale
	}

	dw := int(float64(sw)*scale + 0.5)
	dh := int(float64(sh)*scale + 0.5)
	if dw < 1 {
		dw = 1
	}
	if dh < 1 {
		dh = 1
	}

	dst := image.NewNRGBA(image.Rect(0, 0, dw, dh))

	for y := 0; y < dh; y++ {
		sy0 := y * sh / dh
		sy1 := (y + 1) * sh / dh
		if sy1 <= sy0 {
			sy1 = sy0 + 1
		}
		for x := 0; x < dw; x++ {
			sx0 := x * sw / dw
			sx1 := (x + 1) * sw / dw
			if sx1 <= sx0 {
				sx1 = sx0 + 1
			}
			dst.SetNRGBA(x, y, averageBox(src, sb, sx0, sx1, sy0, sy1))
		}
	}

	return dst
}

// averageBox averages the source pixels in [sx0,sx1) x [sy0,sy1) (relative
// to sb.Min) and returns the result as a straight-alpha NRGBA color.
func averageBox(src image.Image, sb image.Rectangle, sx0, sx1, sy0, sy1 int) color.NRGBA {
	var rSum, gSum, bSum, aSum, count uint64

	for sy := sy0; sy < sy1; sy++ {
		for sx := sx0; sx < sx1; sx++ {
			r, g, b, a := src.At(sb.Min.X+sx, sb.Min.Y+sy).RGBA() // premultiplied, 0-65535
			rSum += uint64(r)
			gSum += uint64(g)
			bSum += uint64(b)
			aSum += uint64(a)
			count++
		}
	}
	if count == 0 {
		return color.NRGBA{}
	}

	// Un-premultiply the averaged premultiplied values, then scale
	// 16-bit -> 8-bit.
	avgA := aSum / count
	if avgA == 0 {
		return color.NRGBA{}
	}
	r8 := to8((rSum / count) * 0xffff / avgA)
	g8 := to8((gSum / count) * 0xffff / avgA)
	b8 := to8((bSum / count) * 0xffff / avgA)
	a8 := to8(avgA)

	return color.NRGBA{R: r8, G: g8, B: b8, A: a8}
}

func to8(v uint64) uint8 {
	if v > 0xffff {
		v = 0xffff
	}
	return uint8(v >> 8) //nolint:gosec // v is clamped to 0-0xffff above
}
