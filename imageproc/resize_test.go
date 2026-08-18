package imageproc

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"
)

// makeJPEG creates a solid-color JPEG of the given size for testing.
func makeJPEG(t *testing.T, w, h int, c color.NRGBA) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}

func makePNG(t *testing.T, w, h int, c color.NRGBA) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func decodeDims(t *testing.T, data []byte) (int, int) {
	t.Helper()
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode config: %v", err)
	}
	return cfg.Width, cfg.Height
}

func TestDisabledReturnsUnchanged(t *testing.T) {
	src := makeJPEG(t, 400, 300, color.NRGBA{R: 200, G: 100, B: 50, A: 255})
	out, resized, err := MaybeDownscale(src, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if resized {
		t.Fatal("expected no resize when disabled (0,0)")
	}
	if !bytes.Equal(out, src) {
		t.Fatal("expected byte-identical passthrough when disabled")
	}
}

func TestImageWithinBoundsUnchanged(t *testing.T) {
	src := makeJPEG(t, 800, 600, color.NRGBA{R: 10, G: 20, B: 30, A: 255})
	out, resized, err := MaybeDownscale(src, 1920, 1080)
	if err != nil {
		t.Fatal(err)
	}
	if resized {
		t.Fatal("expected no resize; image already fits within bounds")
	}
	if !bytes.Equal(out, src) {
		t.Fatal("expected byte-identical passthrough when already within bounds")
	}
}

func TestJPEGDownscaledPreservingAspectRatio(t *testing.T) {
	src := makeJPEG(t, 4000, 2000, color.NRGBA{R: 128, G: 64, B: 200, A: 255}) // 2:1
	out, resized, err := MaybeDownscale(src, 1920, 1080)
	if err != nil {
		t.Fatal(err)
	}
	if !resized {
		t.Fatal("expected resize for oversized image")
	}
	w, h := decodeDims(t, out)
	// Bounded by width (1920) since aspect ratio 2:1 means height-based
	// scale (1080*2=2160) would exceed the width bound first.
	if w != 1920 || h != 960 {
		t.Fatalf("expected 1920x960, got %dx%d", w, h)
	}
	if len(out) == 0 {
		t.Fatal("expected non-empty output")
	}
}

func TestPNGDownscaled(t *testing.T) {
	src := makePNG(t, 3000, 3000, color.NRGBA{R: 1, G: 2, B: 3, A: 255})
	out, resized, err := MaybeDownscale(src, 500, 500)
	if err != nil {
		t.Fatal(err)
	}
	if !resized {
		t.Fatal("expected resize")
	}
	w, h := decodeDims(t, out)
	if w != 500 || h != 500 {
		t.Fatalf("expected 500x500, got %dx%d", w, h)
	}
	_, format, err := image.DecodeConfig(bytes.NewReader(out))
	if err != nil || format != "png" {
		t.Fatalf("expected output to still decode as png, got format=%q err=%v", format, err)
	}
}

func TestUnsupportedFormatPassesThroughUnchanged(t *testing.T) {
	// GIF: dimensions are readable (image/gif is imported for its
	// side-effecting registration), but this package has no GIF encoder,
	// so oversized GIFs must be returned untouched rather than dropped.
	img := image.NewPaletted(image.Rect(0, 0, 3000, 3000), []color.Color{color.White, color.Black})
	var buf bytes.Buffer
	if err := gif.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode gif: %v", err)
	}
	src := buf.Bytes()

	out, resized, err := MaybeDownscale(src, 500, 500)
	if err != nil {
		t.Fatal(err)
	}
	if resized {
		t.Fatal("expected GIF to be passed through unresized (no encoder for it)")
	}
	if !bytes.Equal(out, src) {
		t.Fatal("expected byte-identical passthrough for unsupported format")
	}
}

func TestCorruptDataPassesThroughWithoutError(t *testing.T) {
	src := []byte("not an image")
	out, resized, err := MaybeDownscale(src, 500, 500)
	if err != nil {
		t.Fatalf("expected no error for undecodable input, got %v", err)
	}
	if resized {
		t.Fatal("expected no resize for undecodable input")
	}
	if !bytes.Equal(out, src) {
		t.Fatal("expected byte-identical passthrough for undecodable input")
	}
}
