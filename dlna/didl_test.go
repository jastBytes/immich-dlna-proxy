package dlna

import (
	"strings"
	"testing"
)

func TestBuildContainerWithChildCount(t *testing.T) {
	c := buildContainer("albums", "0", "Albums", 3)
	if !strings.Contains(c, `id="albums"`) || !strings.Contains(c, `parentID="0"`) {
		t.Errorf("missing id/parentID: %s", c)
	}
	if !strings.Contains(c, `childCount="3"`) {
		t.Errorf("expected childCount attribute, got: %s", c)
	}
	if !strings.Contains(c, "<dc:title>Albums</dc:title>") {
		t.Errorf("expected title, got: %s", c)
	}
}

func TestBuildContainerOmitsNegativeChildCount(t *testing.T) {
	c := buildContainer("person:p1", "people", "Alice", -1)
	if strings.Contains(c, "childCount") {
		t.Errorf("expected childCount attribute to be omitted for negative count, got: %s", c)
	}
}

func TestBuildContainerEscapesTitle(t *testing.T) {
	c := buildContainer("id", "0", `A & <B>`, 1)
	if strings.Contains(c, `A & <B>`) {
		t.Errorf("title was not escaped: %s", c)
	}
	if !strings.Contains(c, "A &amp; &lt;B&gt;") {
		t.Errorf("expected escaped title, got: %s", c)
	}
}

func TestBuildPhotoItemDefaultsMimeType(t *testing.T) {
	item := buildPhotoItem("asset:a1", "albums", "photo.jpg", "", "http://host/media/a1")
	if !strings.Contains(item, `protocolInfo="http-get:*:image/jpeg:*"`) {
		t.Errorf("expected default mime type image/jpeg, got: %s", item)
	}
}

func TestBuildPhotoItemUsesGivenMimeType(t *testing.T) {
	item := buildPhotoItem("asset:a1", "albums", "photo.png", "image/png", "http://host/media/a1")
	if !strings.Contains(item, `protocolInfo="http-get:*:image/png:*"`) {
		t.Errorf("expected mime type image/png, got: %s", item)
	}
}

func TestBuildPhotoItemIncludesAlbumArtURI(t *testing.T) {
	item := buildPhotoItem("asset:a1", "albums", "photo.jpg", "image/jpeg", "http://host/media/a1")
	if !strings.Contains(item, "<upnp:albumArtURI>http://host/media/a1</upnp:albumArtURI>") {
		t.Errorf("expected albumArtURI matching res URL, got: %s", item)
	}
}

func TestBuildPhotoItemEscapesTitleAndURL(t *testing.T) {
	item := buildPhotoItem("asset:a1", "albums", `weird & <title>.jpg`, "image/jpeg", "http://host/media/a1?x=1&y=2")
	if strings.Contains(item, `weird & <title>.jpg`) {
		t.Errorf("title was not escaped: %s", item)
	}
	if strings.Contains(item, "media/a1?x=1&y=2</res>") {
		t.Errorf("URL ampersand was not escaped: %s", item)
	}
	if !strings.Contains(item, "media/a1?x=1&amp;y=2</res>") {
		t.Errorf("expected escaped URL, got: %s", item)
	}
}

func TestWrapDIDLIncludesNamespacesAndItems(t *testing.T) {
	didl := wrapDIDL("<item/>")
	if !strings.HasPrefix(didl, `<?xml version="1.0" encoding="UTF-8"?>`) {
		t.Errorf("expected XML declaration prefix, got: %s", didl)
	}
	for _, ns := range []string{
		`xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/"`,
		`xmlns:dc="http://purl.org/dc/elements/1.1/"`,
		`xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/"`,
	} {
		if !strings.Contains(didl, ns) {
			t.Errorf("expected namespace %q, got: %s", ns, didl)
		}
	}
	if !strings.Contains(didl, "<item/>") {
		t.Errorf("expected wrapped item content, got: %s", didl)
	}
}

func TestXMLAttrEscape(t *testing.T) {
	cases := map[string]string{
		`a & b`:  "a &amp; b",
		`"quote"`: "&#34;quote&#34;",
		`<tag>`:  "&lt;tag&gt;",
		"plain":  "plain",
	}
	for in, want := range cases {
		if got := xmlAttrEscape(in); got != want {
			t.Errorf("xmlAttrEscape(%q) = %q, want %q", in, got, want)
		}
	}
}
