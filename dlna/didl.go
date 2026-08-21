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
func buildContainer(id, parentID, title string, childCount int) string {
	childCountAttr := ""
	if childCount >= 0 {
		childCountAttr = fmt.Sprintf(` childCount="%d"`, childCount)
	}
	searchClass := ""
	if id == "0" {
		searchClass = `<upnp:searchClass includeDerived="1">object.item.imageItem</upnp:searchClass>` +
			`<upnp:searchClass includeDerived="1">object.item.videoItem</upnp:searchClass>`
	}
	return fmt.Sprintf(
		`<container id="%s" parentID="%s" restricted="1" searchable="1"%s>`+
			`%s`+
			`<dc:title>%s</dc:title>`+
			`<upnp:class>object.container.storageFolder</upnp:class>`+
			`<upnp:storageUsed>-1</upnp:storageUsed>`+
			`</container>`,
		xmlAttrEscape(id), xmlAttrEscape(parentID), childCountAttr, searchClass, html.EscapeString(title),
	)
}

// buildItem renders one DIDL-Lite <item> element for a photo or video
// asset. resURL must be an absolute http(s) URL the client can GET (and
// ideally range-request) to fetch the bytes. albumArtURL is what
// upnp:albumArtURI points at: without it, media browsers like Home
// Assistant's list titles but show a placeholder icon instead of a
// thumbnail (they don't fall back to <res> for previews). For a photo,
// albumArtURL is typically the same as resURL (the photo is its own
// thumbnail); for a video it must point at a real image instead, since a
// browser fetching albumArtURI can't decode a video file as one.
func buildItem(id, parentID, title, mimeType, resURL, albumArtURL string, isVideo bool) string {
	class, defaultMime := "object.item.imageItem.photo", "image/jpeg"
	if isVideo {
		class, defaultMime = "object.item.videoItem.movie", "video/mp4"
	}
	if mimeType == "" {
		mimeType = defaultMime
	}
	return fmt.Sprintf(
		`<item id="%s" parentID="%s" restricted="1">`+
			`<dc:title>%s</dc:title>`+
			`<upnp:class>%s</upnp:class>`+
			`<upnp:albumArtURI>%s</upnp:albumArtURI>`+
			`<res protocolInfo="http-get:*:%s:*">%s</res>`+
			`</item>`,
		xmlAttrEscape(id), xmlAttrEscape(parentID), html.EscapeString(title), class,
		html.EscapeString(albumArtURL), mimeType, html.EscapeString(resURL),
	)
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
