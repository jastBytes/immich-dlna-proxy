package dlna

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jastBytes/immich-dlna-proxy/cache"
	"github.com/jastBytes/immich-dlna-proxy/config"
	"github.com/jastBytes/immich-dlna-proxy/immich"
)

func TestMediaHandlerCachesAfterFirstRequest(t *testing.T) {
	var immichHits int
	fakeImmich := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/assets/abc123/original" {
			immichHits++
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("fake-jpeg-bytes"))
			return
		}
		http.NotFound(w, r)
	}))
	defer fakeImmich.Close()

	dir := t.TempDir()
	c, err := cache.New(dir, 0)
	if err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{ImmichURL: fakeImmich.URL, APIKeys: []string{"test-key"}}
	client := immich.New(cfg.ImmichURL, cfg.APIKeys[0])
	srv := NewServer(cfg, []UserClient{{Client: client}}, c)

	ts := httptest.NewServer(srv.Mux())
	defer ts.Close()

	for i := 0; i < 2; i++ {
		resp, err := http.Get(ts.URL + "/media/abc123")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if string(body) != "fake-jpeg-bytes" {
			t.Fatalf("request %d: unexpected body %q", i, body)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "image/jpeg" {
			t.Fatalf("request %d: unexpected content-type %q", i, ct)
		}
	}

	if immichHits != 1 {
		t.Fatalf("expected exactly 1 upstream hit (2nd request should be served from cache), got %d", immichHits)
	}
}

func TestMediaHandlerMultiUserRoutesToCorrectAccount(t *testing.T) {
	fake0 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/assets/pic/original" {
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("from-account-0"))
			return
		}
		http.NotFound(w, r)
	}))
	defer fake0.Close()
	fake1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/assets/pic/original" {
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("from-account-1"))
			return
		}
		http.NotFound(w, r)
	}))
	defer fake1.Close()

	cfg := &config.Config{ImmichURL: fake0.URL, APIKeys: []string{"key0", "key1"}}
	users := []UserClient{
		{Name: "Alice", Client: immich.New(fake0.URL, "key0")},
		{Name: "Bob", Client: immich.New(fake1.URL, "key1")},
	}
	srv := NewServer(cfg, users, nil)

	ts := httptest.NewServer(srv.Mux())
	defer ts.Close()

	resp0, err := http.Get(ts.URL + "/media/0/pic")
	if err != nil {
		t.Fatal(err)
	}
	body0, _ := io.ReadAll(resp0.Body)
	_ = resp0.Body.Close()
	if string(body0) != "from-account-0" {
		t.Errorf("/media/0/pic body = %q, want from-account-0", body0)
	}

	resp1, err := http.Get(ts.URL + "/media/1/pic")
	if err != nil {
		t.Fatal(err)
	}
	body1, _ := io.ReadAll(resp1.Body)
	_ = resp1.Body.Close()
	if string(body1) != "from-account-1" {
		t.Errorf("/media/1/pic body = %q, want from-account-1", body1)
	}

	respBad, err := http.Get(ts.URL + "/media/5/pic")
	if err != nil {
		t.Fatal(err)
	}
	_ = respBad.Body.Close()
	if respBad.StatusCode != http.StatusNotFound {
		t.Errorf("/media/5/pic status = %d, want 404", respBad.StatusCode)
	}
}

