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

// fetchQueueTimeout bounds how long a /media/* request will wait for a
// free Immich-fetch slot (see Server.fetchSem) before giving up. Matches
// immich.Client's own HTTP timeout, so a request that does get a slot
// still can't hang indefinitely. A var rather than a const so tests can
// shrink it instead of waiting out the real 30s.
var fetchQueueTimeout = 30 * time.Second

type Server struct {
	cfg    *config.Config
	immich *immich.Client
	cache  *cache.Cache // nil if caching is disabled

	// fetchSem bounds how many /media/* requests may be downloading from
	// Immich at once. A TV rapidly scrolling through a large album can
	// otherwise fire off dozens of concurrent thumbnail requests, each a
	// cache miss, and overwhelm Immich; requests beyond the limit queue
	// for a free slot instead, up to fetchQueueTimeout.
	fetchSem chan struct{}
}

func NewServer(cfg *config.Config, client *immich.Client, c *cache.Cache) *Server {
	concurrency := cfg.MediaFetchConcurrency
	if concurrency <= 0 {
		concurrency = 4
	}
	return &Server{cfg: cfg, immich: client, cache: c, fetchSem: make(chan struct{}, concurrency)}
}

func (s *Server) Mux() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/description.xml", s.handleDescription)
	mux.HandleFunc("/icon48.png", s.handleIcon48)
	mux.HandleFunc("/icon120.png", s.handleIcon120)
	mux.HandleFunc("/ContentDirectory.xml", s.handleContentDirectorySCPD)
	mux.HandleFunc("/ConnectionManager.xml", s.handleConnectionManagerSCPD)
	mux.HandleFunc("/ctl/ContentDirectory", s.handleContentDirectoryControl)
	mux.HandleFunc("/ctl/ConnectionManager", s.handleConnectionManagerControl)
	mux.HandleFunc("/X_MS_MediaReceiverRegistrar.xml", s.handleMediaReceiverRegistrarSCPD)
	mux.HandleFunc("/ctl/X_MS_MediaReceiverRegistrar", s.handleMediaReceiverRegistrarControl)
	mux.HandleFunc("/media/", s.handleMedia)
	mux.HandleFunc("/media/person/", s.handlePersonThumbnail)

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

	// We always buffer and decode the downloaded bytes - not just when
	// resizing is configured - because normalizing EXIF orientation
	// requires it too: most DLNA renderers ignore the orientation tag and
	// show raw pixels, so a portrait photo tagged "rotate 90" needs the
	// rotation baked into the pixels themselves to display upright.
	s.serveMedia(w, r, assetID,
		func() (io.ReadCloser, string, error) { return s.immich.DownloadOriginal(assetID) },
		func(data []byte) []byte {
			data = s.fixOrientation(assetID, data)
			return s.maybeResize(assetID, data)
		})
}

// handlePersonThumbnail serves a person's face-crop thumbnail, used as
// the albumArtURI for their "People" container so DLNA clients that
// render folder covers show a face instead of a generic folder icon.
// Immich serves this as image bytes directly rather than via an asset
// ID, so it goes through GetPersonThumbnail rather than DownloadOriginal
// - but otherwise shares the same disk-cache-backed serving path as
// handleMedia. It's cached under "person:<id>" so it can never collide
// with an actual asset ID, and skips fixOrientation/maybeResize: Immich
// already generates it as a small, correctly-oriented face crop.
func (s *Server) handlePersonThumbnail(w http.ResponseWriter, r *http.Request) {
	personID := strings.TrimPrefix(r.URL.Path, "/media/person/")
	if personID == "" {
		http.NotFound(w, r)
		return
	}

	s.serveMedia(w, r, "person:"+personID,
		func() (io.ReadCloser, string, error) { return s.immich.GetPersonThumbnail(personID) },
		nil)
}

// serveMedia serves image bytes identified by cacheKey, using the disk
// cache when enabled. On a cache miss, fetch downloads the original bytes
// from Immich; transform (may be nil) is applied before the bytes are
// cached and served - handleMedia uses it for EXIF-orientation fixing and
// MAX_RESOLUTION downscaling, which don't apply to person thumbnails.
func (s *Server) serveMedia(w http.ResponseWriter, r *http.Request, cacheKey string, fetch func() (io.ReadCloser, string, error), transform func([]byte) []byte) {
	if s.cache != nil {
		if path, mimeType, modTime, ok := s.cache.Get(cacheKey); ok {
			f, err := os.Open(path)
			if err != nil {
				log.Printf("cache: open(%s) failed: %v", path, err)
				http.Error(w, "cache error", http.StatusInternalServerError)
				return
			}
			defer func() { _ = f.Close() }()
			w.Header().Set("Content-Type", mimeType)
			http.ServeContent(w, r, cacheKey, modTime, f)
			return
		}
	}

	// Queue for a free Immich-fetch slot rather than firing off another
	// concurrent download - see fetchSem's doc comment. Cache hits above
	// never reach this point, so browsing an already-cached album stays
	// unthrottled.
	select {
	case s.fetchSem <- struct{}{}:
		defer func() { <-s.fetchSem }()
	case <-time.After(fetchQueueTimeout):
		log.Printf("fetch(%s): timed out after %s waiting for a free Immich fetch slot", cacheKey, fetchQueueTimeout)
		http.Error(w, "server busy, try again", http.StatusServiceUnavailable)
		return
	case <-r.Context().Done():
		return
	}

	body, mimeType, err := fetch()
	if err != nil {
		log.Printf("fetch(%s) failed: %v", cacheKey, err)
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}
	data, err := io.ReadAll(body)
	_ = body.Close()
	if err != nil {
		log.Printf("fetch(%s) read failed: %v", cacheKey, err)
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}

	if transform != nil {
		data = transform(data)
	}

	if s.cache == nil {
		w.Header().Set("Content-Type", mimeType)
		http.ServeContent(w, r, cacheKey, time.Now(), bytes.NewReader(data))
		return
	}

	// Populate the cache with the (possibly transformed) bytes, then
	// serve it from disk - this also correctly answers any Range request
	// the client made, via http.ServeContent.
	path, err := s.cache.Put(cacheKey, mimeType, bytes.NewReader(data))
	if err != nil {
		log.Printf("cache: put(%s) failed: %v", cacheKey, err)
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
	http.ServeContent(w, r, cacheKey, info.ModTime(), f)
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
