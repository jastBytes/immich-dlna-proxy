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

func TestBuildContainerRootIncludesSearchClass(t *testing.T) {
	c := buildContainer("0", "-1", "Immich Photos", 2)
	if !strings.Contains(c, `<upnp:searchClass includeDerived="1">object.item.imageItem</upnp:searchClass>`) {
		t.Errorf("expected root image searchClass, got: %s", c)
	}
	if !strings.Contains(c, `<upnp:searchClass includeDerived="1">object.item.videoItem</upnp:searchClass>`) {
		t.Errorf("expected root video searchClass, got: %s", c)
	}
}

func TestBuildContainerNonRootOmitsSearchClass(t *testing.T) {
	c := buildContainer("albums", "0", "Albums", 3)
	if strings.Contains(c, "searchClass") {
		t.Errorf("expected non-root container to omit searchClass, got: %s", c)
	}
}

func TestBuildContainerIncludesSearchableAndStorageUsed(t *testing.T) {
	c := buildContainer("albums", "0", "Albums", 3)
	if !strings.Contains(c, `searchable="1"`) {
		t.Errorf("expected searchable attribute, got: %s", c)
	}
	if !strings.Contains(c, "<upnp:storageUsed>-1</upnp:storageUsed>") {
		t.Errorf("expected storageUsed element, got: %s", c)
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

func TestBuildItemDefaultsMimeType(t *testing.T) {
	item := buildItem("asset:a1", "albums", "photo.jpg", "", "http://host/media/a1", "http://host/media/a1", false)
	if !strings.Contains(item, `protocolInfo="http-get:*:image/jpeg:*"`) {
		t.Errorf("expected default mime type image/jpeg, got: %s", item)
	}
}

func TestBuildItemUsesGivenMimeType(t *testing.T) {
	item := buildItem("asset:a1", "albums", "photo.png", "image/png", "http://host/media/a1", "http://host/media/a1", false)
	if !strings.Contains(item, `protocolInfo="http-get:*:image/png:*"`) {
		t.Errorf("expected mime type image/png, got: %s", item)
	}
}

func TestBuildItemIncludesAlbumArtURI(t *testing.T) {
	item := buildItem("asset:a1", "albums", "photo.jpg", "image/jpeg", "http://host/media/a1", "http://host/media/a1", false)
	if !strings.Contains(item, "<upnp:albumArtURI>http://host/media/a1</upnp:albumArtURI>") {
		t.Errorf("expected albumArtURI matching res URL, got: %s", item)
	}
}

func TestBuildItemEscapesTitleAndURL(t *testing.T) {
	item := buildItem("asset:a1", "albums", `weird & <title>.jpg`, "image/jpeg", "http://host/media/a1?x=1&y=2", "http://host/media/a1?x=1&y=2", false)
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

func TestBuildItemUsesVideoClassAndDefaultMimeType(t *testing.T) {
	item := buildItem("asset:v1", "albums", "clip.mp4", "", "http://host/media/v1", "http://host/thumbnail/v1", true)
	if !strings.Contains(item, "<upnp:class>object.item.videoItem.movie</upnp:class>") {
		t.Errorf("expected videoItem.movie class, got: %s", item)
	}
	if !strings.Contains(item, `protocolInfo="http-get:*:video/mp4:*"`) {
		t.Errorf("expected default mime type video/mp4, got: %s", item)
	}
	if !strings.Contains(item, "<upnp:albumArtURI>http://host/thumbnail/v1</upnp:albumArtURI>") {
		t.Errorf("expected albumArtURI pointing at the thumbnail endpoint, got: %s", item)
	}
	if !strings.Contains(item, `<res protocolInfo="http-get:*:video/mp4:*">http://host/media/v1</res>`) {
		t.Errorf("expected res URL pointing at /media, got: %s", item)
	}
}

func TestBuildItemPhotoUsesPhotoClass(t *testing.T) {
	item := buildItem("asset:a1", "albums", "photo.jpg", "image/jpeg", "http://host/media/a1", "http://host/media/a1", false)
	if !strings.Contains(item, "<upnp:class>object.item.imageItem.photo</upnp:class>") {
		t.Errorf("expected imageItem.photo class, got: %s", item)
	}
}

func TestWrapDIDLIncludesNamespacesAndItems(t *testing.T) {
	didl := wrapDIDL("<item/>")
	if !strings.HasPrefix(didl, `<DIDL-Lite`) {
		t.Errorf("expected no nested XML declaration before DIDL-Lite, got: %s", didl)
	}
	for _, ns := range []string{
		`xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/"`,
		`xmlns:dc="http://purl.org/dc/elements/1.1/"`,
		`xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/"`,
		`xmlns:dlna="urn:schemas-dlna-org:metadata-1-0/"`,
		`xmlns:sec="http://www.sec.co.kr/dlna"`,
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
		`a & b`:   "a &amp; b",
		`"quote"`: "&#34;quote&#34;",
		`<tag>`:   "&lt;tag&gt;",
		"plain":   "plain",
	}
	for in, want := range cases {
		if got := xmlAttrEscape(in); got != want {
			t.Errorf("xmlAttrEscape(%q) = %q, want %q", in, got, want)
		}
	}
}
