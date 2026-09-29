package dlna

import (
	"bytes"
	"context"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
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

// UserClient pairs an Immich client with the display name of the Immich
// account it authenticates as. Name is only ever shown to DLNA clients
// when len(users) > 1 (see browseMultiUser in contentdirectory.go) - a
// single configured user browses exactly as before, with no per-user
// folder level, so Name is unused in that case.
type UserClient struct {
	Name   string
	Client *immich.Client
}

type Server struct {
	cfg   *config.Config
	users []UserClient
	cache *cache.Cache // nil if caching is disabled

	// fetchSem bounds how many /media/* and /thumbnail/* requests may be
	// downloading from Immich at once, across all configured accounts. A
	// TV rapidly scrolling through a large album can otherwise fire off
	// dozens of concurrent thumbnail requests, each a cache miss, and
	// overwhelm Immich; requests beyond the limit queue for a free slot
	// instead, up to fetchQueueTimeout.
	fetchSem chan struct{}

	// flights tracks cache keys currently being downloaded into the cache,
	// so concurrent cache misses for the same key (a TV and its preview
	// pane, or two TVs) share one Immich download instead of each
	// starting their own - see serveMedia. Only used with caching
	// enabled; without a cache there's nowhere to share the result.
	flightsMu sync.Mutex
	flights   map[string]*flight

	// listings briefly caches Immich listing responses for Browse - see
	// listingCache.
	listings *listingCache
}

// flight is one in-progress cache fill for a cache key.
type flight struct {
	// done is closed once the fill has finished, successfully or not.
	done chan struct{}
	// video is closed as soon as the key turns out to be a video, whose
	// fill then carries on in the background (see serveMedia); waiters
	// proxy their request straight through to Immich instead of waiting
	// possibly minutes for the whole file.
	video chan struct{}
}

func NewServer(cfg *config.Config, users []UserClient, c *cache.Cache) *Server {
	concurrency := cfg.MediaFetchConcurrency
	if concurrency <= 0 {
		concurrency = 4
	}
	debugLogging.Store(cfg.Debug)
	return &Server{cfg: cfg, users: users, cache: c, fetchSem: make(chan struct{}, concurrency), flights: map[string]*flight{}, listings: newListingCache(cfg.ListingCacheTTL)}
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
		w.Header().Set("Server", serverHeader())
		w.Header()["EXT"] = []string{""}
		next.ServeHTTP(w, r)
	})
}

// parseMediaPath parses the path segment after "/media/". With a single
// configured user it's a bare assetID, preserving the original
// /media/{assetID} URL shape. With multiple IMMICH_API_KEYS configured, the
// DIDL-Lite <res> URLs built in contentdirectory.go instead encode which
// configured account's API key must be used to download the asset, as
// "{userIdx}/{assetID}" - the account isn't otherwise derivable from the
// asset ID alone, since each account only has permission to download
// assets it can see. Asset IDs are UUIDs and never contain "/", so the two
// shapes never collide.
func parseMediaPath(path string, numUsers int) (userIdx int, assetID string, ok bool) {
	if path == "" {
		return 0, "", false
	}
	i := strings.IndexByte(path, '/')
	if i < 0 {
		return 0, path, true
	}
	idx, err := strconv.Atoi(path[:i])
	if err != nil || idx < 0 || idx >= numUsers {
		return 0, "", false
	}
	assetID = path[i+1:]
	if !immich.ValidID(assetID) {
		return 0, "", false
	}
	return idx, assetID, true
}

// mediaURL builds the absolute URL a DLNA client GETs to fetch a photo's
// bytes (see parseMediaPath for how handleMedia decodes it). userIdx is -1
// for the single-user case, rendering the original /media/{assetID} shape
// unchanged; otherwise it identifies which configured account owns the
// asset.
func mediaURL(baseURL string, userIdx int, assetID string) string {
	if userIdx < 0 {
		return baseURL + "/media/" + assetID
	}
	return baseURL + "/media/" + strconv.Itoa(userIdx) + "/" + assetID
}

// personThumbnailURL is mediaURL's counterpart for a person's face-crop
// thumbnail (see Server.handlePersonThumbnail).
func personThumbnailURL(baseURL string, userIdx int, personID string) string {
	if userIdx < 0 {
		return baseURL + "/media/person/" + personID
	}
	return baseURL + "/media/person/" + strconv.Itoa(userIdx) + "/" + personID
}

// thumbnailURL is mediaURL's counterpart for an asset's Immich-generated
// preview thumbnail (see Server.handleThumbnail), used as a video item's
// albumArtURI in buildAssetItem.
func thumbnailURL(baseURL string, userIdx int, assetID string) string {
	if userIdx < 0 {
		return baseURL + "/thumbnail/" + assetID
	}
	return baseURL + "/thumbnail/" + strconv.Itoa(userIdx) + "/" + assetID
}

