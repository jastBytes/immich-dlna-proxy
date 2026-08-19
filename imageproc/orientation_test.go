package imageproc

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

// marker builds a 3x2 image with a distinct color in each corner, so
// rotate/flip transforms can be checked by asking "where did the red
// pixel (originally top-left) end up?".
func marker(t *testing.T) *image.NRGBA {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	red := color.NRGBA{R: 255, A: 255}
	green := color.NRGBA{G: 255, A: 255}
	blue := color.NRGBA{B: 255, A: 255}
	yellow := color.NRGBA{R: 255, G: 255, A: 255}
	img.SetNRGBA(0, 0, red)    // top-left
	img.SetNRGBA(2, 0, green)  // top-right
	img.SetNRGBA(0, 1, blue)   // bottom-left
	img.SetNRGBA(2, 1, yellow) // bottom-right
	return img
}

func at(t *testing.T, img image.Image, x, y int) color.NRGBA {
	t.Helper()
	r, g, b, a := img.At(x, y).RGBA()
	return color.NRGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: uint8(a >> 8)}
}

func TestApplyOrientationCorners(t *testing.T) {
	red := color.NRGBA{R: 255, A: 255}
	green := color.NRGBA{G: 255, A: 255}
	blue := color.NRGBA{B: 255, A: 255}
	yellow := color.NRGBA{R: 255, G: 255, A: 255}

	tests := []struct {
		name           string
		o              int
		wantW, wantH   int
		tl, tr, bl, br color.NRGBA // expected corners of the output
	}{
		{"2_flipH", 2, 3, 2, green, red, yellow, blue},
		{"3_rotate180", 3, 3, 2, yellow, blue, green, red},
		{"4_flipV", 4, 3, 2, blue, yellow, red, green},
		{"6_rotate90CW", 6, 2, 3, blue, red, yellow, green},
		{"8_rotate90CCW", 8, 2, 3, green, yellow, red, blue},
		{"5_transpose", 5, 2, 3, red, blue, green, yellow},
		{"7_transverse", 7, 2, 3, yellow, green, blue, red},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := applyOrientation(marker(t), tt.o)
			b := out.Bounds()
			if b.Dx() != tt.wantW || b.Dy() != tt.wantH {
				t.Fatalf("dims: got %dx%d, want %dx%d", b.Dx(), b.Dy(), tt.wantW, tt.wantH)
			}
			if got := at(t, out, 0, 0); got != tt.tl {
				t.Errorf("top-left: got %v, want %v", got, tt.tl)
			}
			if got := at(t, out, tt.wantW-1, 0); got != tt.tr {
				t.Errorf("top-right: got %v, want %v", got, tt.tr)
			}
			if got := at(t, out, 0, tt.wantH-1); got != tt.bl {
				t.Errorf("bottom-left: got %v, want %v", got, tt.bl)
			}
			if got := at(t, out, tt.wantW-1, tt.wantH-1); got != tt.br {
				t.Errorf("bottom-right: got %v, want %v", got, tt.br)
			}
		})
	}
}

// exifJPEG builds a minimal valid JPEG (via the stdlib encoder) and splices
// in a synthetic APP1/Exif segment carrying just the orientation tag, so
// FixOrientation can be tested without a real camera file on disk.
func exifJPEG(t *testing.T, w, h, orientation int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	base := buf.Bytes()

	// Minimal TIFF/Exif block: header + 1 IFD entry (orientation) + next-IFD=0.
	tiff := make([]byte, 8+2+12+4)
	copy(tiff[0:2], "II")
	binary.LittleEndian.PutUint16(tiff[2:4], 42)
	binary.LittleEndian.PutUint32(tiff[4:8], 8)
	binary.LittleEndian.PutUint16(tiff[8:10], 1) // 1 entry
	binary.LittleEndian.PutUint16(tiff[10:12], 0x0112)
	binary.LittleEndian.PutUint16(tiff[12:14], 3) // type SHORT
	binary.LittleEndian.PutUint32(tiff[14:18], 1) // count
	binary.LittleEndian.PutUint16(tiff[18:20], uint16(orientation))
	binary.LittleEndian.PutUint32(tiff[22:26], 0) // next IFD offset

	app1Data := append([]byte("Exif\x00\x00"), tiff...)
	app1 := make([]byte, 2+2+len(app1Data))
	app1[0], app1[1] = 0xFF, 0xE1
	binary.BigEndian.PutUint16(app1[2:4], uint16(2+len(app1Data)))
	copy(app1[4:], app1Data)

	// Splice the APP1 segment in right after the SOI marker (first 2 bytes).
	out := make([]byte, 0, len(base)+len(app1))
	out = append(out, base[:2]...)
	out = append(out, app1...)
	out = append(out, base[2:]...)
	return out
}

func TestFixOrientationNoTagPassesThrough(t *testing.T) {
	src := exifJPEG(t, 4, 4, 1)
	out, changed, err := FixOrientation(src)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("orientation 1 should not be changed")
	}
	if !bytes.Equal(out, src) {
		t.Fatal("expected byte-identical passthrough for orientation 1")
	}
}

func TestFixOrientationRotatesAndStripsTag(t *testing.T) {
	src := exifJPEG(t, 4, 6, 6) // portrait-tagged-as-landscape, orientation 6
	out, changed, err := FixOrientation(src)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected orientation 6 to trigger a rewrite")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("output doesn't decode: %v", err)
	}
	if cfg.Width != 6 || cfg.Height != 4 {
		t.Fatalf("expected rotated dims 6x4, got %dx%d", cfg.Width, cfg.Height)
	}
	if o := jpegOrientation(out); o != 1 {
		t.Fatalf("expected re-encoded output to carry no orientation tag, got %d", o)
	}
}

func TestFixOrientationNonJPEGPassesThrough(t *testing.T) {
	src := []byte("not a jpeg")
	out, changed, err := FixOrientation(src)
	if err != nil {
		t.Fatal(err)
	}
	if changed || !bytes.Equal(out, src) {
		t.Fatal("expected non-JPEG data to pass through unchanged")
	}
}
