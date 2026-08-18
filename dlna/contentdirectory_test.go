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
		case "/api/search/metadata":
			body, _ := io.ReadAll(r.Body)
			switch {
			case strings.Contains(string(body), `"albumIds"`):
				_, _ = w.Write([]byte(`{"assets":{"total":1,"count":1,"nextPage":null,
					"items":[{"id":"photo1","originalFileName":"beach.jpg","originalMimeType":"image/jpeg","type":"IMAGE"}]}}`))
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