func TestMediaHandlerDownscalesOversizedImage(t *testing.T) {
	oversized := makeTestJPEG(t, 4000, 2000)

	fakeImmich := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/assets/big1/original" {
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(oversized)
			return
		}
		http.NotFound(w, r)
	}))
	defer fakeImmich.Close()

	dir := t.TempDir()
	c, err := cache.New(dir, 0)
	if err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		ImmichURL: fakeImmich.URL,
		APIKeys:   []string{"test-key"},
		MaxWidth:  1920,
		MaxHeight: 1080,
	}
	client := immich.New(cfg.ImmichURL, cfg.APIKeys[0])
	srv := NewServer(cfg, []UserClient{{Client: client}}, c)

	ts := httptest.NewServer(srv.Mux())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/media/big1")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if len(body) >= len(oversized) {
		t.Fatalf("expected downscaled output to be smaller than %d bytes, got %d", len(oversized), len(body))
	}

	cfgOut, _, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("served body doesn't decode as an image: %v", err)
	}
	if cfgOut.Width > 1920 || cfgOut.Height > 1080 {
		t.Fatalf("expected image within 1920x1080, got %dx%d", cfgOut.Width, cfgOut.Height)
	}

	// Second request should be served from the (already-downscaled) cache.
	resp2, err := http.Get(ts.URL + "/media/big1")
	if err != nil {
		t.Fatal(err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	_ = resp2.Body.Close()
	if !bytes.Equal(body, body2) {
		t.Fatal("expected cached response to match the first (already-downscaled) response")
	}
}

func TestMediaHandlerFixesOrientationEvenWithoutCacheOrResize(t *testing.T) {
	// orientation 6: sensor recorded this 6x4 photo sideways; it should
	// display upright (4x6) once served, even with caching and resizing
	// both disabled - most DLNA renderers ignore the EXIF tag itself.
	src := makeExifJPEG(t, 6, 4, 6)

	fakeImmich := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/assets/rot1/original" {
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(src)
			return
		}
		http.NotFound(w, r)
	}))
	defer fakeImmich.Close()

	cfg := &config.Config{ImmichURL: fakeImmich.URL, APIKeys: []string{"test-key"}}
	client := immich.New(cfg.ImmichURL, cfg.APIKeys[0])
	srv := NewServer(cfg, []UserClient{{Client: client}}, nil) // no cache

	ts := httptest.NewServer(srv.Mux())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/media/rot1")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	cfgOut, _, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("served body doesn't decode as an image: %v", err)
	}
	if cfgOut.Width != 4 || cfgOut.Height != 6 {
		t.Fatalf("expected rotated dims 4x6, got %dx%d", cfgOut.Width, cfgOut.Height)
	}
}

func TestMediaHandlerNoCacheStillResizes(t *testing.T) {
	oversized := makeTestJPEG(t, 4000, 2000)

	fakeImmich := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/assets/big1/original" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(oversized)
	}))
	defer fakeImmich.Close()

	cfg := &config.Config{
		ImmichURL: fakeImmich.URL,
		APIKeys:   []string{"test-key"},
		MaxWidth:  1920,
		MaxHeight: 1080,
	}
	client := immich.New(cfg.ImmichURL, cfg.APIKeys[0])
	srv := NewServer(cfg, []UserClient{{Client: client}}, nil) // no cache configured, resize enabled

	ts := httptest.NewServer(srv.Mux())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/media/big1")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if len(body) >= len(oversized) {
		t.Fatalf("expected downscaled output smaller than %d bytes, got %d", len(oversized), len(body))
	}
	cfgOut, _, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("served body doesn't decode as an image: %v", err)
	}
	if cfgOut.Width > 1920 || cfgOut.Height > 1080 {
		t.Fatalf("expected image within 1920x1080, got %dx%d", cfgOut.Width, cfgOut.Height)
	}
}

func TestMediaHandlerNoCacheUpstreamErrorReturnsBadGateway(t *testing.T) {
	fakeImmich := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer fakeImmich.Close()

	cfg := &config.Config{ImmichURL: fakeImmich.URL, APIKeys: []string{"test-key"}}
	client := immich.New(cfg.ImmichURL, cfg.APIKeys[0])
	srv := NewServer(cfg, []UserClient{{Client: client}}, nil)

	ts := httptest.NewServer(srv.Mux())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/media/missing")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
}

func TestMediaHandlerEmptyAssetIDReturns404(t *testing.T) {
	cfg := &config.Config{ImmichURL: "http://immich.local", APIKeys: []string{"test-key"}}
	client := immich.New(cfg.ImmichURL, cfg.APIKeys[0])
	srv := NewServer(cfg, []UserClient{{Client: client}}, nil)

	req := httptest.NewRequest(http.MethodGet, "/media/", nil)
	rec := httptest.NewRecorder()
	srv.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestMediaHandlerCacheMissDownloadErrorReturnsBadGateway(t *testing.T) {
	fakeImmich := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer fakeImmich.Close()

	dir := t.TempDir()
	c, err := cache.New(dir, 0)
	if err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{ImmichURL: fakeImmich.URL, APIKeys: []string{"test-key"}}
	client := immich.New(cfg.ImmichURL, cfg.APIKeys[0])
	srv := NewServer(cfg, []UserClient{{Client: client}}, c)

	ts := httptest.NewServer(srv.Mux())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/media/missing")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
}

// makeExifJPEG builds a JPEG with a synthetic APP1/Exif segment carrying
// just the orientation tag, so tests don't need a real camera file on disk.
func makeExifJPEG(t *testing.T, w, h, orientation int) []byte {
	t.Helper()
	base := makeTestJPEG(t, w, h)

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

	out := make([]byte, 0, len(base)+len(app1))
	out = append(out, base[:2]...) // SOI marker
	out = append(out, app1...)
	out = append(out, base[2:]...)
	return out
}

func makeTestJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode test jpeg: %v", err)
	}
	return buf.Bytes()
}
