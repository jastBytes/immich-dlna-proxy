package immich

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewTrimsTrailingSlash(t *testing.T) {
	c := New("http://immich.local/", "key")
	if c.BaseURL != "http://immich.local" {
		t.Errorf("BaseURL = %q, want no trailing slash", c.BaseURL)
	}
}

func TestListAlbums(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/albums" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "test-key" {
			t.Errorf("x-api-key header = %q", r.Header.Get("x-api-key"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"a1","albumName":"Trip","assetCount":3}]`))
	}))
	defer ts.Close()

	client := New(ts.URL, "test-key")
	albums, err := client.ListAlbums()
	if err != nil {
		t.Fatal(err)
	}
	if len(albums) != 1 || albums[0].ID != "a1" || albums[0].AlbumName != "Trip" || albums[0].AssetCount != 3 {
		t.Fatalf("unexpected albums: %+v", albums)
	}
}

func TestListAlbumsErrorStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer ts.Close()

	client := New(ts.URL, "test-key")
	if _, err := client.ListAlbums(); err == nil {
		t.Fatal("expected error for non-200 status")
	}
}

func TestListAlbumsBadJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`not json`))
	}))
	defer ts.Close()

	client := New(ts.URL, "test-key")
	if _, err := client.ListAlbums(); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestGetAlbum(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/albums/a1" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"a1","albumName":"Trip","assetCount":3}`))
	}))
	defer ts.Close()

	client := New(ts.URL, "test-key")
	album, err := client.GetAlbum("a1")
	if err != nil {
		t.Fatal(err)
	}
	if album.ID != "a1" || album.AlbumName != "Trip" {
		t.Fatalf("unexpected album: %+v", album)
	}
}

func TestGetAlbumErrorStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	client := New(ts.URL, "test-key")
	if _, err := client.GetAlbum("missing"); err == nil {
		t.Fatal("expected error for non-200 status")
	}
}

func TestListPeopleFiltersNothingItselfButDecodesAll(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/people" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"people":[{"id":"p1","name":"Alice","isHidden":false},{"id":"p2","name":"","isHidden":false}],"total":2,"hidden":0}`))
	}))
	defer ts.Close()

	client := New(ts.URL, "test-key")
	people, err := client.ListPeople()
	if err != nil {
		t.Fatal(err)
	}
	if len(people) != 2 || people[0].Name != "Alice" || people[1].Name != "" {
		t.Fatalf("unexpected people: %+v", people)
	}
}

func TestListPeopleErrorStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	client := New(ts.URL, "test-key")
	if _, err := client.ListPeople(); err == nil {
		t.Fatal("expected error for non-200 status")
	}
}

func TestGetPerson(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/people/p1" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"p1","name":"Alice","isHidden":false,"thumbnailPath":"/thumb.jpg"}`))
	}))
	defer ts.Close()

	client := New(ts.URL, "test-key")
	person, err := client.GetPerson("p1")
	if err != nil {
		t.Fatal(err)
	}
	if person.ID != "p1" || person.Name != "Alice" {
		t.Fatalf("unexpected person: %+v", person)
	}
}

func TestGetPersonErrorStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	client := New(ts.URL, "test-key")
	if _, err := client.GetPerson("missing"); err == nil {
		t.Fatal("expected error for non-200 status")
	}
}

func TestGetPersonAssetsUsesPersonIdsFilter(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody struct {
			PersonIds []string `json:"personIds"`
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &reqBody); err != nil {
			t.Fatalf("bad request body: %v", err)
		}
		if len(reqBody.PersonIds) != 1 || reqBody.PersonIds[0] != "p1" {
			t.Fatalf("unexpected personIds: %v", reqBody.PersonIds)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"assets":{"total":1,"count":1,"nextPage":null,"items":[{"id":"a1","originalFileName":"a1.jpg","type":"IMAGE"}]}}`))
	}))
	defer ts.Close()

	client := New(ts.URL, "test-key")
	assets, err := client.GetPersonAssets("p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 1 || assets[0].ID != "a1" {
		t.Fatalf("unexpected assets: %+v", assets)
	}
}

func TestSearchMetadataAssetsErrorStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	client := New(ts.URL, "test-key")
	if _, err := client.GetAlbumAssets("album1"); err == nil {
		t.Fatal("expected error for non-200 status")
	}
}

func TestSearchMetadataAssetsStopsOnUnparseableNextPage(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"assets":{"total":1,"count":1,"nextPage":"not-a-number",
			"items":[{"id":"a1","originalFileName":"a1.jpg","type":"IMAGE"}]}}`))
	}))
	defer ts.Close()

	client := New(ts.URL, "test-key")
	assets, err := client.GetAlbumAssets("album1")
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 1 || assets[0].ID != "a1" {
		t.Fatalf("unexpected assets: %+v", assets)
	}
}

