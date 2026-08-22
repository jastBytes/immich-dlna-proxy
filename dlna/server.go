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

func (s *Server) Mux() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/description.xml", s.handleDescription)
	mux.HandleFunc("/ContentDirectory.xml", s.handleContentDirectorySCPD)
	mux.HandleFunc("/ConnectionManager.xml", s.handleConnectionManagerSCPD)
	mux.HandleFunc("/ctl/ContentDirectory", s.handleContentDirectoryControl)
	mux.HandleFunc("/ctl/ConnectionManager", s.handleConnectionManagerControl)
	mux.HandleFunc("/X_MS_MediaReceiverRegistrar.xml", s.handleMediaReceiverRegistrarSCPD)
	mux.HandleFunc("/ctl/X_MS_MediaReceiverRegistrar", s.handleMediaReceiverRegistrarControl)
	mux.HandleFunc("/media/", s.handleMedia)
	mux.HandleFunc("/thumbnail/", s.handleThumbnail)

	return loggingMiddleware(upnpHeadersMiddleware(mux))
}

// upnpHeadersMiddleware sets the SERVER and EXT headers UPnP/DLNA clients
// expect on every HTTP response, not just SSDP replies. Some DLNA client
// stacks treat the SERVER header's DLNADOC/1.50 token as their compliance
// check for whether to trust a device's responses at all, rather than (or
// in addition to) the same token in the device description XML.
//
// EXT is set via the header map directly (not Header.Set, which
// canonicalizes to "Ext") because a packet capture of a real Samsung TV
// showed it only ever accepts all-caps "EXT:" from a working minidlna
// instance - HTTP header names are case-insensitive per spec, but Samsung's
// embedded client stack apparently isn't.
func upnpHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "Linux UPnP/1.0 DLNADOC/1.50 immich-dlna-proxy/1.0")
		w.Header()["EXT"] = []string{""}
		next.ServeHTTP(w, r)
	})
}

// loggingMiddleware logs every incoming HTTP request, primarily to make it
// obvious whether a DLNA client got as far as fetching /description.xml or
// calling ContentDirectory Browse at all - useful for diagnosing clients
// that discover the server over SSDP but then go quiet.
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("HTTP %s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	assetID := strings.TrimPrefix(r.URL.Path, "/media/")
	if assetID == "" {
		http.NotFound(w, r)
		return
	}

	if s.cache != nil {
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
	}

	// Cache miss (or caching disabled): download the full original from
	// Immich. We always buffer and decode it - not just when resizing is
	// configured - because normalizing EXIF orientation requires it too:
	// most DLNA renderers ignore the orientation tag and show raw pixels,
	// so a portrait photo tagged "rotate 90" needs the rotation baked into
	// the pixels themselves to display upright.
	body, mimeType, err := s.immich.DownloadOriginal(assetID)
	if err != nil {
		log.Printf("DownloadOriginal(%s) failed: %v", assetID, err)
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}

	if isVideoMimeType(mimeType) {
		s.serveVideo(w, r, assetID, mimeType, body)
		return
	}

	data, err := io.ReadAll(body)
	_ = body.Close()
	if err != nil {
		log.Printf("DownloadOriginal(%s) read failed: %v", assetID, err)
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}

	data = s.fixOrientation(assetID, data)
	data = s.maybeResize(assetID, data)

	if s.cache == nil {
		w.Header().Set("Content-Type", mimeType)
		http.ServeContent(w, r, assetID, time.Now(), bytes.NewReader(data))
		return
	}

	// Populate the cache with the (possibly rotated/downscaled) bytes,
	// then serve it from disk - this also correctly answers any Range
	// request the client made, via http.ServeContent.
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

// isVideoMimeType reports whether mimeType (as returned by Immich's
// Content-Type header) identifies a video asset.
func isVideoMimeType(mimeType string) bool {
	return strings.HasPrefix(mimeType, "video/")
}

// serveVideo streams a video asset from Immich. Unlike photos, videos are
// never buffered into memory or decoded - there's no orientation/resize
// step for them, and they can be gigabytes in size, so buffering the
// whole thing first (as the photo path does, to support decode-based
// transforms) isn't practical. On a cache miss, it's written straight to
// disk via cache.Put and then served from there, so Range requests (which
// TVs rely on to seek during playback) work the same way a cache hit
// does. With caching disabled, it's copied directly to the response
// instead, which can't support Range - an accepted trade-off for that
// already-opt-out, streaming-for-debugging mode.
func (s *Server) serveVideo(w http.ResponseWriter, r *http.Request, assetID, mimeType string, body io.ReadCloser) {
	defer func() { _ = body.Close() }()

	if s.cache == nil {
		w.Header().Set("Content-Type", mimeType)
		if _, err := io.Copy(w, body); err != nil {
			log.Printf("stream video %s failed: %v", assetID, err)
		}
		return
	}

	path, err := s.cache.Put(assetID, mimeType, body)
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

// handleThumbnail proxies Immich's generated preview-sized thumbnail for
// an asset. It's used for video items' albumArtURI (see buildItem) - a
// video's own bytes can't double as an image preview the way a photo's
// can. Thumbnails are small and cheap for Immich to regenerate, so unlike
// /media, this isn't cached.
func (s *Server) handleThumbnail(w http.ResponseWriter, r *http.Request) {
	assetID := strings.TrimPrefix(r.URL.Path, "/thumbnail/")
	if assetID == "" {
		http.NotFound(w, r)
		return
	}

	body, mimeType, err := s.immich.GetAssetThumbnail(assetID)
	if err != nil {
		log.Printf("GetAssetThumbnail(%s) failed: %v", assetID, err)
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}
	defer func() { _ = body.Close() }()

	w.Header().Set("Content-Type", mimeType)
	if _, err := io.Copy(w, body); err != nil {
		log.Printf("stream thumbnail(%s) failed: %v", assetID, err)
	}
}

// fixOrientation normalizes EXIF orientation (see imageproc.FixOrientation)
// so photos display upright on renderers that ignore the tag. On any error
// it returns data unchanged - orientation correction is a nice-to-have,
// never a reason to fail serving the photo.
func (s *Server) fixOrientation(assetID string, data []byte) []byte {
	out, changed, err := imageproc.FixOrientation(data)
	if err != nil {
		log.Printf("fixOrientation(%s) failed, serving original: %v", assetID, err)
		return data
	}
	if changed {
		log.Printf("normalized EXIF orientation for %s", assetID)
	}
	return out
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
