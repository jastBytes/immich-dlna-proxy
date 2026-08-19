package dlna

import (
	"bytes"
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

	cfg := &config.Config{ImmichURL: fakeImmich.URL, APIKey: "test-key"}
	client := immich.New(cfg.ImmichURL, cfg.APIKey)
	srv := NewServer(cfg, client, c)

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
		APIKey:    "test-key",
		MaxWidth:  1920,
		MaxHeight: 1080,
	}
	client := immich.New(cfg.ImmichURL, cfg.APIKey)
	srv := NewServer(cfg, client, c)

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

func TestMediaHandlerNoCachePassthroughSupportsRange(t *testing.T) {
	fakeImmich := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/assets/abc123/original" {
			http.NotFound(w, r)
			return
		}
		if rng := r.Header.Get("Range"); rng != "" {
			w.Header().Set("Content-Type", "image/jpeg")
			w.Header().Set("Content-Range", "bytes 5-13/14")
			w.Header().Set("Accept-Ranges", "bytes")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte("jpeg-bytes"))
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("fake-jpeg-bytes"))
	}))
	defer fakeImmich.Close()

	cfg := &config.Config{ImmichURL: fakeImmich.URL, APIKey: "test-key"}
	client := immich.New(cfg.ImmichURL, cfg.APIKey)
	srv := NewServer(cfg, client, nil) // no cache configured

	ts := httptest.NewServer(srv.Mux())
	defer ts.Close()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/media/abc123", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Range", "bytes=5-13")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", resp.StatusCode)
	}
	if string(body) != "jpeg-bytes" {
		t.Fatalf("unexpected body %q", body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("unexpected Content-Type %q", ct)
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
		APIKey:    "test-key",
		MaxWidth:  1920,
		MaxHeight: 1080,
	}
	client := immich.New(cfg.ImmichURL, cfg.APIKey)
	srv := NewServer(cfg, client, nil) // no cache configured, resize enabled

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

	cfg := &config.Config{ImmichURL: fakeImmich.URL, APIKey: "test-key"}
	client := immich.New(cfg.ImmichURL, cfg.APIKey)
	srv := NewServer(cfg, client, nil)

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
	cfg := &config.Config{ImmichURL: "http://immich.local", APIKey: "test-key"}
	client := immich.New(cfg.ImmichURL, cfg.APIKey)
	srv := NewServer(cfg, client, nil)

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

	cfg := &config.Config{ImmichURL: fakeImmich.URL, APIKey: "test-key"}
	client := immich.New(cfg.ImmichURL, cfg.APIKey)
	srv := NewServer(cfg, client, c)

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
