package config

import "testing"

// clearConfigEnv unsets every env var Load reads, so each test starts from
// a clean slate regardless of what the test binary's environment carries.
func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"IMMICH_URL", "IMMICH_API_KEY", "LISTEN_ADDR", "FRIENDLY_NAME",
		"DEVICE_UUID", "SSDP_INTERFACE", "CACHE_DIR", "DISABLE_CACHE",
		"CACHE_MAX_MB", "MAX_RESOLUTION",
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
	if cfg.APIKey != "key" {
		t.Errorf("APIKey = %q", cfg.APIKey)
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
