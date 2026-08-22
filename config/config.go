package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds all runtime settings for the proxy.
type Config struct {
	// ImmichURL is the base URL of the Immich server, e.g. http://192.168.1.10:2283
	ImmichURL string
	// APIKeys is one or more Immich API keys, each with at least
	// album.read / asset.read permissions. A single key (the common case)
	// browses as today - no extra folder level. When more than one key is
	// configured (IMMICH_API_KEYS), each key gets its own top-level folder
	// named after the Immich account it belongs to, so multiple users of
	// the same Immich server can each browse their own albums/people.
	APIKeys []string
	// ListenAddr is host:port the HTTP part of the DLNA server binds to, e.g. :8200
	ListenAddr string
	// FriendlyName is shown on TVs / DLNA clients when browsing available servers.
	FriendlyName string
	// UUID uniquely identifies this DLNA device. Keep it stable across restarts
	// so clients don't treat every restart as a brand new server.
	UUID string
	// Interface optionally restricts SSDP to a single network interface name
	// (e.g. "eth0"). Empty means "all interfaces".
	Interface string

	// CacheDir is where original photo bytes are cached on disk. Empty
	// disables caching (every view proxies straight from Immich again).
	CacheDir string
	// CacheMaxBytes is the soft size budget for CacheDir; oldest-accessed
	// files are evicted first once it's exceeded. <= 0 means unlimited.
	CacheMaxBytes int64

	// MaxWidth and MaxHeight bound the resolution photos are downscaled
	// to before being cached/served. Both 0 means the feature is disabled
	// and photos are served at their original resolution. Set via
	// MAX_RESOLUTION="WIDTHxHEIGHT", e.g. "1920x1080".
	MaxWidth  int
	MaxHeight int
}

func Load() (*Config, error) {
	cfg := &Config{
		ImmichURL:    os.Getenv("IMMICH_URL"),
		APIKeys:      parseAPIKeys(os.Getenv("IMMICH_API_KEYS"), os.Getenv("IMMICH_API_KEY")),
		ListenAddr:   getEnvDefault("LISTEN_ADDR", ":8200"),
		FriendlyName: getEnvDefault("FRIENDLY_NAME", "Immich Photos"),
		UUID:         getEnvDefault("DEVICE_UUID", "3e7f0f4e-8c2e-4f7a-9c2a-immichdlna01"),
		Interface:    os.Getenv("SSDP_INTERFACE"),
		CacheDir:     getEnvDefault("CACHE_DIR", "/config/cache"),
	}

	if cfg.ImmichURL == "" {
		return nil, fmt.Errorf("IMMICH_URL is not set")
	}
	if len(cfg.APIKeys) == 0 {
		return nil, fmt.Errorf("IMMICH_API_KEY (or IMMICH_API_KEYS) is not set")
	}

	if os.Getenv("DISABLE_CACHE") == "true" {
		cfg.CacheDir = ""
	}

	maxMB := getEnvDefault("CACHE_MAX_MB", "2048")
	mb, err := strconv.ParseInt(maxMB, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("CACHE_MAX_MB must be an integer, got %q", maxMB)
	}
	cfg.CacheMaxBytes = mb * 1024 * 1024

	maxW, maxH, err := parseMaxResolution(os.Getenv("MAX_RESOLUTION"))
	if err != nil {
		return nil, err
	}
	cfg.MaxWidth, cfg.MaxHeight = maxW, maxH

	return cfg, nil
}

// parseAPIKeys builds the configured API key list. IMMICH_API_KEYS (a
// comma-separated list, for multiple Immich accounts sharing this proxy)
// takes precedence when it contains at least one non-empty entry;
// otherwise it falls back to the single IMMICH_API_KEY. An empty result
// means neither is set, which Load() rejects.
func parseAPIKeys(multi, single string) []string {
	var keys []string
	for _, k := range strings.Split(multi, ",") {
		if k = strings.TrimSpace(k); k != "" {
			keys = append(keys, k)
		}
	}
	if len(keys) > 0 {
		return keys
	}
	if single != "" {
		return []string{single}
	}
	return nil
}

// parseMaxResolution parses a "WIDTHxHEIGHT" string (e.g. "1920x1080").
// An empty string returns (0, 0, nil), meaning "disabled".
func parseMaxResolution(s string) (width, height int, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, nil
	}

	parts := strings.SplitN(strings.ToLower(s), "x", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf(`MAX_RESOLUTION must look like "WIDTHxHEIGHT" (e.g. "1920x1080"), got %q`, s)
	}

	width, err = strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || width <= 0 {
		return 0, 0, fmt.Errorf("MAX_RESOLUTION: invalid width in %q", s)
	}
	height, err = strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || height <= 0 {
		return 0, 0, fmt.Errorf("MAX_RESOLUTION: invalid height in %q", s)
	}

	return width, height, nil
}

func getEnvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
