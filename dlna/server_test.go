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
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

func TestMediaHandlerServesVideoAndCachesIt(t *testing.T) {
	var immichHits int
	fakeImmich := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/assets/clip1/original" {
			immichHits++
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write([]byte("fake-video-bytes"))
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
		resp, err := http.Get(ts.URL + "/media/clip1")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if string(body) != "fake-video-bytes" {
			t.Fatalf("request %d: unexpected body %q", i, body)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "video/mp4" {
			t.Fatalf("request %d: unexpected content-type %q", i, ct)
		}
	}

	if immichHits != 1 {
		t.Fatalf("expected exactly 1 upstream hit (2nd request should be served from cache), got %d", immichHits)
	}
}

func TestMediaHandlerVideoSupportsRangeRequestsFromCache(t *testing.T) {
	fakeImmich := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/assets/clip1/original" {
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write([]byte("0123456789"))
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

	// Prime the cache.
	resp, err := http.Get(ts.URL + "/media/clip1")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/media/clip1", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Range", "bytes=2-4")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", resp2.StatusCode)
	}
	body, _ := io.ReadAll(resp2.Body)
	if string(body) != "234" {
		t.Fatalf("range body = %q, want %q", body, "234")
	}
}

func TestMediaHandlerVideoNoCacheStreamsDirectly(t *testing.T) {
	fakeImmich := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/assets/clip1/original" {
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write([]byte("fake-video-bytes"))
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

	resp, err := http.Get(ts.URL + "/media/clip1")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "fake-video-bytes" {
		t.Fatalf("unexpected body %q", body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "video/mp4" {
		t.Fatalf("unexpected content-type %q", ct)
	}
}

func TestThumbnailHandlerProxiesImmich(t *testing.T) {
	fakeImmich := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/assets/clip1/thumbnail" {
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("fake-thumb-bytes"))
			return
		}
		http.NotFound(w, r)
	}))
	defer fakeImmich.Close()

	cfg := &config.Config{ImmichURL: fakeImmich.URL, APIKeys: []string{"test-key"}}
	client := immich.New(cfg.ImmichURL, cfg.APIKeys[0])
	srv := NewServer(cfg, []UserClient{{Client: client}}, nil)

	ts := httptest.NewServer(srv.Mux())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/thumbnail/clip1")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "fake-thumb-bytes" {
		t.Fatalf("unexpected body %q", body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("unexpected content-type %q", ct)
	}
}

func TestThumbnailHandlerMultiUserRoutesToCorrectAccount(t *testing.T) {
	fake0 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/assets/clip1/thumbnail" {
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("thumb-from-account-0"))
			return
		}
		http.NotFound(w, r)
	}))
	defer fake0.Close()
	fake1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/assets/clip1/thumbnail" {
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("thumb-from-account-1"))
			return
		}
		http.NotFound(w, r)
	}))
	defer fake1.Close()

	cfg := &config.Config{APIKeys: []string{"key0", "key1"}}
	users := []UserClient{
		{Name: "Alice", Client: immich.New(fake0.URL, "key0")},
		{Name: "Bob", Client: immich.New(fake1.URL, "key1")},
	}
	srv := NewServer(cfg, users, nil)

	ts := httptest.NewServer(srv.Mux())
	defer ts.Close()

	resp0, err := http.Get(ts.URL + "/thumbnail/0/clip1")
	if err != nil {
		t.Fatal(err)
	}
	body0, _ := io.ReadAll(resp0.Body)
	_ = resp0.Body.Close()
	if string(body0) != "thumb-from-account-0" {
		t.Errorf("/thumbnail/0/clip1 body = %q, want thumb-from-account-0", body0)
	}

	resp1, err := http.Get(ts.URL + "/thumbnail/1/clip1")
	if err != nil {
		t.Fatal(err)
	}
	body1, _ := io.ReadAll(resp1.Body)
	_ = resp1.Body.Close()
	if string(body1) != "thumb-from-account-1" {
		t.Errorf("/thumbnail/1/clip1 body = %q, want thumb-from-account-1", body1)
	}
}

