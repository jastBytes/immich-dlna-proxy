package dlna

import (
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jastBytes/immich-dlna-proxy/config"
	"github.com/jastBytes/immich-dlna-proxy/immich"
)

// browse issues a Browse SOAP request against the given test server and
// returns the raw SOAP response body.
func browse(t *testing.T, tsURL, objectID, flag string) string {
	t.Helper()
	body := `<?xml version="1.0"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
  <s:Body>
    <u:Browse xmlns:u="urn:schemas-upnp-org:service:ContentDirectory:1">
      <ObjectID>` + objectID + `</ObjectID>
      <BrowseFlag>` + flag + `</BrowseFlag>
      <Filter>*</Filter>
      <StartingIndex>0</StartingIndex>
      <RequestedCount>0</RequestedCount>
      <SortCriteria></SortCriteria>
    </u:Browse>
  </s:Body>
</s:Envelope>`

	req, err := http.NewRequest(http.MethodPost, tsURL+"/ctl/ContentDirectory", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	req.Header.Set("SOAPACTION", `"urn:schemas-upnp-org:service:ContentDirectory:1#Browse"`)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Browse(%s, %s): unexpected status %s", objectID, flag, resp.Status)
	}

	body2, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body2)
}

// browseSorted is like browse but lets the caller set SortCriteria.
func browseSorted(t *testing.T, tsURL, objectID, flag, sortCriteria string) string {
	t.Helper()
	body := `<?xml version="1.0"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
  <s:Body>
    <u:Browse xmlns:u="urn:schemas-upnp-org:service:ContentDirectory:1">
      <ObjectID>` + objectID + `</ObjectID>
      <BrowseFlag>` + flag + `</BrowseFlag>
      <Filter>*</Filter>
      <StartingIndex>0</StartingIndex>
      <RequestedCount>0</RequestedCount>
      <SortCriteria>` + sortCriteria + `</SortCriteria>
    </u:Browse>
  </s:Body>
</s:Envelope>`

	req, err := http.NewRequest(http.MethodPost, tsURL+"/ctl/ContentDirectory", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	req.Header.Set("SOAPACTION", `"urn:schemas-upnp-org:service:ContentDirectory:1#Browse"`)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Browse(%s, %s): unexpected status %s", objectID, flag, resp.Status)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(respBody)
}

// didlResult extracts and unescapes the <Result> (DIDL-Lite) element from
// a raw Browse SOAP response.
func didlResult(t *testing.T, soapResponse string) string {
	t.Helper()
	var env struct {
		Body struct {
			BrowseResponse struct {
				Result string `xml:"Result"`
			} `xml:"BrowseResponse"`
		} `xml:"Body"`
	}
	if err := xml.Unmarshal([]byte(soapResponse), &env); err != nil {
		t.Fatalf("could not parse SOAP response: %v\nraw: %s", err, soapResponse)
	}
	return env.Body.BrowseResponse.Result
}

func newTestServerWithFakeImmich(t *testing.T) (srvURL string) {
	t.Helper()

	fakeImmich := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/albums":
			_, _ = w.Write([]byte(`[{"id":"album1","albumName":"Vacation","assetCount":1}]`))
		case "/api/albums/album1":
			_, _ = w.Write([]byte(`{"id":"album1","albumName":"Vacation","assetCount":1}`))
		case "/api/people":
			_, _ = w.Write([]byte(`{"total":2,"hidden":0,"people":[
				{"id":"person1","name":"Alice","isHidden":false},
				{"id":"person2","name":"","isHidden":false}
			]}`))
		case "/api/people/person1":
			_, _ = w.Write([]byte(`{"id":"person1","name":"Alice","isHidden":false}`))
		case "/api/assets/photo1":
			_, _ = w.Write([]byte(`{"id":"photo1","originalFileName":"beach.jpg","originalMimeType":"image/jpeg","type":"IMAGE"}`))
		case "/api/search/metadata":
			body, _ := io.ReadAll(r.Body)
			switch {
			case strings.Contains(string(body), `"albumIds"`):
				_, _ = w.Write([]byte(`{"assets":{"total":2,"count":2,"nextPage":null,
					"items":[
						{"id":"photo1","originalFileName":"beach.jpg","originalMimeType":"image/jpeg","type":"IMAGE"},
						{"id":"clip1","originalFileName":"clip.mp4","originalMimeType":"video/mp4","type":"VIDEO"}
					]}}`))
			case strings.Contains(string(body), `"personIds"`):
				_, _ = w.Write([]byte(`{"assets":{"total":1,"count":1,"nextPage":null,
					"items":[{"id":"photo2","originalFileName":"alice.jpg","originalMimeType":"image/jpeg","type":"IMAGE"}]}}`))
			default:
				http.NotFound(w, r)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(fakeImmich.Close)

	cfg := &config.Config{ImmichURL: fakeImmich.URL, APIKey: "test-key", FriendlyName: "Test Server"}
	client := immich.New(cfg.ImmichURL, cfg.APIKey)
	srv := NewServer(cfg, client, nil)

	ts := httptest.NewServer(srv.Mux())
	t.Cleanup(ts.Close)

	return ts.URL
}

func TestBrowseRootShowsAlbumsAndPeopleFolders(t *testing.T) {
	ts := newTestServerWithFakeImmich(t)

	resp := browse(t, ts, "0", "BrowseDirectChildren")
	didl := didlResult(t, resp)

	if !strings.Contains(didl, `id="albums"`) {
		t.Errorf("expected an 'albums' container at root, got: %s", didl)
	}
	if !strings.Contains(didl, `id="people"`) {
		t.Errorf("expected a 'people' container at root, got: %s", didl)
	}
	if !strings.Contains(didl, "Albums") || !strings.Contains(didl, "People") {
		t.Errorf("expected titles 'Albums' and 'People', got: %s", didl)
	}
}

func TestSearchOnRootBehavesLikeBrowseDirectChildren(t *testing.T) {
	ts := newTestServerWithFakeImmich(t)

	body := `<?xml version="1.0"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
  <s:Body>
    <u:Search xmlns:u="urn:schemas-upnp-org:service:ContentDirectory:1">
      <ContainerID>0</ContainerID>
      <SearchCriteria></SearchCriteria>
      <Filter>*</Filter>
      <StartingIndex>0</StartingIndex>
      <RequestedCount>0</RequestedCount>
      <SortCriteria></SortCriteria>
    </u:Search>
  </s:Body>
</s:Envelope>`

	req, err := http.NewRequest(http.MethodPost, ts+"/ctl/ContentDirectory", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	req.Header.Set("SOAPACTION", `"urn:schemas-upnp-org:service:ContentDirectory:1#Search"`)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Search: unexpected status %s", resp.Status)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(raw), "SearchResponse") {
		t.Errorf("expected SearchResponse, got: %s", raw)
	}
	if !strings.Contains(string(raw), `id="albums"`) || !strings.Contains(string(raw), `id="people"`) {
		t.Errorf("expected Search on root to behave like BrowseDirectChildren, got: %s", raw)
	}
}

func TestBrowseAlbumsListsAlbums(t *testing.T) {
	ts := newTestServerWithFakeImmich(t)

	didl := didlResult(t, browse(t, ts, "albums", "BrowseDirectChildren"))
	if !strings.Contains(didl, `id="album:album1"`) {
		t.Errorf("expected album1 container, got: %s", didl)
	}
	if !strings.Contains(didl, "Vacation") {
		t.Errorf("expected album title 'Vacation', got: %s", didl)
	}
}

func TestBrowsePeopleListsOnlyNamedPeople(t *testing.T) {
	ts := newTestServerWithFakeImmich(t)

	didl := didlResult(t, browse(t, ts, "people", "BrowseDirectChildren"))
	if !strings.Contains(didl, `id="person:person1"`) {
		t.Errorf("expected person1 (named 'Alice') to be listed, got: %s", didl)
	}
	if strings.Contains(didl, `id="person:person2"`) {
		t.Errorf("expected unnamed person2 to be excluded, got: %s", didl)
	}
	if !strings.Contains(didl, "Alice") {
		t.Errorf("expected person name 'Alice', got: %s", didl)
	}
}

func TestBrowsePersonListsTheirPhotos(t *testing.T) {
	ts := newTestServerWithFakeImmich(t)

	didl := didlResult(t, browse(t, ts, "person:person1", "BrowseDirectChildren"))
	if !strings.Contains(didl, `id="asset:photo2"`) {
		t.Errorf("expected photo2 item under person1, got: %s", didl)
	}
	if !strings.Contains(didl, "/media/photo2") {
		t.Errorf("expected a /media/photo2 res URL, got: %s", didl)
	}
}

func TestBrowseMetadataOnPersonReturnsItsOwnName(t *testing.T) {
	ts := newTestServerWithFakeImmich(t)

	didl := didlResult(t, browse(t, ts, "person:person1", "BrowseMetadata"))
	if !strings.Contains(didl, `id="person:person1"`) {
		t.Errorf("expected self-describing container, got: %s", didl)
	}
	if !strings.Contains(didl, "Alice") {
		t.Errorf("expected name 'Alice' in metadata, got: %s", didl)
	}
}

func TestBrowseRootMetadataReturnsSelfDescribingContainer(t *testing.T) {
	ts := newTestServerWithFakeImmich(t)

	didl := didlResult(t, browse(t, ts, "0", "BrowseMetadata"))
	if !strings.Contains(didl, `id="0"`) || !strings.Contains(didl, `parentID="-1"`) {
		t.Errorf("expected root self-describing container, got: %s", didl)
	}
}

func TestBrowseAlbumsMetadataReturnsCount(t *testing.T) {
	ts := newTestServerWithFakeImmich(t)

	didl := didlResult(t, browse(t, ts, "albums", "BrowseMetadata"))
	if !strings.Contains(didl, `id="albums"`) || !strings.Contains(didl, `childCount="1"`) {
		t.Errorf("expected albums metadata with childCount 1, got: %s", didl)
	}
}

func TestBrowsePeopleMetadataReturnsNamedCount(t *testing.T) {
	ts := newTestServerWithFakeImmich(t)

	didl := didlResult(t, browse(t, ts, "people", "BrowseMetadata"))
	if !strings.Contains(didl, `id="people"`) || !strings.Contains(didl, `childCount="1"`) {
		t.Errorf("expected people metadata counting only named people, got: %s", didl)
	}
}

func TestBrowseAlbumMetadataReturnsPhotoCount(t *testing.T) {
	ts := newTestServerWithFakeImmich(t)

	didl := didlResult(t, browse(t, ts, "album:album1", "BrowseMetadata"))
	if !strings.Contains(didl, `id="album:album1"`) || !strings.Contains(didl, "Vacation") {
		t.Errorf("expected album1 metadata, got: %s", didl)
	}
}

func TestBrowseAlbumListsVideoAlongsidePhoto(t *testing.T) {
	ts := newTestServerWithFakeImmich(t)

	didl := didlResult(t, browse(t, ts, "album:album1", "BrowseDirectChildren"))
	if !strings.Contains(didl, `id="asset:photo1"`) {
		t.Errorf("expected photo1 item, got: %s", didl)
	}
	if !strings.Contains(didl, `id="asset:clip1"`) {
		t.Errorf("expected clip1 video item, got: %s", didl)
	}
	if !strings.Contains(didl, "<upnp:class>object.item.videoItem.movie</upnp:class>") {
		t.Errorf("expected video item to use the videoItem.movie class, got: %s", didl)
	}
	if !strings.Contains(didl, "/media/clip1") {
		t.Errorf("expected clip1's <res> to point at /media/clip1, got: %s", didl)
	}
	if !strings.Contains(didl, "<upnp:albumArtURI>http://") || !strings.Contains(didl, "/thumbnail/clip1</upnp:albumArtURI>") {
		t.Errorf("expected clip1's albumArtURI to point at /thumbnail/clip1, got: %s", didl)
	}
}

func TestBrowseAssetReturnsPhotoItem(t *testing.T) {
	ts := newTestServerWithFakeImmich(t)

	didl := didlResult(t, browse(t, ts, "asset:photo1", "BrowseMetadata"))
	if !strings.Contains(didl, `id="asset:photo1"`) {
		t.Errorf("expected asset item, got: %s", didl)
	}
	if !strings.Contains(didl, "/media/photo1") {
		t.Errorf("expected a /media/photo1 res URL, got: %s", didl)
	}
	if !strings.Contains(didl, "beach.jpg") {
		t.Errorf("expected title 'beach.jpg', got: %s", didl)
	}
}

// newTestServerWithUnsortedFakeImmich is like newTestServerWithFakeImmich
// but every listing comes back in an order that isn't already
// alphabetical, so sorting behavior is actually observable.
func newTestServerWithUnsortedFakeImmich(t *testing.T) (srvURL string) {
	t.Helper()

	fakeImmich := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/albums":
			_, _ = w.Write([]byte(`[
				{"id":"album-zebra","albumName":"Zebra","assetCount":0},
				{"id":"album-apple","albumName":"Apple","assetCount":0}
			]`))
		case "/api/albums/album1":
			_, _ = w.Write([]byte(`{"id":"album1","albumName":"Mixed","assetCount":2}`))
		case "/api/people":
			_, _ = w.Write([]byte(`{"total":2,"hidden":0,"people":[
				{"id":"person-zack","name":"Zack","isHidden":false},
				{"id":"person-amy","name":"Amy","isHidden":false}
			]}`))
		case "/api/search/metadata":
			body, _ := io.ReadAll(r.Body)
			switch {
			case strings.Contains(string(body), `"albumIds"`):
				_, _ = w.Write([]byte(`{"assets":{"total":2,"count":2,"nextPage":null,
					"items":[
						{"id":"photo-zzz","originalFileName":"zzz.jpg","originalMimeType":"image/jpeg","type":"IMAGE","fileCreatedAt":"2024-06-01T00:00:00Z"},
						{"id":"photo-aaa","originalFileName":"aaa.jpg","originalMimeType":"image/jpeg","type":"IMAGE","fileCreatedAt":"2020-01-01T00:00:00Z"}
					]}}`))
			case strings.Contains(string(body), `"personIds"`):
				_, _ = w.Write([]byte(`{"assets":{"total":2,"count":2,"nextPage":null,
					"items":[
						{"id":"photo-zzz","originalFileName":"zzz.jpg","originalMimeType":"image/jpeg","type":"IMAGE","fileCreatedAt":"2024-06-01T00:00:00Z"},
						{"id":"photo-aaa","originalFileName":"aaa.jpg","originalMimeType":"image/jpeg","type":"IMAGE","fileCreatedAt":"2020-01-01T00:00:00Z"}
					]}}`))
			default:
				http.NotFound(w, r)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(fakeImmich.Close)

	cfg := &config.Config{ImmichURL: fakeImmich.URL, APIKey: "test-key", FriendlyName: "Test Server"}
	client := immich.New(cfg.ImmichURL, cfg.APIKey)
	srv := NewServer(cfg, client, nil)

	ts := httptest.NewServer(srv.Mux())
	t.Cleanup(ts.Close)

	return ts.URL
}

func TestBrowseHonorsSortCriteriaOnAlbums(t *testing.T) {
	ts := newTestServerWithUnsortedFakeImmich(t)

	ascending := didlResult(t, browseSorted(t, ts, "albums", "BrowseDirectChildren", "+dc:title"))
	if strings.Index(ascending, "Apple") > strings.Index(ascending, "Zebra") || !strings.Contains(ascending, "Apple") {
		t.Errorf("expected Apple before Zebra with +dc:title, got: %s", ascending)
	}

	descending := didlResult(t, browseSorted(t, ts, "albums", "BrowseDirectChildren", "-dc:title"))
	if strings.Index(descending, "Zebra") > strings.Index(descending, "Apple") || !strings.Contains(descending, "Zebra") {
		t.Errorf("expected Zebra before Apple with -dc:title, got: %s", descending)
	}

	unsorted := didlResult(t, browseSorted(t, ts, "albums", "BrowseDirectChildren", ""))
	if strings.Index(unsorted, "Zebra") > strings.Index(unsorted, "Apple") {
		t.Errorf("expected upstream order (Zebra, Apple) preserved without SortCriteria, got: %s", unsorted)
	}
}

func TestBrowseHonorsSortCriteriaOnPeople(t *testing.T) {
	ts := newTestServerWithUnsortedFakeImmich(t)

	ascending := didlResult(t, browseSorted(t, ts, "people", "BrowseDirectChildren", "+dc:title"))
	if strings.Index(ascending, "Amy") > strings.Index(ascending, "Zack") || !strings.Contains(ascending, "Amy") {
		t.Errorf("expected Amy before Zack with +dc:title, got: %s", ascending)
	}
}

func TestBrowseHonorsSortCriteriaOnAlbumPhotos(t *testing.T) {
	ts := newTestServerWithUnsortedFakeImmich(t)

	ascending := didlResult(t, browseSorted(t, ts, "album:album1", "BrowseDirectChildren", "+dc:title"))
	if strings.Index(ascending, "aaa.jpg") > strings.Index(ascending, "zzz.jpg") || !strings.Contains(ascending, "aaa.jpg") {
		t.Errorf("expected aaa.jpg before zzz.jpg with +dc:title, got: %s", ascending)
	}
}

func TestBrowseHonorsSortCriteriaOnPersonPhotos(t *testing.T) {
	ts := newTestServerWithUnsortedFakeImmich(t)

	ascending := didlResult(t, browseSorted(t, ts, "person:person1", "BrowseDirectChildren", "+dc:title"))
	if strings.Index(ascending, "aaa.jpg") > strings.Index(ascending, "zzz.jpg") || !strings.Contains(ascending, "aaa.jpg") {
		t.Errorf("expected aaa.jpg before zzz.jpg with +dc:title, got: %s", ascending)
	}
}

// TestBrowseHonorsSortCriteriaOnAlbumPhotosByDate exercises "-dc:date"
// (newest capture first): zzz.jpg is the newer photo (2024) despite
// sorting last alphabetically, and aaa.jpg is the older one (2020) despite
// sorting first alphabetically - so this only passes if dc:date sorting
// is actually driven by fileCreatedAt rather than accidentally matching
// the dc:title test's alphabetical order.
func TestBrowseHonorsSortCriteriaOnAlbumPhotosByDate(t *testing.T) {
	ts := newTestServerWithUnsortedFakeImmich(t)

	newestFirst := didlResult(t, browseSorted(t, ts, "album:album1", "BrowseDirectChildren", "-dc:date"))
	if strings.Index(newestFirst, "zzz.jpg") > strings.Index(newestFirst, "aaa.jpg") || !strings.Contains(newestFirst, "zzz.jpg") {
		t.Errorf("expected newer zzz.jpg before older aaa.jpg with -dc:date, got: %s", newestFirst)
	}

	oldestFirst := didlResult(t, browseSorted(t, ts, "album:album1", "BrowseDirectChildren", "+dc:date"))
	if strings.Index(oldestFirst, "aaa.jpg") > strings.Index(oldestFirst, "zzz.jpg") || !strings.Contains(oldestFirst, "aaa.jpg") {
		t.Errorf("expected older aaa.jpg before newer zzz.jpg with +dc:date, got: %s", oldestFirst)
	}
}

func TestBrowseHonorsSortCriteriaOnPersonPhotosByDate(t *testing.T) {
	ts := newTestServerWithUnsortedFakeImmich(t)

	newestFirst := didlResult(t, browseSorted(t, ts, "person:person1", "BrowseDirectChildren", "-dc:date"))
	if strings.Index(newestFirst, "zzz.jpg") > strings.Index(newestFirst, "aaa.jpg") || !strings.Contains(newestFirst, "zzz.jpg") {
		t.Errorf("expected newer zzz.jpg before older aaa.jpg with -dc:date, got: %s", newestFirst)
	}
}

func TestBrowseUnknownObjectReturns404(t *testing.T) {
	ts := newTestServerWithFakeImmich(t)

	resp := browseExpectStatus(t, ts, "not-a-real-object", "BrowseDirectChildren", http.StatusNotFound)
	if !strings.Contains(resp, "unknown object") {
		t.Errorf("expected 'unknown object' error, got: %s", resp)
	}
}

// browseExpectStatus is like browse but for requests expected to fail;
// it asserts the status code instead of requiring 200.
func browseExpectStatus(t *testing.T, tsURL, objectID, flag string, wantStatus int) string {
	t.Helper()
	body := `<?xml version="1.0"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
  <s:Body>
    <u:Browse xmlns:u="urn:schemas-upnp-org:service:ContentDirectory:1">
      <ObjectID>` + objectID + `</ObjectID>
      <BrowseFlag>` + flag + `</BrowseFlag>
      <Filter>*</Filter>
      <StartingIndex>0</StartingIndex>
      <RequestedCount>0</RequestedCount>
      <SortCriteria></SortCriteria>
    </u:Browse>
  </s:Body>
</s:Envelope>`

	req, err := http.NewRequest(http.MethodPost, tsURL+"/ctl/ContentDirectory", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	req.Header.Set("SOAPACTION", `"urn:schemas-upnp-org:service:ContentDirectory:1#Browse"`)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != wantStatus {
		t.Fatalf("Browse(%s, %s): status = %d, want %d", objectID, flag, resp.StatusCode, wantStatus)
	}
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(respBody)
}

// newTestServerWithFailingImmich builds a server whose Immich backend
// returns 500 for every request, to exercise handleBrowse's upstream-error
// branches (which all respond 502 Bad Gateway).
func newTestServerWithFailingImmich(t *testing.T) (srvURL string) {
	t.Helper()
	fakeImmich := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(fakeImmich.Close)

	cfg := &config.Config{ImmichURL: fakeImmich.URL, APIKey: "test-key", FriendlyName: "Test Server"}
	client := immich.New(cfg.ImmichURL, cfg.APIKey)
	srv := NewServer(cfg, client, nil)

	ts := httptest.NewServer(srv.Mux())
	t.Cleanup(ts.Close)

	return ts.URL
}

func TestBrowseUpstreamErrorsReturn502(t *testing.T) {
	ts := newTestServerWithFailingImmich(t)

	cases := []struct {
		name     string
		objectID string
		flag     string
	}{
		{"root direct children (ListAlbums)", "0", "BrowseDirectChildren"},
		{"albums metadata (ListAlbums)", "albums", "BrowseMetadata"},
		{"albums direct children (ListAlbums)", "albums", "BrowseDirectChildren"},
		{"people metadata (ListPeople)", "people", "BrowseMetadata"},
		{"people direct children (ListPeople)", "people", "BrowseDirectChildren"},
		{"album metadata (GetAlbum)", "album:album1", "BrowseMetadata"},
		{"album direct children (GetAlbum)", "album:album1", "BrowseDirectChildren"},
		{"person metadata (GetPerson)", "person:person1", "BrowseMetadata"},
		{"person direct children (GetPersonAssets)", "person:person1", "BrowseDirectChildren"},
		{"asset (GetAsset)", "asset:photo1", "BrowseMetadata"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := browseExpectStatus(t, ts, c.objectID, c.flag, http.StatusBadGateway)
			if !strings.Contains(resp, "upstream error") {
				t.Errorf("expected 'upstream error', got: %s", resp)
			}
		})
	}
}

func TestHandleContentDirectoryControlGetSearchCapabilities(t *testing.T) {
	srv := newTestServer(t)
	body := `<?xml version="1.0"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/">
  <s:Body><u:GetSearchCapabilities xmlns:u="urn:schemas-upnp-org:service:ContentDirectory:1"/></s:Body>
</s:Envelope>`

	code, resp := soapPost(t, srv, "/ctl/ContentDirectory", body)
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", code, resp)
	}
	if !strings.Contains(resp, "GetSearchCapabilitiesResponse") {
		t.Errorf("expected GetSearchCapabilitiesResponse, got: %s", resp)
	}
}

func TestHandleContentDirectoryControlGetSortCapabilities(t *testing.T) {
	srv := newTestServer(t)
	body := `<?xml version="1.0"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/">
  <s:Body><u:GetSortCapabilities xmlns:u="urn:schemas-upnp-org:service:ContentDirectory:1"/></s:Body>
</s:Envelope>`

	code, resp := soapPost(t, srv, "/ctl/ContentDirectory", body)
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", code, resp)
	}
	if !strings.Contains(resp, "GetSortCapabilitiesResponse") {
		t.Errorf("expected GetSortCapabilitiesResponse, got: %s", resp)
	}
	if !strings.Contains(resp, "<SortCaps>dc:title,dc:date</SortCaps>") {
		t.Errorf("expected SortCaps to advertise dc:title,dc:date, got: %s", resp)
	}
}

func TestHandleContentDirectoryControlGetSystemUpdateID(t *testing.T) {
	srv := newTestServer(t)
	body := `<?xml version="1.0"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/">
  <s:Body><u:GetSystemUpdateID xmlns:u="urn:schemas-upnp-org:service:ContentDirectory:1"/></s:Body>
</s:Envelope>`

	code, resp := soapPost(t, srv, "/ctl/ContentDirectory", body)
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", code, resp)
	}
	if !strings.Contains(resp, "<Id>1</Id>") {
		t.Errorf("expected GetSystemUpdateIDResponse with Id 1, got: %s", resp)
	}
}

