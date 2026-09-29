package immich

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

func TestGetPersonThumbnail(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/people/p1/thumbnail" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "image/webp")
		_, _ = w.Write([]byte("face-crop-bytes"))
	}))
	defer ts.Close()

	client := New(ts.URL, "test-key")
	body, mimeType, err := client.GetPersonThumbnail(context.Background(), "p1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = body.Close() }()
	if mimeType != "image/webp" {
		t.Errorf("mimeType = %q", mimeType)
	}
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "face-crop-bytes" {
		t.Errorf("body = %q", data)
	}
}

func TestGetPersonThumbnailDefaultsMimeType(t *testing.T) {
	client := New("http://immich.local", "test-key")
	client.Stream.Transport = stubRoundTripper{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("data")),
	}}

	body, mimeType, err := client.GetPersonThumbnail(context.Background(), "p1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = body.Close() }()
	if mimeType != "image/jpeg" {
		t.Errorf("mimeType = %q, want default image/jpeg", mimeType)
	}
}

func TestGetPersonThumbnailErrorStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	client := New(ts.URL, "test-key")
	if _, _, err := client.GetPersonThumbnail(context.Background(), "missing"); err == nil {
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

func TestListTimelineAssetsUsesOrderDesc(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody struct {
			Order     string   `json:"order"`
			AlbumIds  []string `json:"albumIds"`
			PersonIds []string `json:"personIds"`
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &reqBody); err != nil {
			t.Fatalf("bad request body: %v", err)
		}
		if reqBody.Order != "desc" {
			t.Fatalf("expected order=desc, got %q", reqBody.Order)
		}
		if len(reqBody.AlbumIds) != 0 || len(reqBody.PersonIds) != 0 {
			t.Fatalf("expected no albumIds/personIds filter, got %+v", reqBody)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"assets":{"total":1,"count":1,"nextPage":null,"items":[{"id":"a1","originalFileName":"a1.jpg","type":"IMAGE"}]}}`))
	}))
	defer ts.Close()

	client := New(ts.URL, "test-key")
	assets, err := client.ListTimelineAssets()
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

func TestGetMyUser(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/users/me" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "test-key" {
			t.Errorf("x-api-key header = %q", r.Header.Get("x-api-key"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"u1","email":"alice@example.com","name":"Alice"}`))
	}))
	defer ts.Close()

	client := New(ts.URL, "test-key")
	user, err := client.GetMyUser()
	if err != nil {
		t.Fatal(err)
	}
	if user.ID != "u1" || user.Name != "Alice" || user.Email != "alice@example.com" {
		t.Fatalf("unexpected user: %+v", user)
	}
}

func TestGetMyUserErrorStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer ts.Close()

	client := New(ts.URL, "test-key")
	if _, err := client.GetMyUser(); err == nil {
		t.Fatal("expected error for non-200 status")
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
	body, mimeType, err := client.DownloadOriginal(context.Background(), "a1")
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
	body, mimeType, err := client.GetAssetThumbnail(context.Background(), "a1")
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
	if _, _, err := client.GetAssetThumbnail(context.Background(), "missing"); err == nil {
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
	client.Stream.Transport = stubRoundTripper{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("data")),
	}}

	body, mimeType, err := client.DownloadOriginal(context.Background(), "a1")
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
	if _, _, err := client.DownloadOriginal(context.Background(), "a1"); err == nil {
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

func TestValidID(t *testing.T) {
	for _, id := range []string{"4f1c2d3e-0000-4abc-9def-0123456789ab", "photo1", "ABC-def"} {
		if !ValidID(id) {
			t.Errorf("ValidID(%q) = false, want true", id)
		}
	}
	for _, id := range []string{"", "..", "../users/me", "a/b", "a?b", "a#b", "a%2Fb", "a.b", "a b", strings.Repeat("a", 65)} {
		if ValidID(id) {
			t.Errorf("ValidID(%q) = true, want false", id)
		}
	}
}

func TestIDsArePathEscaped(t *testing.T) {
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	_, _, _ = New(ts.URL, "k").DownloadOriginal(context.Background(), "../users/me?")
	if want := "/api/assets/..%2Fusers%2Fme%3F/original"; gotPath != want {
		t.Errorf("upstream path = %q, want %q", gotPath, want)
	}
}

// A download slower than apiTimeout overall, but that never stalls for
// longer than stallTimeout on a single read, must not be cut off - that's
// the whole point of not using http.Client.Timeout for Stream.
func TestDownloadSlowButProgressingIsNotCutOff(t *testing.T) {
	old := stallTimeout
	stallTimeout = 200 * time.Millisecond
	defer func() { stallTimeout = old }()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		for i := 0; i < 6; i++ {
			_, _ = w.Write([]byte("chunk"))
			w.(http.Flusher).Flush()
			time.Sleep(100 * time.Millisecond)
		}
	}))
	defer ts.Close()

	client := New(ts.URL, "k")
	client.HTTP.Timeout = 50 * time.Millisecond // must not apply to downloads
	body, _, err := client.DownloadOriginal(context.Background(), "a1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = body.Close() }()
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("slow download was cut off: %v", err)
	}
	if string(data) != strings.Repeat("chunk", 6) {
		t.Errorf("body = %q", data)
	}
}