// mediaSource describes where serveMedia gets the bytes for one cache
// key from on a cache miss.
type mediaSource struct {
	// fetch downloads the complete bytes and their MIME type.
	fetch func(ctx context.Context) (io.ReadCloser, string, error)
	// openRange, if non-nil, requests the same bytes from Immich with a
	// client's Range header forwarded as-is. Used for videos, which are
	// proxied straight through on a cache miss rather than downloaded in
	// full first - see serveMedia. Sources that can never be video
	// (thumbnails) leave it nil.
	openRange func(ctx context.Context, rangeHeader string) (*http.Response, error)
	// transform, if non-nil, is applied to photo bytes before they're
	// cached and served (EXIF-orientation fixing, MAX_RESOLUTION
	// downscaling).
	transform func([]byte) []byte
}

func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	userIdx, assetID, ok := parseMediaPath(strings.TrimPrefix(r.URL.Path, "/media/"), len(s.users))
	if !ok {
		http.NotFound(w, r)
		return
	}
	client := s.users[userIdx].Client

	// We always buffer and decode the downloaded bytes - not just when
	// resizing is configured - because normalizing EXIF orientation
	// requires it too: most DLNA renderers ignore the orientation tag and
	// show raw pixels, so a portrait photo tagged "rotate 90" needs the
	// rotation baked into the pixels themselves to display upright.
	s.serveMedia(w, r, assetID, mediaSource{
		fetch: func(ctx context.Context) (io.ReadCloser, string, error) {
			return client.DownloadOriginal(ctx, assetID)
		},
		openRange: func(ctx context.Context, rangeHeader string) (*http.Response, error) {
			return client.OpenOriginalRange(ctx, assetID, rangeHeader)
		},
		transform: func(data []byte) []byte {
			data = s.fixOrientation(assetID, data)
			return s.maybeResize(assetID, data)
		},
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
	userIdx, personID, ok := parseMediaPath(strings.TrimPrefix(r.URL.Path, "/media/person/"), len(s.users))
	if !ok {
		http.NotFound(w, r)
		return
	}
	client := s.users[userIdx].Client

	// Cached under the bare person ID, with no userIdx component: person
	// IDs, like asset IDs, are UUIDs unique across every account on the
	// same Immich server, so they can't collide between accounts either.
	s.serveMedia(w, r, "person:"+personID, mediaSource{
		fetch: func(ctx context.Context) (io.ReadCloser, string, error) {
			return client.GetPersonThumbnail(ctx, personID)
		},
	})
}

// handleThumbnail serves Immich's generated preview-sized thumbnail for
// an asset. It's used for video items' albumArtURI (see buildAssetItem) -
// a video's own bytes can't double as an image preview the way a photo's
// can. It shares handleMedia's cache/queue/dedup path, cached under
// "thumb:<id>" so it never collides with the asset's own bytes: a TV
// scrolling through a video-heavy album requests these as rapidly as it
// requests photos, so they need the same protection for Immich.
func (s *Server) handleThumbnail(w http.ResponseWriter, r *http.Request) {
	userIdx, assetID, ok := parseMediaPath(strings.TrimPrefix(r.URL.Path, "/thumbnail/"), len(s.users))
	if !ok {
		http.NotFound(w, r)
		return
	}
	client := s.users[userIdx].Client

	s.serveMedia(w, r, "thumb:"+assetID, mediaSource{
		fetch: func(ctx context.Context) (io.ReadCloser, string, error) {
			return client.GetAssetThumbnail(ctx, assetID)
		},
	})
}

// serveMedia serves the bytes identified by cacheKey, using the disk
// cache when enabled:
//
//   - Cache hit: served from disk, no Immich call, no queueing.
//   - Another request is already filling the cache for this key: wait for
//     it and serve its result (or, once it turns out to be a video, proxy
//     straight through - see below) rather than downloading it again.
//   - Otherwise: queue for a fetchSem slot and download from Immich. A
//     photo is buffered, transformed, cached and served from disk. A
//     video (when src.openRange is set) is instead proxied straight
//     through to Immich with the client's Range header, so playback and
//     seeking start immediately rather than after the whole (possibly
//     multi-GB) file has downloaded; with caching enabled, the full
//     download already started carries on in the background, detached
//     from this request, to fill the cache for next time.
func (s *Server) serveMedia(w http.ResponseWriter, r *http.Request, cacheKey string, src mediaSource) {
	var fl *flight
	if s.cache != nil {
		for {
			if s.serveFromCache(w, r, cacheKey) {
				return
			}
			var leader bool
			fl, leader = s.joinFlight(cacheKey)
			if leader {
				break
			}
			select {
			case <-fl.done:
				continue // re-check the cache; become the leader if the fill failed
			case <-fl.video:
				s.proxyRange(w, r, cacheKey, src)
				return
			case <-r.Context().Done():
				return
			}
		}
	}
	// finish ends this request's flight, unless ownership was handed off
	// to a background video fill.
	finish := func() {
		if fl != nil {
			s.finishFlight(cacheKey, fl)
			fl = nil
		}
	}
	defer func() { finish() }()

	// Queue for a free Immich-fetch slot rather than firing off another
	// concurrent download - see fetchSem's doc comment. Cache hits above
	// never reach this point, so browsing an already-cached album stays
	// unthrottled.
	select {
	case s.fetchSem <- struct{}{}:
	case <-time.After(fetchQueueTimeout):
		log.Printf("fetch(%s): timed out after %s waiting for a free Immich fetch slot", cacheKey, fetchQueueTimeout)
		http.Error(w, "server busy, try again", http.StatusServiceUnavailable)
		return
	case <-r.Context().Done():
		return
	}
	slotHeld := true
	releaseSlot := func() {
		if slotHeld {
			<-s.fetchSem
			slotHeld = false
		}
	}
	defer releaseSlot()

	// The download runs under its own context, cancelled when this
	// request ends - unless a video's cache fill detaches it below.
	fetchCtx, cancelFetch := context.WithCancel(context.WithoutCancel(r.Context()))
	stopFollowingRequest := context.AfterFunc(r.Context(), cancelFetch)
	detached := false
	defer func() {
		if !detached {
			stopFollowingRequest()
			cancelFetch()
		}
	}()

	body, mimeType, err := src.fetch(fetchCtx)
	if err != nil {
		upstreamError(w, cacheKey, err)
		return
	}

	if isVideoMimeType(mimeType) && src.openRange != nil {
		// Playback doesn't occupy a fetch slot: those exist to protect
		// Immich from bursts of thumbnail requests, not to cap how many
		// TVs can play a video at once.
		releaseSlot()
		if fl != nil && stopFollowingRequest() {
			detached = true
			close(fl.video)
			bgFlight := fl
			fl = nil // the background fill owns the flight now
			go s.fillCacheInBackground(cacheKey, mimeType, body, bgFlight, cancelFetch)
		} else {
			_ = body.Close()
		}
		s.proxyRange(w, r, cacheKey, src)
		return
	}
	defer func() { _ = body.Close() }()

	if isVideoMimeType(mimeType) {
		// A video from a source without openRange - not expected from
		// Immich, but stream it rather than buffering it into memory.
		s.serveStream(w, r, cacheKey, mimeType, body)
		return
	}

	data, err := io.ReadAll(body)
	if err != nil {
		log.Printf("fetch(%s) read failed: %v", cacheKey, err)
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}
	releaseSlot() // the rest is local CPU/disk work

	if src.transform != nil {
		data = src.transform(data)
	}

	if s.cache == nil {
		w.Header().Set("Content-Type", mimeType)
		http.ServeContent(w, r, cacheKey, time.Now(), bytes.NewReader(data))
		return
	}

	// Populate the cache with the (possibly transformed) bytes, then
	// serve it from disk - this also correctly answers any Range request
	// the client made, via http.ServeContent.
	f, err := s.cache.Put(cacheKey, mimeType, bytes.NewReader(data))
	if err != nil {
		log.Printf("cache: put(%s) failed: %v", cacheKey, err)
		http.Error(w, "cache error", http.StatusInternalServerError)
		return
	}
	finish() // waiters can serve from the cache now
	serveFile(w, r, cacheKey, mimeType, f)
}

// serveFromCache serves cacheKey from the disk cache, reporting false
// (having written nothing) on a miss.
func (s *Server) serveFromCache(w http.ResponseWriter, r *http.Request, cacheKey string) bool {
	f, mimeType, modTime, ok := s.cache.Get(cacheKey)
	if !ok {
		return false
	}
	defer func() { _ = f.Close() }()
	w.Header().Set("Content-Type", mimeType)
	http.ServeContent(w, r, cacheKey, modTime, f)
	return true
}

// serveFile serves (and closes) a file just written by cache.Put.
func serveFile(w http.ResponseWriter, r *http.Request, name, mimeType string, f *os.File) {
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		http.Error(w, "cache error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", mimeType)
	http.ServeContent(w, r, name, info.ModTime(), f)
}

// joinFlight returns the in-progress flight for cacheKey, or registers a
// new one, in which case leader is true and the caller must eventually
// call finishFlight.
func (s *Server) joinFlight(cacheKey string) (fl *flight, leader bool) {
	s.flightsMu.Lock()
	defer s.flightsMu.Unlock()
	if fl, ok := s.flights[cacheKey]; ok {
		return fl, false
	}
	fl = &flight{done: make(chan struct{}), video: make(chan struct{})}
	s.flights[cacheKey] = fl
	return fl, true
}

func (s *Server) finishFlight(cacheKey string, fl *flight) {
	s.flightsMu.Lock()
	delete(s.flights, cacheKey)
	s.flightsMu.Unlock()
	close(fl.done)
}

// fillCacheInBackground finishes downloading a video into the cache after
// the request that started it has moved on to proxying (see serveMedia).
// It's detached from that request, so a TV that stops playback early or
// only probed the first few bytes still ends up with the video cached;
// a stalled download still aborts via the client's stall timeout.
func (s *Server) fillCacheInBackground(cacheKey, mimeType string, body io.ReadCloser, fl *flight, cancel context.CancelFunc) {
	defer cancel()
	defer s.finishFlight(cacheKey, fl)
	defer func() { _ = body.Close() }()

	f, err := s.cache.Put(cacheKey, mimeType, body)
	if err != nil {
		log.Printf("cache: background fill of %s failed: %v", cacheKey, err)
		return
	}
	_ = f.Close()
	debugf("cache: background fill of %s complete", cacheKey)
}

// proxyRange serves a request for a video that isn't (yet) cached by
// proxying it straight through to Immich, forwarding the client's Range
// header and relaying the status and the headers a player needs to seek.
// Without this, playback of an uncached video could only start once the
// whole file had been downloaded - long enough for many TVs to give up.
func (s *Server) proxyRange(w http.ResponseWriter, r *http.Request, cacheKey string, src mediaSource) {
	resp, err := src.openRange(r.Context(), r.Header.Get("Range"))
	if err != nil {
		upstreamError(w, cacheKey, err)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	for _, h := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "Last-Modified"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	if w.Header().Get("Accept-Ranges") == "" {
		w.Header().Set("Accept-Ranges", "bytes")
	}
	w.WriteHeader(resp.StatusCode)
	if r.Method == http.MethodHead {
		return
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		debugf("proxy %s: %v", cacheKey, err)
	}
}

// upstreamError answers a failed Immich fetch: 404 when Immich says the
// object doesn't exist (or isn't visible to this API key), 502 otherwise.
func upstreamError(w http.ResponseWriter, cacheKey string, err error) {
	log.Printf("fetch(%s) failed: %v", cacheKey, err)
	if immich.IsNotFound(err) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	http.Error(w, "upstream error", http.StatusBadGateway)
}

// isVideoMimeType reports whether mimeType (as returned by Immich's
// Content-Type header) identifies a video asset.
func isVideoMimeType(mimeType string) bool {
	return strings.HasPrefix(mimeType, "video/")
}

// serveStream serves bytes that shouldn't be buffered into memory,
// writing them through the cache (and serving from there, so Range
// works) or, with caching disabled, copying them straight to the client.
func (s *Server) serveStream(w http.ResponseWriter, r *http.Request, cacheKey, mimeType string, body io.Reader) {
	if s.cache == nil {
		w.Header().Set("Content-Type", mimeType)
		if _, err := io.Copy(w, body); err != nil {
			log.Printf("stream %s failed: %v", cacheKey, err)
		}
		return
	}
	f, err := s.cache.Put(cacheKey, mimeType, body)
	if err != nil {
		log.Printf("cache: put(%s) failed: %v", cacheKey, err)
		http.Error(w, "cache error", http.StatusInternalServerError)
		return
	}
	serveFile(w, r, cacheKey, mimeType, f)
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
		debugf("normalized EXIF orientation for %s", assetID)
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
		debugf("resized %s: %d -> %d bytes (max %dx%d)", assetID, len(data), len(out), s.cfg.MaxWidth, s.cfg.MaxHeight)
	}
	return out
}

// NewHTTPServer returns the HTTP part of the server (description, SOAP
// control, and media streaming), ready to ListenAndServe. Run it
// alongside the SSDP responder. ReadHeaderTimeout keeps an idle or
// malicious client from holding a connection open without ever sending a
// request; there's deliberately no WriteTimeout, which would cut off
// long video streams.
func (s *Server) NewHTTPServer() *http.Server {
	return &http.Server{
		Addr:              s.cfg.ListenAddr,
		Handler:           s.Mux(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
}