func TestHandleContentDirectoryControlUnsupportedAction(t *testing.T) {
	srv := newTestServer(t)
	body := `<?xml version="1.0"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/">
  <s:Body><u:SomeUnknownAction xmlns:u="urn:schemas-upnp-org:service:ContentDirectory:1"/></s:Body>
</s:Envelope>`

	code, _ := soapPost(t, srv, "/ctl/ContentDirectory", body)
	if code != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501", code)
	}
}

func TestHandleContentDirectoryControlBadEnvelope(t *testing.T) {
	srv := newTestServer(t)
	code, _ := soapPost(t, srv, "/ctl/ContentDirectory", "not xml at all")
	if code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", code)
	}
}

func TestPage(t *testing.T) {
	items := []int{0, 1, 2, 3, 4}

	if got := page(items, 0, 0); len(got) != 5 {
		t.Errorf("RequestedCount 0 means no limit, got %v", got)
	}
	if got := page(items, 0, 2); !equalInts(got, []int{0, 1}) {
		t.Errorf("first page = %v, want [0 1]", got)
	}
	if got := page(items, 2, 2); !equalInts(got, []int{2, 3}) {
		t.Errorf("second page = %v, want [2 3]", got)
	}
	if got := page(items, 4, 2); !equalInts(got, []int{4}) {
		t.Errorf("last partial page = %v, want [4]", got)
	}
	if got := page(items, 5, 2); len(got) != 0 {
		t.Errorf("StartingIndex at end = %v, want empty", got)
	}
	if got := page(items, 10, 2); len(got) != 0 {
		t.Errorf("StartingIndex past end = %v, want empty", got)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
