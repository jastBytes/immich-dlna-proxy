package config

import (
	"strings"
	"testing"
	"time"
)

// clearConfigEnv unsets every env var Load reads, so each test starts from
// a clean slate regardless of what the test binary's environment carries.
func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"IMMICH_URL", "IMMICH_API_KEY", "IMMICH_API_KEYS", "LISTEN_ADDR", "FRIENDLY_NAME",
		"DEVICE_UUID", "SSDP_INTERFACE", "CACHE_DIR", "DISABLE_CACHE",
		"CACHE_MAX_MB", "MAX_RESOLUTION", "MEDIA_FETCH_CONCURRENCY", "TITLE_DATE_PREFIX", "TITLE_DATE_PREFIX_DESC",
		"LISTING_CACHE_SECONDS", "TIMELINE_GROUPING", "ADVERTISE_IP", "DEBUG", "PHOTO_SOURCE", "VIDEO_SOURCE", "EXTRA_FOLDERS", "TIMELINE_REFRESH_MINUTES",
	} {
		t.Setenv(k, "")
	}
}

func TestLoadMissingImmichURL(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("IMMICH_API_KEY", "key")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when IMMICH_URL is not set")
	}
}

func TestLoadMissingAPIKey(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("IMMICH_URL", "http://immich.local")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when IMMICH_API_KEY is not set")
	}
}

func TestLoadMultipleAPIKeys(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("IMMICH_URL", "http://immich.local")
	t.Setenv("IMMICH_API_KEYS", "key1, key2 ,key3")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"key1", "key2", "key3"}
	if len(cfg.APIKeys) != len(want) {
		t.Fatalf("APIKeys = %v, want %v", cfg.APIKeys, want)
	}
	for i, k := range want {
		if cfg.APIKeys[i] != k {
			t.Errorf("APIKeys[%d] = %q, want %q", i, cfg.APIKeys[i], k)
		}
	}
}

func TestLoadAPIKeysTakesPrecedenceOverSingle(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("IMMICH_URL", "http://immich.local")
	t.Setenv("IMMICH_API_KEY", "single")
	t.Setenv("IMMICH_API_KEYS", "key1,key2")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.APIKeys) != 2 || cfg.APIKeys[0] != "key1" || cfg.APIKeys[1] != "key2" {
		t.Fatalf("APIKeys = %v, want [key1 key2]", cfg.APIKeys)
	}
}

func TestLoadAPIKeysBlankFallsBackToSingle(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("IMMICH_URL", "http://immich.local")
	t.Setenv("IMMICH_API_KEY", "single")
	t.Setenv("IMMICH_API_KEYS", " , ,")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.APIKeys) != 1 || cfg.APIKeys[0] != "single" {
		t.Fatalf("APIKeys = %v, want [single]", cfg.APIKeys)
	}
}

func TestLoadDefaults(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("IMMICH_URL", "http://immich.local")
	t.Setenv("IMMICH_API_KEY", "key")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ImmichURL != "http://immich.local" {
		t.Errorf("ImmichURL = %q", cfg.ImmichURL)
	}
	if len(cfg.APIKeys) != 1 || cfg.APIKeys[0] != "key" {
		t.Errorf("APIKeys = %v, want [key]", cfg.APIKeys)
	}
	if cfg.ListenAddr != ":8200" {
		t.Errorf("ListenAddr default = %q, want :8200", cfg.ListenAddr)
	}
	if cfg.FriendlyName != "Immich Photos" {
		t.Errorf("FriendlyName default = %q", cfg.FriendlyName)
	}
	if cfg.UUID != "3e7f0f4e-8c2e-4f7a-9c2a-immichdlna01" {
		t.Errorf("UUID default = %q", cfg.UUID)
	}
	if cfg.Interface != "" {
		t.Errorf("Interface default = %q, want empty", cfg.Interface)
	}
	if cfg.CacheDir != "/config/cache" {
		t.Errorf("CacheDir default = %q", cfg.CacheDir)
	}
	if cfg.CacheMaxBytes != 2048*1024*1024 {
		t.Errorf("CacheMaxBytes default = %d, want %d", cfg.CacheMaxBytes, 2048*1024*1024)
	}
	if cfg.MaxWidth != 0 || cfg.MaxHeight != 0 {
		t.Errorf("MaxWidth/MaxHeight default = %dx%d, want 0x0", cfg.MaxWidth, cfg.MaxHeight)
	}
	if cfg.MediaFetchConcurrency != 4 {
		t.Errorf("MediaFetchConcurrency default = %d, want 4", cfg.MediaFetchConcurrency)
	}
	if cfg.TitleDatePrefix {
		t.Errorf("TitleDatePrefix default = true, want false")
	}
	if cfg.TitleDatePrefixDescending {
		t.Errorf("TitleDatePrefixDescending default = true, want false")
	}
	if cfg.TimelineRefresh != 15*time.Minute {
		t.Errorf("TimelineRefresh default = %s, want 15m", cfg.TimelineRefresh)
	}
	if cfg.ListingCacheTTL != 30*time.Second {
		t.Errorf("ListingCacheTTL default = %s, want 30s", cfg.ListingCacheTTL)
	}
	if cfg.TimelineGrouping != "none" {
		t.Errorf("TimelineGrouping default = %q, want none", cfg.TimelineGrouping)
	}
	if cfg.AdvertiseIP != "" {
		t.Errorf("AdvertiseIP default = %q, want empty", cfg.AdvertiseIP)
	}
	if cfg.Debug {
		t.Errorf("Debug default = true, want false")
	}
	if cfg.VideoSource != "original" {
		t.Errorf("VideoSource default = %q, want original", cfg.VideoSource)
	}
	if strings.Join(cfg.ExtraFolders, ",") != "favorites,onthisday" {
		t.Errorf("ExtraFolders default = %v, want [favorites onthisday]", cfg.ExtraFolders)
	}
	if cfg.PhotoSource != "auto" {
		t.Errorf("PhotoSource default = %q, want auto", cfg.PhotoSource)
	}
}

