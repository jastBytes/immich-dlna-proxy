package dlna

import (
	"strings"

	"github.com/jastBytes/immich-dlna-proxy/immich"
)

// Photo source modes (config.Config.PhotoSource, PHOTO_SOURCE).
const (
	// PhotoSourceAuto serves JPEG/PNG photos as their original file and
	// everything else (HEIC, WebP, TIFF, RAW-derived formats, ...) as
	// Immich's generated JPEG preview.
	PhotoSourceAuto = "auto"
	// PhotoSourceOriginal always serves the original file, whatever its
	// format.
	PhotoSourceOriginal = "original"
	// PhotoSourcePreview always serves Immich's preview - smaller and
	// faster to load on slow TVs, at reduced resolution.
	PhotoSourcePreview = "preview"
)

// displayableImage reports whether mimeType is a photo format DLNA
// renderers can be relied on to display: JPEG (the one image format DLNA
// requires every renderer to support) and PNG. Anything else - notably
// HEIC, which iPhones shoot by default - most TVs show as a broken
// thumbnail or refuse to open.
func displayableImage(mimeType string) bool {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/jpeg", "image/jpg", "image/png":
		return true
	}
	return false
}

// servesPreview reports whether asset a is served as Immich's JPEG
// preview rather than its original file under the given PHOTO_SOURCE
// mode. Only ever true for photos: videos are always served as the
// original. An unknown MIME type counts as displayable in auto mode, so
// missing metadata keeps the original-file behavior rather than
// silently downgrading it.
func servesPreview(mode string, a immich.Asset) bool {
	if !a.IsPhoto() {
		return false
	}
	switch mode {
	case PhotoSourcePreview:
		return true
	case PhotoSourceAuto:
		return a.OriginalMimeType != "" && !displayableImage(a.OriginalMimeType)
	}
	return false
}
