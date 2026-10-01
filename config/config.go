package config

import (
	"fmt"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime settings for the proxy.
type Config struct {
	// ImmichURL is the base URL of the Immich server, e.g. http://192.168.1.10:2283
	ImmichURL string
	// APIKeys is one or more Immich API keys, each with at least
	// album.read / asset.read / asset.download / asset.view / person.read
	// permissions (plus user.read when more than one is configured) - see
	// docs/configuration.md.
	// A single key (the common case) browses as today - no extra folder
	// level. When more than one key is configured (IMMICH_API_KEYS), each
	// key gets its own top-level folder named after the Immich account it
	// belongs to, so multiple users of the same Immich server can each
	// browse their own albums/people.
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
	// AdvertiseIP overrides the IP address announced to DLNA clients in
	// SSDP LOCATION URLs (ADVERTISE_IP). Empty means auto-detect: the
	// IPv4 address of Interface if set, otherwise the address the OS
	// would use to reach the internet.
	AdvertiseIP string

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

	// MediaFetchConcurrency caps how many /media/* requests may be
	// downloading from Immich at once; additional requests queue for a
	// free slot instead of firing off another concurrent Immich request.
	// This keeps a TV rapidly scrolling through a large album from
	// hammering Immich with dozens of simultaneous downloads.
	MediaFetchConcurrency int

	// TitleDatePrefix, when true, prefixes every photo/video item's
	// dc:title with its capture date ("2024-05-01 IMG_1234.jpg") instead
	// of the bare filename. Some DLNA clients (Samsung's Smart TV browser
	// is the known case) always display items sorted by dc:title
	// themselves, ignoring both the order Browse returns them in and any
	// SortCriteria the client itself could send - there's no on-TV option
	// to switch that to date order. Prefixing the title with a sortable
	// date makes the TV's own alphabetical sort come out chronological.
	TitleDatePrefix bool

	// TitleDatePrefixDescending, when true (and only meaningful alongside
	// TitleDatePrefix), makes the client's own ascending alphabetical
	// title sort come out newest-first instead of oldest-first. A plain
	// calendar date can't be made to sort in reverse while still reading
	// as a real date under simple ASCII comparison, so in this mode the
	// title instead leads with a zero-padded countdown number (seconds
	// until a fixed far-future instant - smaller for newer assets, so it
	// sorts first) followed by the actual human-readable date and
	// filename - see assetTitle in dlna/contentdirectory.go.
	TitleDatePrefixDescending bool

	// ListingCacheTTL is how long Immich listing responses (albums,
	// people, an album's/person's assets, the timeline) are reused across
	// Browse calls. A TV pages through a container with many small Browse
	// requests; without this, each one re-fetches the complete listing
	// from Immich. Set via LISTING_CACHE_SECONDS (default 30); 0 disables
	// it so every Browse hits Immich live. Photo/video bytes are cached
	// separately (CacheDir) and unaffected.
	ListingCacheTTL time.Duration

	// TimelineRefresh is how old the in-memory copy of the full timeline
	// listing (used by the Timeline, Places and Random folders) may get
	// before the next access refreshes it in the background. That listing
	// is loaded at startup and always served from memory, because fetching
	// it from Immich takes large libraries longer than TVs wait for a
	// Browse response. Set via TIMELINE_REFRESH_MINUTES (default 15,
	// minimum 1).
	TimelineRefresh time.Duration

	// PhotoSource selects which bytes /media/ serves for photos:
	// "auto" (default) serves JPEG/PNG originals as-is and anything else
	// (HEIC, WebP, TIFF, ...) as Immich's generated JPEG preview, since
	// most TVs can't display those formats; "original" always serves the
	// original file; "preview" always serves the preview (smaller, faster,
	// lower resolution). Videos are unaffected. Set via PHOTO_SOURCE.
	PhotoSource string

	// VideoSource selects which bytes /media/ serves for videos:
	// "original" (default) always serves the original file; "transcoded"
	// serves Immich's playback version (GET /api/assets/{id}/video/
	// playback) - whatever Immich transcoded under its own transcoding
	// policy, H.264/AAC in MP4 by default, or the original where it didn't
	// transcode. Set via VIDEO_SOURCE.
	VideoSource string

	// ExtraFolders lists the optional root folders to show next to
	// Albums/People/Timeline, in order: "favorites", "onthisday",
	// "places", "random". Set via EXTRA_FOLDERS (comma-separated, default
	// "favorites,onthisday"; "none" for no extra folders).
	ExtraFolders []string

	// TimelineGrouping controls how the Timeline folder is organized:
	// "none" (default) lists every photo/video flat, newest first; "year"
	// adds one folder per year; "month" adds year folders containing one
	// folder per month. Set via TIMELINE_GROUPING.
	TimelineGrouping string

	// Debug enables verbose logging (DEBUG=true): every /media/ and
	// /thumbnail/ request, per-photo orientation/resize notes, and
	// background cache fills. Off by default because a TV scrolling
	// through an album produces one such line per photo; protocol
	// requests (description, Browse) are always logged regardless.
	Debug bool
}

func Load() (*Config, error) {
	cfg := &Config{
		ImmichURL:    os.Getenv("IMMICH_URL"),
		APIKeys:      parseAPIKeys(os.Getenv("IMMICH_API_KEYS"), os.Getenv("IMMICH_API_KEY")),
		ListenAddr:   getEnvDefault("LISTEN_ADDR", ":8200"),
		FriendlyName: getEnvDefault("FRIENDLY_NAME", "Immich Photos"),
		UUID:         getEnvDefault("DEVICE_UUID", "3e7f0f4e-8c2e-4f7a-9c2a-immichdlna01"),
		Interface:    os.Getenv("SSDP_INTERFACE"),
		AdvertiseIP:  strings.TrimSpace(os.Getenv("ADVERTISE_IP")),
		CacheDir:     getEnvDefault("CACHE_DIR", "/config/cache"),
	}

	if cfg.ImmichURL == "" {
		return nil, fmt.Errorf("IMMICH_URL is not set")
	}
	if len(cfg.APIKeys) == 0 {
		return nil, fmt.Errorf("IMMICH_API_KEY (or IMMICH_API_KEYS) is not set")
	}

	if cfg.AdvertiseIP != "" {
		if ip := net.ParseIP(cfg.AdvertiseIP); ip == nil || ip.To4() == nil {
			return nil, fmt.Errorf("ADVERTISE_IP must be an IPv4 address, got %q", cfg.AdvertiseIP)
		}
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

	concurrency := getEnvDefault("MEDIA_FETCH_CONCURRENCY", "4")
	n, err := strconv.Atoi(concurrency)
	if err != nil || n <= 0 {
		return nil, fmt.Errorf("MEDIA_FETCH_CONCURRENCY must be a positive integer, got %q", concurrency)
	}
	cfg.MediaFetchConcurrency = n

	listingTTL := getEnvDefault("LISTING_CACHE_SECONDS", "30")
	secs, err := strconv.Atoi(listingTTL)
	if err != nil || secs < 0 {
		return nil, fmt.Errorf("LISTING_CACHE_SECONDS must be a non-negative integer, got %q", listingTTL)
	}
	cfg.ListingCacheTTL = time.Duration(secs) * time.Second

	timelineRefresh := getEnvDefault("TIMELINE_REFRESH_MINUTES", "15")
	mins, err := strconv.Atoi(timelineRefresh)
	if err != nil || mins < 1 {
		return nil, fmt.Errorf("TIMELINE_REFRESH_MINUTES must be a positive integer, got %q", timelineRefresh)
	}
	cfg.TimelineRefresh = time.Duration(mins) * time.Minute

	cfg.PhotoSource = strings.ToLower(getEnvDefault("PHOTO_SOURCE", "auto"))
	switch cfg.PhotoSource {
	case "auto", "original", "preview":
	default:
		return nil, fmt.Errorf(`PHOTO_SOURCE must be "auto", "original" or "preview", got %q`, os.Getenv("PHOTO_SOURCE"))
	}

	cfg.VideoSource = strings.ToLower(getEnvDefault("VIDEO_SOURCE", "original"))
	switch cfg.VideoSource {
	case "original", "transcoded":
	default:
		return nil, fmt.Errorf(`VIDEO_SOURCE must be "original" or "transcoded", got %q`, os.Getenv("VIDEO_SOURCE"))
	}

	cfg.ExtraFolders, err = parseExtraFolders(getEnvDefault("EXTRA_FOLDERS", "favorites,onthisday"))
	if err != nil {
		return nil, err
	}

	cfg.TimelineGrouping = strings.ToLower(getEnvDefault("TIMELINE_GROUPING", "none"))
	switch cfg.TimelineGrouping {
	case "none", "year", "month":
	default:
		return nil, fmt.Errorf(`TIMELINE_GROUPING must be "none", "year" or "month", got %q`, os.Getenv("TIMELINE_GROUPING"))
	}

	cfg.TitleDatePrefix = os.Getenv("TITLE_DATE_PREFIX") == "true"
	cfg.TitleDatePrefixDescending = os.Getenv("TITLE_DATE_PREFIX_DESC") == "true"
	cfg.Debug = os.Getenv("DEBUG") == "true"

	return cfg, nil
}

// validExtraFolders are the optional root folders EXTRA_FOLDERS may list.
var validExtraFolders = []string{"favorites", "onthisday", "places", "random"}

// parseExtraFolders parses EXTRA_FOLDERS: a comma-separated list of
// validExtraFolders entries, in the order they should appear, or "none".
// Duplicates are dropped; an unknown entry fails startup.
func parseExtraFolders(s string) ([]string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "none" {
		return nil, nil
	}
	var out []string
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		if f == "" || slices.Contains(out, f) {
			continue
		}
		if !slices.Contains(validExtraFolders, f) {
			return nil, fmt.Errorf("EXTRA_FOLDERS: unknown folder %q (valid: %s, or \"none\")", f, strings.Join(validExtraFolders, ", "))
		}
		out = append(out, f)
	}
	return out, nil
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