func TestLoadOverrides(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("IMMICH_URL", "http://immich.local")
	t.Setenv("IMMICH_API_KEY", "key")
	t.Setenv("LISTEN_ADDR", ":9000")
	t.Setenv("FRIENDLY_NAME", "My Photos")
	t.Setenv("DEVICE_UUID", "custom-uuid")
	t.Setenv("SSDP_INTERFACE", "eth0")
	t.Setenv("CACHE_DIR", "/custom/cache")
	t.Setenv("CACHE_MAX_MB", "100")
	t.Setenv("MAX_RESOLUTION", "1920x1080")
	t.Setenv("MEDIA_FETCH_CONCURRENCY", "8")
	t.Setenv("TITLE_DATE_PREFIX", "true")
	t.Setenv("TITLE_DATE_PREFIX_DESC", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ListenAddr != ":9000" {
		t.Errorf("ListenAddr = %q", cfg.ListenAddr)
	}
	if cfg.FriendlyName != "My Photos" {
		t.Errorf("FriendlyName = %q", cfg.FriendlyName)
	}
	if cfg.UUID != "custom-uuid" {
		t.Errorf("UUID = %q", cfg.UUID)
	}
	if cfg.Interface != "eth0" {
		t.Errorf("Interface = %q", cfg.Interface)
	}
	if cfg.CacheDir != "/custom/cache" {
		t.Errorf("CacheDir = %q", cfg.CacheDir)
	}
	if cfg.CacheMaxBytes != 100*1024*1024 {
		t.Errorf("CacheMaxBytes = %d, want %d", cfg.CacheMaxBytes, 100*1024*1024)
	}
	if cfg.MaxWidth != 1920 || cfg.MaxHeight != 1080 {
		t.Errorf("MaxWidth/MaxHeight = %dx%d, want 1920x1080", cfg.MaxWidth, cfg.MaxHeight)
	}
	if cfg.MediaFetchConcurrency != 8 {
		t.Errorf("MediaFetchConcurrency = %d, want 8", cfg.MediaFetchConcurrency)
	}
	if !cfg.TitleDatePrefix {
		t.Errorf("TitleDatePrefix = false, want true")
	}
	if !cfg.TitleDatePrefixDescending {
		t.Errorf("TitleDatePrefixDescending = false, want true")
	}
}

func TestLoadDisableCache(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("IMMICH_URL", "http://immich.local")
	t.Setenv("IMMICH_API_KEY", "key")
	t.Setenv("CACHE_DIR", "/custom/cache")
	t.Setenv("DISABLE_CACHE", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.CacheDir != "" {
		t.Errorf("CacheDir = %q, want empty when DISABLE_CACHE=true", cfg.CacheDir)
	}
}

func TestLoadInvalidCacheMaxMB(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("IMMICH_URL", "http://immich.local")
	t.Setenv("IMMICH_API_KEY", "key")
	t.Setenv("CACHE_MAX_MB", "not-a-number")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid CACHE_MAX_MB")
	}
}

func TestLoadInvalidMaxResolution(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("IMMICH_URL", "http://immich.local")
	t.Setenv("IMMICH_API_KEY", "key")
	t.Setenv("MAX_RESOLUTION", "not-a-resolution")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid MAX_RESOLUTION")
	}
}

