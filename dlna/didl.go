package dlna

import (
	"fmt"
	"html"
	"strings"
)

// buildContainer renders one DIDL-Lite <container> element, e.g. for an
// album, a person, or a top-level "Albums"/"People" folder. Pass a
// negative childCount to omit the attribute entirely (DLNA clients treat
// a container without childCount as "browsable, count unknown" rather
// than empty) - useful when reporting an accurate count would require an
// extra API call per item. Every container carries searchable="1" and
// upnp:storageUsed, and the root ("0") additionally advertises an
// upnp:searchClass for photo items - verified against a real minidlna
// instance browsing successfully on a Samsung TV that got stuck forever
// on BrowseMetadata against our earlier, sparser responses.
//
// albumArtURI, if non-empty, is rendered as the container's cover image
// (an album's thumbnail asset, or a person's face-crop thumbnail) - the
// same reasoning as buildItem's albumArtURI: without it, DLNA
// clients that render a folder view show a generic folder icon instead
// of a cover, even though nothing about browsing the folder is broken.
// Pass "" for containers with no natural cover (the root, and the
// top-level "Albums"/"People" folders).
func buildContainer(id, parentID, title string, childCount int, albumArtURI string) string {
	childCountAttr := ""
	if childCount >= 0 {
		childCountAttr = fmt.Sprintf(` childCount="%d"`, childCount)
	}
	searchClass := ""
	if id == "0" {
		searchClass = `<upnp:searchClass includeDerived="1">object.item.imageItem</upnp:searchClass>` +
			`<upnp:searchClass includeDerived="1">object.item.videoItem</upnp:searchClass>`
	}
	albumArt := ""
	if albumArtURI != "" {
		albumArt = fmt.Sprintf(`<upnp:albumArtURI>%s</upnp:albumArtURI>`, html.EscapeString(albumArtURI))
	}
	return fmt.Sprintf(
		`<container id="%s" parentID="%s" restricted="1" searchable="1"%s>`+
			`%s`+
			`<dc:title>%s</dc:title>`+
			`%s`+
			`<upnp:class>object.container.storageFolder</upnp:class>`+
			`<upnp:storageUsed>-1</upnp:storageUsed>`+
			`</container>`,
		xmlAttrEscape(id), xmlAttrEscape(parentID), childCountAttr, searchClass, html.EscapeString(title), albumArt,
	)
}

// itemSpec describes one DIDL-Lite <item> for buildItem.
type itemSpec struct {
	ID, ParentID, Title string
	// MimeType is the MIME type of the bytes behind ResURL (defaults to
	// image/jpeg, or video/mp4 for a video).
	MimeType string
	// ResURL must be an absolute http(s) URL the client can GET (and
	// ideally range-request) to fetch the bytes.
	ResURL string
	// AlbumArtURL is what upnp:albumArtURI points at: without it, media
	// browsers like Home Assistant's list titles but show a placeholder
	// icon instead of a thumbnail (they don't fall back to <res> for
	// previews). For a photo it's typically the same as ResURL (the photo
	// is its own thumbnail); for a video it must point at a real image
	// instead, since a browser fetching albumArtURI can't decode a video
	// file as one.
	AlbumArtURL string
	IsVideo     bool
	// Converted marks bytes that aren't the original file - a photo served
	// as Immich's JPEG preview (see PHOTO_SOURCE) - which DLNA flags as
	// DLNA.ORG_CI=1.
	Converted bool
	// Date ("2006-01-02") and Size (bytes) are omitted when unknown ("" /
	// 0) rather than rendered as a bogus value - see buildAssetItem for
	// why they matter: without them, some DLNA clients (Samsung's Smart TV
	// browser is the known case) show a per-item info readout of "0
	// bytes"/"Jan 1 1970" instead of leaving those fields blank.
	Date string
	Size int64
	// Duration ("H:MM:SS.fff") and Resolution ("1920x1080") are video-only
	// res attributes, omitted when unknown; TVs use them to show a
	// video's length and a seek bar before playback starts.
	Duration, Resolution string
}