func TestGetAsset(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/assets/a1" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"a1","originalFileName":"a1.jpg","originalMimeType":"image/jpeg","type":"IMAGE"}`))
	}))
	defer ts.Close()

	client := New(ts.URL, "test-key")
	asset, err := client.GetAsset("a1")
	if err != nil {
		t.Fatal(err)
	}
	if asset.ID != "a1" || asset.OriginalMimeType != "image/jpeg" {
		t.Fatalf("unexpected asset: %+v", asset)
	}
}

func TestGetAssetErrorStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	client := New(ts.URL, "test-key")
	if _, err := client.GetAsset("missing"); err == nil {
		t.Fatal("expected error for non-200 status")
	}
}

func TestDownloadOriginal(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/assets/a1/original" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("binarydata"))
	}))
	defer ts.Close()

	client := New(ts.URL, "test-key")
	body, mimeType, err := client.DownloadOriginal("a1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = body.Close() }()
	if mimeType != "image/png" {
		t.Errorf("mimeType = %q", mimeType)
	}
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "binarydata" {
		t.Errorf("body = %q", data)
	}
}

func TestGetAssetThumbnail(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/assets/a1/thumbnail" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.URL.Query().Get("size") != "preview" {
			t.Errorf("size query = %q, want preview", r.URL.Query().Get("size"))
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("thumbbytes"))
	}))
	defer ts.Close()

	client := New(ts.URL, "test-key")
	body, mimeType, err := client.GetAssetThumbnail("a1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = body.Close() }()
	if mimeType != "image/jpeg" {
		t.Errorf("mimeType = %q", mimeType)
	}
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "thumbbytes" {
		t.Errorf("body = %q", data)
	}
}

func TestGetAssetThumbnailErrorStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	client := New(ts.URL, "test-key")
	if _, _, err := client.GetAssetThumbnail("missing"); err == nil {
		t.Fatal("expected error for non-200 status")
	}
}

// stubRoundTripper returns a fixed response without going over the wire,
// which lets us produce a response with no Content-Type header at all -
// something a real net/http server won't do, since it auto-sniffs one.
type stubRoundTripper struct{ resp *http.Response }

func (s stubRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return s.resp, nil
}

func TestDownloadOriginalDefaultsMimeType(t *testing.T) {
	client := New("http://immich.local", "test-key")
	client.HTTP.Transport = stubRoundTripper{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("data")),
	}}

	body, mimeType, err := client.DownloadOriginal("a1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = body.Close() }()
	if mimeType != "image/jpeg" {
		t.Errorf("mimeType = %q, want default image/jpeg", mimeType)
	}
}

func TestDownloadOriginalErrorStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer ts.Close()

	client := New(ts.URL, "test-key")
	if _, _, err := client.DownloadOriginal("a1"); err == nil {
		t.Fatal("expected error for non-200 status")
	}
}

// TestSearchMetadataAssetsFollowsPagination verifies GetAlbumAssets collects
// items across multiple pages by following the "nextPage" cursor.
func TestSearchMetadataAssetsFollowsPagination(t *testing.T) {
	var pagesSeen []int

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody struct {
			AlbumIds []string `json:"albumIds"`
			Page     int      `json:"page"`
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &reqBody); err != nil {
			t.Fatalf("bad request body: %v", err)
		}
		if reqBody.Page == 0 {
			reqBody.Page = 1
		}
		pagesSeen = append(pagesSeen, reqBody.Page)

		w.Header().Set("Content-Type", "application/json")
		switch reqBody.Page {
		case 1:
			_, _ = w.Write([]byte(`{"assets":{"total":2,"count":1,"nextPage":"2",
				"items":[{"id":"a1","originalFileName":"a1.jpg","type":"IMAGE"}]}}`))
		case 2:
			_, _ = w.Write([]byte(`{"assets":{"total":2,"count":1,"nextPage":null,
				"items":[{"id":"a2","originalFileName":"a2.jpg","type":"IMAGE"}]}}`))
		default:
			t.Fatalf("unexpected page %d", reqBody.Page)
		}
	}))
	defer ts.Close()

	client := New(ts.URL, "test-key")
	assets, err := client.GetAlbumAssets("album1")
	if err != nil {
		t.Fatal(err)
	}

	if len(assets) != 2 || assets[0].ID != "a1" || assets[1].ID != "a2" {
		t.Fatalf("expected [a1 a2], got %+v", assets)
	}
	if len(pagesSeen) != 2 || pagesSeen[0] != 1 || pagesSeen[1] != 2 {
		t.Fatalf("expected to fetch pages [1 2], got %v", pagesSeen)
	}
}
