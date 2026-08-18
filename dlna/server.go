package dlna

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jastBytes/immich-dlna-proxy/cache"
	"github.com/jastBytes/immich-dlna-proxy/config"
	"github.com/jastBytes/immich-dlna-proxy/imageproc"
	"github.com/jastBytes/immich-dlna-proxy/immich"
)

type Server struct {
	cfg    *config.Config
	immich *immich.Client
	cache  *cache.Cache // nil if caching is disabled
}

func NewServer(cfg *config.Config, client *immich.Client, c *cache.Cache) *Server {
	return &Server{cfg: cfg, immich: client, cache: c}
}

func (s *Server) Mux() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("/description.xml", s.handleDescription)
	mux.HandleFunc("/ContentDirectory.xml", s.handleContentDirectorySCPD)
	mux.HandleFunc("/ConnectionManager.xml", s.handleConnectionManagerSCPD)
	mux.HandleFunc("/ctl/ContentDirectory", s.handleContentDirectoryControl)
	mux.HandleFunc("/ctl/ConnectionManager", s.handleConnectionManagerControl)
	mux.HandleFunc("/X_MS_MediaReceiverRegistrar.xml", s.handleMediaReceiverRegistrarSCPD)
	mux.HandleFunc("/ctl/X_MS_MediaReceiverRegistrar", s.handleMediaReceiverRegistrarControl)
	mux.HandleFunc("/media/", s.handleMedia)

	return mux
}

func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	assetID := strings.TrimPrefix(r.URL.Path, "/media/")
	if assetID == "" {
		http.NotFound(w, r)
		return
	}

	resizeEnabled := s.cfg.MaxWidth > 0 && s.cfg.MaxHeight > 0

	if s.cache == nil {
		if !resizeEnabled {
			// Fast path: proxy bytes straight through, including Range
			// support, without buffering the whole file in memory.
			if err := s.immich.StreamOriginal(w, r, assetID); err != nil {
				log.Printf("StreamOriginal(%s) failed: %v", assetID, err)
			}
			return
		}

		// Downscaling requires decoding the whole image first, so we
		// can't stream-passthrough here - download it fully, resize,
		// then serve the result from memory (still supports Range,
		// via http.ServeContent).
		body, mimeType, err := s.immich.DownloadOriginal(assetID)
		if err != nil {
			log.Printf("DownloadOriginal(%s) failed: %v", assetID, err)
			http.Error(w, "upstream error", http.StatusBadGateway)
			return
		}
		data, err := io.ReadAll(body)
		_ = body.Close()
		if err != nil {
			log.Printf("DownloadOriginal(%s) read failed: %v", assetID, err)
			http.Error(w, "upstream error", http.StatusBadGateway)
			return
		}

		data = s.maybeResize(assetID, data)
		w.Header().Set("Content-Type", mimeType)
		http.ServeContent(w, r, assetID, time.Now(), bytes.NewReader(data))
		return
	}

	if path, mimeType, modTime, ok := s.cache.Get(assetID); ok {
		f, err := os.Open(path)
		if err != nil {
			log.Printf("cache: open(%s) failed: %v", path, err)
			http.Error(w, "cache error", http.StatusInternalServerError)
			return
		}
		defer func() { _ = f.Close() }()
		w.Header().Set("Content-Type", mimeType)
		http.ServeContent(w, r, assetID, modTime, f)
		return
	}

	// Cache miss: download the full original from Immich, downscale it
	// if configured, populate the cache with the (possibly downscaled)
	// bytes, then serve it from disk (this also correctly answers any
	// Range request the client made, via http.ServeContent).
	body, mimeType, err := s.immich.DownloadOriginal(assetID)
	if err != nil {
		log.Printf("DownloadOriginal(%s) failed: %v", assetID, err)
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}
	data, err := io.ReadAll(body)
	_ = body.Close()
	if err != nil {
		log.Printf("DownloadOriginal(%s) read failed: %v", assetID, err)
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}

	data = s.maybeResize(assetID, data)

	path, err := s.cache.Put(assetID, mimeType, bytes.NewReader(data))
	if err != nil {
		log.Printf("cache: put(%s) failed: %v", assetID, err)
		http.Error(w, "cache error", http.StatusInternalServerError)
		return
	}

	f, err := os.Open(path)
	if err != nil {
		log.Printf("cache: open(%s) failed: %v", path, err)
		http.Error(w, "cache error", http.StatusInternalServerError)
		return
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		http.Error(w, "cache error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", mimeType)
	http.ServeContent(w, r, assetID, info.ModTime(), f)
}

// maybeResize downscales data if MAX_RESOLUTION is configured and the
// image exceeds it. On any error, or for formats/sizes it doesn't need to
// touch, it returns data unchanged - resizing is a nice-to-have, never a
// reason to fail serving the photo.
func (s *Server) maybeResize(assetID string, data []byte) []byte {
	if s.cfg.MaxWidth <= 0 || s.cfg.MaxHeight <= 0 {
		return data
	}
	out, resized, err := imageproc.MaybeDownscale(data, s.cfg.MaxWidth, s.cfg.MaxHeight)
	if err != nil {
		log.Printf("resize(%s) failed, serving original: %v", assetID, err)
		return data
	}
	if resized {
		log.Printf("resized %s: %d -> %d bytes (max %dx%d)", assetID, len(data), len(out), s.cfg.MaxWidth, s.cfg.MaxHeight)
	}
	return out
}

// ListenAndServe starts the HTTP part of the server (description, SOAP
// control, and media streaming). Run this in a goroutine alongside the
// SSDP responder.
func (s *Server) ListenAndServe() error {
	log.Printf("HTTP (description/SOAP/media) listening on %s", s.cfg.ListenAddr)
	return http.ListenAndServe(s.cfg.ListenAddr, s.Mux())
}