func TestLoadInvalidMediaFetchConcurrency(t *testing.T) {
	cases := []string{"not-a-number", "0", "-1"}
	for _, v := range cases {
		t.Run(v, func(t *testing.T) {
			clearConfigEnv(t)
			t.Setenv("IMMICH_URL", "http://immich.local")
			t.Setenv("IMMICH_API_KEY", "key")
			t.Setenv("MEDIA_FETCH_CONCURRENCY", v)

			if _, err := Load(); err == nil {
				t.Fatalf("expected error for MEDIA_FETCH_CONCURRENCY=%q", v)
			}
		})
	}
}

func TestParseMaxResolution(t *testing.T) {
	cases := []struct {
		in          string
		wantW       int
		wantH       int
		wantErr     bool
		description string
	}{
		{"", 0, 0, false, "empty means disabled"},
		{"1920x1080", 1920, 1080, false, "lowercase x"},
		{"1920X1080", 1920, 1080, false, "uppercase X"},
		{" 1920x1080 ", 1920, 1080, false, "surrounding whitespace"},
		{"3840x2160", 3840, 2160, false, "4K"},
		{"1920", 0, 0, true, "missing height"},
		{"1920x", 0, 0, true, "empty height"},
		{"x1080", 0, 0, true, "empty width"},
		{"0x1080", 0, 0, true, "zero width rejected"},
		{"1920x0", 0, 0, true, "zero height rejected"},
		{"-1920x1080", 0, 0, true, "negative width rejected"},
		{"abcxdef", 0, 0, true, "non-numeric"},
	}

	for _, c := range cases {
		t.Run(c.description, func(t *testing.T) {
			w, h, err := parseMaxResolution(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("input %q: expected error, got none", c.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("input %q: unexpected error: %v", c.in, err)
			}
			if w != c.wantW || h != c.wantH {
				t.Fatalf("input %q: got %dx%d, want %dx%d", c.in, w, h, c.wantW, c.wantH)
			}
		})
	}
}

func loadWith(t *testing.T, env map[string]string) (*Config, error) {
	t.Helper()
	clearConfigEnv(t)
	t.Setenv("IMMICH_URL", "http://immich.local")
	t.Setenv("IMMICH_API_KEY", "key")
	for k, v := range env {
		t.Setenv(k, v)
	}
	return Load()
}

func TestLoadNewOptions(t *testing.T) {
	cfg, err := loadWith(t, map[string]string{
		"LISTING_CACHE_SECONDS": "0",
		"TIMELINE_GROUPING":     "Month",
		"ADVERTISE_IP":          "192.168.1.50",
		"DEBUG":                 "true",
		"PHOTO_SOURCE":          "Preview",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListingCacheTTL != 0 {
		t.Errorf("ListingCacheTTL = %s, want 0 (disabled)", cfg.ListingCacheTTL)
	}
	if cfg.TimelineGrouping != "month" {
		t.Errorf("TimelineGrouping = %q, want month", cfg.TimelineGrouping)
	}
	if cfg.AdvertiseIP != "192.168.1.50" {
		t.Errorf("AdvertiseIP = %q", cfg.AdvertiseIP)
	}
	if !cfg.Debug {
		t.Error("Debug = false, want true")
	}
	if cfg.PhotoSource != "preview" {
		t.Errorf("PhotoSource = %q, want preview", cfg.PhotoSource)
	}
}

func TestLoadRejectsInvalidNewOptions(t *testing.T) {
	for _, env := range []map[string]string{
		{"LISTING_CACHE_SECONDS": "-1"},
		{"LISTING_CACHE_SECONDS": "30s"},
		{"TIMELINE_GROUPING": "week"},
		{"ADVERTISE_IP": "not-an-ip"},
		{"ADVERTISE_IP": "::1"},
		{"PHOTO_SOURCE": "thumbnail"},
		{"VIDEO_SOURCE": "hevc"},
		{"TIMELINE_REFRESH_MINUTES": "0"},
		{"TIMELINE_REFRESH_MINUTES": "15m"},
		{"EXTRA_FOLDERS": "favorites,memories"},
	} {
		if _, err := loadWith(t, env); err == nil {
			t.Errorf("Load() with %v succeeded, want an error", env)
		}
	}
}

func TestParseExtraFolders(t *testing.T) {
	cases := map[string]string{
		"favorites,onthisday":          "favorites,onthisday",
		" Places , random,places ":     "places,random",
		"none":                         "",
		"":                             "",
		"random,favorites,onthisday,x": "error",
	}
	for in, want := range cases {
		got, err := parseExtraFolders(in)
		if want == "error" {
			if err == nil {
				t.Errorf("%q: want error, got %v", in, got)
			}
			continue
		}
		if err != nil || strings.Join(got, ",") != want {
			t.Errorf("%q: got %v, %v; want %q", in, got, err, want)
		}
	}
}
