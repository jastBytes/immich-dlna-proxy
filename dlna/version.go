package dlna

// Version is this build's version (e.g. "v0.2.0"), set by main at
// startup. It's reported to DLNA clients in SERVER headers and as the
// device description's modelNumber, so a TV's "server info" screen - and
// a bug report quoting a packet capture - shows which release is running.
var Version = "dev"

// serverHeader is the SERVER header value for HTTP responses. Some DLNA
// client stacks treat its DLNADOC/1.50 token as their compliance check
// for whether to trust a device's responses at all (see
// upnpHeadersMiddleware).
func serverHeader() string {
	return "Linux UPnP/1.0 DLNADOC/1.50 immich-dlna-proxy/" + Version
}

// ssdpServerHeader is the SERVER header value for SSDP replies and
// announcements.
func ssdpServerHeader() string {
	return "Linux UPnP/1.0 immich-dlna-proxy/" + Version
}