// buildItem renders one DIDL-Lite <item> element for a photo or video
// asset.
func buildItem(it itemSpec) string {
	class, mimeType := "object.item.imageItem.photo", "image/jpeg"
	if it.IsVideo {
		class, mimeType = "object.item.videoItem.movie", "video/mp4"
	}
	if it.MimeType != "" {
		mimeType = it.MimeType
	}
	dateElem := ""
	if it.Date != "" {
		dateElem = fmt.Sprintf(`<dc:date>%s</dc:date>`, html.EscapeString(it.Date))
	}
	var attrs strings.Builder
	if it.Size > 0 {
		fmt.Fprintf(&attrs, ` size="%d"`, it.Size)
	}
	if it.Duration != "" {
		fmt.Fprintf(&attrs, ` duration="%s"`, xmlAttrEscape(it.Duration))
	}
	if it.Resolution != "" {
		fmt.Fprintf(&attrs, ` resolution="%s"`, xmlAttrEscape(it.Resolution))
	}
	return fmt.Sprintf(
		`<item id="%s" parentID="%s" restricted="1">`+
			`<dc:title>%s</dc:title>`+
			`%s`+
			`<upnp:class>%s</upnp:class>`+
			`<upnp:albumArtURI>%s</upnp:albumArtURI>`+
			`<res protocolInfo="http-get:*:%s:%s"%s>%s</res>`+
			`</item>`,
		xmlAttrEscape(it.ID), xmlAttrEscape(it.ParentID), html.EscapeString(it.Title), dateElem, class,
		html.EscapeString(it.AlbumArtURL), mimeType, dlnaFeatures(it.IsVideo, it.Converted), attrs.String(), html.EscapeString(it.ResURL),
	)
}

// DLNA.ORG_FLAGS values, as minidlna sends them: for images, interactive
// + background transfer modes, connection stall allowed, DLNA 1.5; for
// video, streaming instead of interactive transfer mode.
const (
	dlnaFlagsImage = "00f00000000000000000000000000000"
	dlnaFlagsVideo = "01700000000000000000000000000000"
)

// dlnaFeatures returns the DLNA fourth field of a protocolInfo string,
// also sent as the contentFeatures.dlna.org response header on media
// requests. DLNA.ORG_OP=01 advertises byte-range seeking (every media URL
// supports Range) - several Samsung and LG TVs only show a seek bar, or
// only allow seeking at all, when it's present. DLNA.ORG_CI=1 marks
// converted content. No DLNA.ORG_PN profile is given: which JPEG profile
// applies depends on pixel dimensions (JPEG_LRG tops out at 4096x4096,
// smaller than many phone photos), and a wrong profile is worse than
// none.
func dlnaFeatures(isVideo, converted bool) string {
	ci, flags := "0", dlnaFlagsImage
	if converted {
		ci = "1"
	}
	if isVideo {
		flags = dlnaFlagsVideo
	}
	return "DLNA.ORG_OP=01;DLNA.ORG_CI=" + ci + ";DLNA.ORG_FLAGS=" + flags
}

// wrapDIDL wraps one or more container/item fragments in the DIDL-Lite
// envelope DLNA clients expect inside the SOAP Result element. No XML
// declaration is included - it's already inside the outer SOAP response's
// text content, and a nested "<?xml ...?>" there is not something any
// reference DLNA server (minidlna included) emits. xmlns:sec is Samsung's
// own extension namespace (http://www.sec.co.kr/dlna) - a packet capture
// of a real Samsung TV browsing a working minidlna instance showed it
// declared on every DIDL-Lite response even when unused, and dropping it
// is the one difference that correlated with the TV refusing to browse
// past root on our server.
func wrapDIDL(items string) string {
	var b strings.Builder
	b.WriteString(`<DIDL-Lite xmlns:dc="http://purl.org/dc/elements/1.1/" ` +
		`xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/" ` +
		`xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" ` +
		`xmlns:dlna="urn:schemas-dlna-org:metadata-1-0/" ` +
		`xmlns:sec="http://www.sec.co.kr/dlna">`)
	b.WriteString(items)
	b.WriteString(`</DIDL-Lite>`)
	return b.String()
}

func xmlAttrEscape(s string) string {
	return html.EscapeString(s)
}