func TestDownloadStallIsAborted(t *testing.T) {
	old := stallTimeout
	stallTimeout = 100 * time.Millisecond
	defer func() { stallTimeout = old }()

	release := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("start"))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer ts.Close()
	defer close(release)

	body, _, err := New(ts.URL, "k").DownloadOriginal(context.Background(), "a1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = body.Close() }()

	done := make(chan error, 1)
	go func() { _, err := io.ReadAll(body); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Error("stalled download returned no error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stalled download was never aborted")
	}
}

func TestDownloadAbortsWhenContextCancelled(t *testing.T) {
	release := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("start"))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer ts.Close()
	defer close(release)

	ctx, cancel := context.WithCancel(context.Background())
	body, _, err := New(ts.URL, "k").DownloadOriginal(ctx, "a1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = body.Close() }()

	done := make(chan error, 1)
	go func() { _, err := io.ReadAll(body); done <- err }()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Error("cancelled download returned no error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled download kept running")
	}
}

func TestOpenOriginalRangeForwardsRangeAndRelaysPartialContent(t *testing.T) {
	var gotRange string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRange = r.Header.Get("Range")
		w.Header().Set("Content-Type", "video/mp4")
		http.ServeContent(w, r, "v.mp4", time.Unix(0, 0), strings.NewReader("0123456789"))
	}))
	defer ts.Close()

	resp, err := New(ts.URL, "k").OpenOriginalRange(context.Background(), "a1", "bytes=3-5")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if gotRange != "bytes=3-5" {
		t.Errorf("forwarded Range = %q", gotRange)
	}
	if resp.StatusCode != http.StatusPartialContent {
		t.Errorf("status = %d, want 206", resp.StatusCode)
	}
	if b, _ := io.ReadAll(resp.Body); string(b) != "345" {
		t.Errorf("body = %q", b)
	}
}

func TestIsNotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "missing") {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()
	c := New(ts.URL, "k")

	if _, err := c.GetAlbum("missing"); !IsNotFound(err) {
		t.Errorf("GetAlbum(missing): IsNotFound(%v) = false", err)
	}
	if _, err := c.GetAlbum("broken"); err == nil || IsNotFound(err) {
		t.Errorf("GetAlbum(broken): err = %v, want a non-not-found error", err)
	}
	if _, _, err := c.DownloadOriginal(context.Background(), "missing"); !IsNotFound(err) {
		t.Errorf("DownloadOriginal(missing): IsNotFound(%v) = false", err)
	}
}

// A 403 names the API key permission the call needs, since a scoped key
// missing one otherwise just makes some photos or folders fail.
func TestForbiddenNamesMissingPermission(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer ts.Close()
	c := New(ts.URL, "k")

	cases := map[string]error{}
	_, _, cases["asset.view"] = c.GetAssetThumbnail(context.Background(), "a1")
	_, _, cases["asset.download"] = c.DownloadOriginal(context.Background(), "a1")
	_, cases["album.read"] = c.ListAlbums()
	_, cases["asset.read"] = c.ListTimelineAssets()
	_, cases["user.read"] = c.GetMyUser()
	for perm, err := range cases {
		if err == nil || !strings.Contains(err.Error(), "lacks the "+perm+" permission") {
			t.Errorf("want hint about %s, got %v", perm, err)
		}
	}

	// Other statuses carry no permission hint.
	notFound := httptest.NewServer(http.NotFoundHandler())
	defer notFound.Close()
	if _, err := New(notFound.URL, "k").ListAlbums(); err == nil || strings.Contains(err.Error(), "permission") {
		t.Errorf("404 should not mention permissions: %v", err)
	}
}
