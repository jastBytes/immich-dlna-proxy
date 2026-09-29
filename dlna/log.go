package dlna

import (
	"log"
	"net/http"
	"strings"
	"sync/atomic"
)

// debugLogging enables per-request logging of media traffic and other
// high-volume diagnostics (config.Config.Debug, DEBUG=true). A package
// variable rather than a Server field because debugf is also called from
// helpers that have no Server at hand.
var debugLogging atomic.Bool

// debugf logs only when DEBUG=true.
func debugf(format string, args ...any) {
	if debugLogging.Load() {
		log.Printf(format, args...)
	}
}

// isHighVolumePath reports whether path is one of the byte-serving
// endpoints a TV hits once per photo/thumbnail it renders, or the health
// check a container runtime polls every 30 seconds - far too many
// requests to log individually by default.
func isHighVolumePath(path string) bool {
	return strings.HasPrefix(path, "/media/") || strings.HasPrefix(path, "/thumbnail/") || path == "/healthz"
}

// loggingMiddleware logs incoming HTTP requests, primarily to make it
// obvious whether a DLNA client got as far as fetching /description.xml or
// calling ContentDirectory Browse at all - useful for diagnosing clients
// that discover the server over SSDP but then go quiet. Those protocol
// requests are always logged; /media/, /thumbnail/ (one per photo a TV
// renders) and /healthz requests only with DEBUG=true.
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isHighVolumePath(r.URL.Path) {
			debugf("HTTP %s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)
		} else {
			log.Printf("HTTP %s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)
		}
		next.ServeHTTP(w, r)
	})
}