func TestThumbnailHandlerEmptyAssetIDReturns404(t *testing.T) {
	cfg := &config.Config{ImmichURL: "http://immich.local", APIKeys: []string{"test-key"}}
	client := immich.New(cfg.ImmichURL, cfg.APIKeys[0])
	srv := NewServer(cfg, []UserClient{{Client: client}}, nil)

	req := httptest.NewRequest(http.MethodGet, "/thumbnail/", nil)
	rec := httptest.NewRecorder()
	srv.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestThumbnailHandlerUpstreamErrorReturnsBadGateway(t *testing.T) {
	fakeImmich := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer fakeImmich.Close()

	cfg := &config.Config{ImmichURL: fakeImmich.URL, APIKeys: []string{"test-key"}}
	client := immich.New(cfg.ImmichURL, cfg.APIKeys[0])
	srv := NewServer(cfg, []UserClient{{Client: client}}, nil)

	ts := httptest.NewServer(srv.Mux())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/thumbnail/missing")
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

func TestPersonThumbnailHandlerCachesAfterFirstRequest(t *testing.T) {
	var immichHits int
	fakeImmich := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/people/p1/thumbnail" {
			immichHits++
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("fake-face-crop"))
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
		resp, err := http.Get(ts.URL + "/media/person/p1")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if string(body) != "fake-face-crop" {
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

func TestPersonThumbnailHandlerDoesNotCollideWithAssetCache(t *testing.T) {
	fakeImmich := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/assets/p1/original":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("asset-bytes"))
		case "/api/people/p1/thumbnail":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("thumbnail-bytes"))
		default:
			http.NotFound(w, r)
		}
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

	// A photo asset and a person can share the same ID (they're separate
	// Immich ID namespaces) - the two endpoints must not serve each
	// other's cached bytes.
	assetResp, err := http.Get(ts.URL + "/media/p1")
	if err != nil {
		t.Fatal(err)
	}
	assetBody, _ := io.ReadAll(assetResp.Body)
	_ = assetResp.Body.Close()
	if string(assetBody) != "asset-bytes" {
		t.Fatalf("asset body = %q, want %q", assetBody, "asset-bytes")
	}

	thumbResp, err := http.Get(ts.URL + "/media/person/p1")
	if err != nil {
		t.Fatal(err)
	}
	thumbBody, _ := io.ReadAll(thumbResp.Body)
	_ = thumbResp.Body.Close()
	if string(thumbBody) != "thumbnail-bytes" {
		t.Fatalf("thumbnail body = %q, want %q", thumbBody, "thumbnail-bytes")
	}
}

func TestPersonThumbnailHandlerEmptyPersonIDReturns404(t *testing.T) {
	cfg := &config.Config{ImmichURL: "http://immich.local", APIKeys: []string{"test-key"}}
	client := immich.New(cfg.ImmichURL, cfg.APIKeys[0])
	srv := NewServer(cfg, []UserClient{{Client: client}}, nil)

	req := httptest.NewRequest(http.MethodGet, "/media/person/", nil)
	rec := httptest.NewRecorder()
	srv.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestPersonThumbnailHandlerUpstreamErrorReturnsBadGateway(t *testing.T) {
	fakeImmich := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer fakeImmich.Close()

	cfg := &config.Config{ImmichURL: fakeImmich.URL, APIKeys: []string{"test-key"}}
	client := immich.New(cfg.ImmichURL, cfg.APIKeys[0])
	srv := NewServer(cfg, []UserClient{{Client: client}}, nil)

	ts := httptest.NewServer(srv.Mux())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/media/person/missing")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
}

// TestMediaHandlerLimitsConcurrentImmichFetches verifies that with
// MediaFetchConcurrency=1, a second cache-miss request for a different
// asset waits for the first to finish instead of hitting Immich at the
// same time - the core protection against a TV rapidly scrolling through
// a large album and firing off many simultaneous downloads.
func TestMediaHandlerLimitsConcurrentImmichFetches(t *testing.T) {
	release := make(chan struct{})
	var inFlight int32
	var maxInFlight int32

	fakeImmich := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&inFlight, 1)
		for {
			old := atomic.LoadInt32(&maxInFlight)
			if n <= old || atomic.CompareAndSwapInt32(&maxInFlight, old, n) {
				break
			}
		}
		<-release
		atomic.AddInt32(&inFlight, -1)
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("fake-jpeg-bytes"))
	}))
	defer fakeImmich.Close()

	cfg := &config.Config{ImmichURL: fakeImmich.URL, APIKeys: []string{"test-key"}, MediaFetchConcurrency: 1}
	client := immich.New(cfg.ImmichURL, cfg.APIKeys[0])
	srv := NewServer(cfg, []UserClient{{Client: client}}, nil)

	ts := httptest.NewServer(srv.Mux())
	defer ts.Close()

	var wg sync.WaitGroup
	for _, id := range []string{"a1", "a2"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			resp, err := http.Get(ts.URL + "/media/" + id)
			if err != nil {
				t.Error(err)
				return
			}
			_, _ = io.ReadAll(resp.Body)
			_ = resp.Body.Close()
		}(id)
	}

	// Give both requests time to reach the handler (or queue for a slot),
	// then let the fake Immich server proceed.
	time.Sleep(200 * time.Millisecond)
	close(release)
	wg.Wait()

	if maxInFlight != 1 {
		t.Fatalf("max concurrent Immich fetches = %d, want 1 (MediaFetchConcurrency=1)", maxInFlight)
	}
}

