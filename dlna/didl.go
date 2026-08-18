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
// extra API call per item.
func buildContainer(id, parentID, title string, childCount int) string {
	if childCount < 0 {
		return fmt.Sprintf(
			`<container id="%s" parentID="%s" restricted="1">`+
				`<dc:title>%s</dc:title>`+
				`<upnp:class>object.container.storageFolder</upnp:class>`+
				`</container>`,
			xmlAttrEscape(id), xmlAttrEscape(parentID), html.EscapeString(title),
		)
	}
	return fmt.Sprintf(
		`<container id="%s" parentID="%s" restricted="1" childCount="%d">`+
			`<dc:title>%s</dc:title>`+
			`<upnp:class>object.container.storageFolder</upnp:class>`+
			`</container>`,
		xmlAttrEscape(id), xmlAttrEscape(parentID), childCount, html.EscapeString(title),
	)
}

// buildPhotoItem renders one DIDL-Lite <item> element for a photo asset.
// resURL must be an absolute http(s) URL the client can GET (and ideally
// range-request) to fetch the bytes.
func buildPhotoItem(id, parentID, title, mimeType, resURL string) string {
	if mimeType == "" {
		mimeType = "image/jpeg"
	}
	return fmt.Sprintf(
		`<item id="%s" parentID="%s" restricted="1">`+
			`<dc:title>%s</dc:title>`+
			`<upnp:class>object.item.imageItem.photo</upnp:class>`+
			`<res protocolInfo="http-get:*:%s:*">%s</res>`+
			`</item>`,
		xmlAttrEscape(id), xmlAttrEscape(parentID), html.EscapeString(title),
		mimeType, html.EscapeString(resURL),
	)
}

// wrapDIDL wraps one or more container/item fragments in the DIDL-Lite
// envelope DLNA clients expect inside the SOAP Result element.
func wrapDIDL(items string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	b.WriteString(`<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" ` +
		`xmlns:dc="http://purl.org/dc/elements/1.1/" ` +
		`xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/">`)
	b.WriteString(items)
	b.WriteString(`</DIDL-Lite>`)
	return b.String()
}

func xmlAttrEscape(s string) string {
	return html.EscapeString(s)
}