// TestMediaHandlerQueueTimeoutReturns503 verifies a request that can't get
// a free Immich-fetch slot within fetchQueueTimeout gets a 503 rather than
// hanging or piling onto Immich.
func TestMediaHandlerQueueTimeoutReturns503(t *testing.T) {
	origTimeout := fetchQueueTimeout
	fetchQueueTimeout = 100 * time.Millisecond
	defer func() { fetchQueueTimeout = origTimeout }()

	release := make(chan struct{})

	fakeImmich := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("fake-jpeg-bytes"))
	}))
	defer fakeImmich.Close()

	cfg := &config.Config{ImmichURL: fakeImmich.URL, APIKeys: []string{"test-key"}, MediaFetchConcurrency: 1}
	client := immich.New(cfg.ImmichURL, cfg.APIKeys[0])
	srv := NewServer(cfg, []UserClient{{Client: client}}, nil)

	ts := httptest.NewServer(srv.Mux())
	defer ts.Close()

	// Occupy the single slot with a request that won't complete until
	// released below, so the second request has to wait for a slot.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		resp, err := http.Get(ts.URL + "/media/busy")
		if err == nil {
			_, _ = io.ReadAll(resp.Body)
			_ = resp.Body.Close()
		}
	}()
	time.Sleep(50 * time.Millisecond)

	resp, err := http.Get(ts.URL + "/media/queued")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}

	close(release)
	wg.Wait()
}

func TestPersonThumbnailHandlerMultiUserRoutesToCorrectAccount(t *testing.T) {
	fake0 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/people/p1/thumbnail" {
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("face-from-account-0"))
			return
		}
		http.NotFound(w, r)
	}))
	defer fake0.Close()
	fake1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/people/p1/thumbnail" {
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("face-from-account-1"))
			return
		}
		http.NotFound(w, r)
	}))
	defer fake1.Close()

	cfg := &config.Config{APIKeys: []string{"key0", "key1"}}
	users := []UserClient{
		{Name: "Alice", Client: immich.New(fake0.URL, "key0")},
		{Name: "Bob", Client: immich.New(fake1.URL, "key1")},
	}
	srv := NewServer(cfg, users, nil)

	ts := httptest.NewServer(srv.Mux())
	defer ts.Close()

	resp0, err := http.Get(ts.URL + "/media/person/0/p1")
	if err != nil {
		t.Fatal(err)
	}
	body0, _ := io.ReadAll(resp0.Body)
	_ = resp0.Body.Close()
	if string(body0) != "face-from-account-0" {
		t.Errorf("/media/person/0/p1 body = %q, want face-from-account-0", body0)
	}

	resp1, err := http.Get(ts.URL + "/media/person/1/p1")
	if err != nil {
		t.Fatal(err)
	}
	body1, _ := io.ReadAll(resp1.Body)
	_ = resp1.Body.Close()
	if string(body1) != "face-from-account-1" {
		t.Errorf("/media/person/1/p1 body = %q, want face-from-account-1", body1)
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
