package dlna

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jastBytes/immich-dlna-proxy/config"
	"github.com/jastBytes/immich-dlna-proxy/immich"
)

// extraFoldersTimeline is the fake library behind the extra-folder tests.
const extraFoldersTimeline = `[
	{"id":"a1","originalFileName":"xmas23.jpg","originalMimeType":"image/jpeg","type":"IMAGE","fileCreatedAt":"2023-12-24T18:00:00.000Z","exifInfo":{"country":"Germany","city":"Berlin"}},
	{"id":"a2","originalFileName":"xmas20.jpg","originalMimeType":"image/jpeg","type":"IMAGE","fileCreatedAt":"2020-12-24T09:00:00.000Z","exifInfo":{"country":"Germany","city":"Hamburg"}},
	{"id":"a3","originalFileName":"summer.jpg","originalMimeType":"image/jpeg","type":"IMAGE","fileCreatedAt":"2022-07-04T09:00:00.000Z","exifInfo":{"country":"France","city":null}},
	{"id":"a4","originalFileName":"today.jpg","originalMimeType":"image/jpeg","type":"IMAGE","fileCreatedAt":"2024-12-24T08:00:00.000Z","exifInfo":{"country":"Germany","city":"Berlin"}},
	{"id":"a5","originalFileName":"nogps.jpg","originalMimeType":"image/jpeg","type":"IMAGE","fileCreatedAt":"2021-01-01T08:00:00.000Z"},
	{"id":"a6","originalFileName":"xmas22-after-midnight.jpg","originalMimeType":"image/jpeg","type":"IMAGE","fileCreatedAt":"2022-12-23T23:30:00.000Z","localDateTime":"2022-12-24T00:30:00.000Z"},
	{"id":"a7","originalFileName":"boxing-day21.jpg","originalMimeType":"image/jpeg","type":"IMAGE","fileCreatedAt":"2021-12-24T23:30:00.000Z","localDateTime":"2021-12-25T00:30:00.000Z"}
]`

// extraFoldersImmich fakes the parts of Immich's search API the extra
// folders use - favorites, capture-date ranges and the oldest-asset lookup
// are filtered like the real server does - and counts unfiltered (whole
// library) searches.
type extraFoldersImmich struct {
	fullListings atomic.Int32
}

func (f *extraFoldersImmich) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/albums":
		_, _ = w.Write([]byte(`[]`))
	case "/api/people":
		_, _ = w.Write([]byte(`{"people":[]}`))
	case "/api/search/metadata":
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		var all []map[string]any
		_ = json.Unmarshal([]byte(extraFoldersTimeline), &all)
		var items []map[string]any
		switch {
		case req["isFavorite"] == true:
			items = []map[string]any{{"id": "a3", "originalFileName": "summer.jpg", "originalMimeType": "image/jpeg", "type": "IMAGE"}}
		case req["order"] == "asc" && req["size"] == float64(1):
			oldest := all[0]
			for _, a := range all {
				if a["fileCreatedAt"].(string) < oldest["fileCreatedAt"].(string) {
					oldest = a
				}
			}
			items = []map[string]any{oldest}
		case req["takenAfter"] != nil:
			after, _ := time.Parse(time.RFC3339, req["takenAfter"].(string))
			before, _ := time.Parse(time.RFC3339, req["takenBefore"].(string))
			for _, a := range all {
				t, _ := time.Parse(time.RFC3339, a["fileCreatedAt"].(string))
				if !t.Before(after) && t.Before(before) {
					items = append(items, a)
				}
			}
		default:
			f.fullListings.Add(1)
			items = all
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"assets": map[string]any{"nextPage": nil, "items": items}})
	default:
		http.NotFound(w, r)
	}
}

func newExtraFoldersServer(t *testing.T, folders ...string) string {
	t.Helper()
	url, _ := newExtraFoldersServerWithFake(t, folders...)
	return url
}

func newExtraFoldersServerWithFake(t *testing.T, folders ...string) (string, *extraFoldersImmich) {
	t.Helper()
	fake := &extraFoldersImmich{}
	fakeImmich := httptest.NewServer(fake)
	t.Cleanup(fakeImmich.Close)
	cfg := &config.Config{ImmichURL: fakeImmich.URL, APIKeys: []string{"k"}, FriendlyName: "Test", ExtraFolders: folders}
	ts := httptest.NewServer(NewServer(cfg, []UserClient{{Client: immich.New(fakeImmich.URL, "k")}}, nil).Mux())
	t.Cleanup(ts.Close)
	return ts.URL, fake
}

func TestRootListsConfiguredExtraFolders(t *testing.T) {
	ts := newExtraFoldersServer(t, folderFavorites, folderPlaces)
	root := didlResult(t, browse(t, ts, "0", "BrowseDirectChildren"))
	assertOrder(t, root, `id="albums"`, `id="people"`, `id="timeline"`,
		`id="favorites"`, `<dc:title>Favorites</dc:title>`, `id="places"`, `<dc:title>Places</dc:title>`)
	if strings.Contains(root, `id="random"`) || strings.Contains(root, `id="onthisday"`) {
		t.Errorf("unconfigured folders listed: %s", root)
	}
	if meta := didlResult(t, browse(t, ts, "0", "BrowseMetadata")); !strings.Contains(meta, `childCount="5"`) {
		t.Errorf("root childCount should include extra folders: %s", meta)
	}
	// A disabled folder doesn't exist.
	browseExpectStatus(t, ts, "random", "BrowseDirectChildren", http.StatusInternalServerError)
}

func TestFavoritesFolder(t *testing.T) {
	ts := newExtraFoldersServer(t, folderFavorites)
	didl := didlResult(t, browse(t, ts, "favorites", "BrowseDirectChildren"))
	assertOrder(t, didl, `id="asset:a3" parentID="favorites"`)
	if strings.Contains(didl, "asset:a1") {
		t.Errorf("non-favorite listed: %s", didl)
	}
	if meta := didlResult(t, browse(t, ts, "favorites", "BrowseMetadata")); !strings.Contains(meta, `<dc:title>Favorites</dc:title>`) {
		t.Errorf("favorites metadata: %s", meta)
	}
}

func TestOnThisDayFolder(t *testing.T) {
	old := timeNow
	timeNow = func() time.Time { return time.Date(2024, 12, 24, 12, 0, 0, 0, time.UTC) }
	defer func() { timeNow = old }()

	ts, fake := newExtraFoldersServerWithFake(t, folderOnThisDay)
	didl := didlResult(t, browse(t, ts, "onthisday", "BrowseDirectChildren"))
	// Newest year first; a6 was taken at 00:30 local time on 24 December
	// 2022 - 23 December in UTC - and counts for its local date, like in
	// Immich's own memories.
	assertOrder(t, didl, `id="asset:a1" parentID="onthisday"`, `id="asset:a6"`, `id="asset:a2"`)
	if strings.Contains(didl, "asset:a4") {
		t.Errorf("today's own photo belongs to the timeline, not On this day: %s", didl)
	}
	if strings.Contains(didl, "asset:a3") {
		t.Errorf("photo from another day listed: %s", didl)
	}
	if strings.Contains(didl, "asset:a7") {
		t.Errorf("photo taken on 25 December local time (24 December UTC) listed: %s", didl)
	}
	// Fetching the whole library takes large libraries longer than TVs
	// wait for a Browse answer - the folder must use date searches.
	if n := fake.fullListings.Load(); n != 0 {
		t.Errorf("On this day fetched the whole library %d times", n)
	}
}

// On 29 February, years without one are skipped rather than searched for
// 1 March.
func TestOnThisDayLeapDay(t *testing.T) {
	old := timeNow
	timeNow = func() time.Time { return time.Date(2024, 2, 29, 12, 0, 0, 0, time.UTC) }
	defer func() { timeNow = old }()

	ts := newExtraFoldersServer(t, folderOnThisDay)
	didl := didlResult(t, browse(t, ts, "onthisday", "BrowseDirectChildren"))
	if strings.Contains(didl, "<item") {
		t.Errorf("no photo was taken on a 29 February, got: %s", didl)
	}
}

func TestPlacesFolder(t *testing.T) {
	ts := newExtraFoldersServer(t, folderPlaces)

	countries := didlResult(t, browse(t, ts, "places", "BrowseDirectChildren"))
	assertOrder(t, countries,
		`id="place:France" parentID="places"`, `childCount="1"`, `<dc:title>France</dc:title>`,
		`id="place:Germany"`, `childCount="2"`, `<dc:title>Germany</dc:title>`)

	cities := didlResult(t, browse(t, ts, "place:Germany", "BrowseDirectChildren"))
	assertOrder(t, cities,
		`id="place:Germany:Berlin" parentID="place:Germany"`, `childCount="2"`,
		`id="place:Germany:Hamburg"`, `childCount="1"`)

	berlin := didlResult(t, browse(t, ts, "place:Germany:Berlin", "BrowseDirectChildren"))
	assertOrder(t, berlin, `id="asset:a1" parentID="place:Germany:Berlin"`, `id="asset:a4"`)

	// A country's assets without a city go into "Other".
	france := didlResult(t, browse(t, ts, "place:France", "BrowseDirectChildren"))
	assertOrder(t, france, `id="place:France:"`, `<dc:title>Other</dc:title>`)

	meta := didlResult(t, browse(t, ts, "place:Germany:Hamburg", "BrowseMetadata"))
	assertOrder(t, meta, `id="place:Germany:Hamburg" parentID="place:Germany"`, `<dc:title>Hamburg</dc:title>`)

	for _, bad := range []string{"place:Atlantis", "place:Germany:Paris", "place:%zz"} {
		browseExpectStatus(t, ts, bad, "BrowseDirectChildren", http.StatusInternalServerError)
	}
}

func TestPlaceIDRoundTrip(t *testing.T) {
	city := "Saint-Denis: Nord/Est"
	id := placeID("", "Côte d'Ivoire", &city)
	country, gotCity, ok := parsePlaceID(id)
	if !ok || country != "Côte d'Ivoire" || gotCity == nil || *gotCity != city {
		t.Errorf("round trip of %q = %q, %v, %v", id, country, gotCity, ok)
	}
	if strings.Count(id, ":") != 2 {
		t.Errorf("names must not add separators: %q", id)
	}
}

func TestRandomSampleIsStableWithinTheHour(t *testing.T) {
	media := make([]immich.Asset, 250)
	for i := range media {
		media[i].ID = string(rune('A'+i%26)) + strings.Repeat("x", i)
	}
	t0 := time.Date(2024, 5, 1, 10, 5, 0, 0, time.UTC)
	a := randomSample(media, t0, 0)
	b := randomSample(media, t0.Add(40*time.Minute), 0)
	c := randomSample(media, t0.Add(time.Hour), 0)
	if len(a) != randomFolderSize {
		t.Fatalf("sample size = %d, want %d", len(a), randomFolderSize)
	}
	if a[0].ID != b[0].ID || a[99].ID != b[99].ID {
		t.Error("sample changed within the same hour - paging would be inconsistent")
	}
	same := 0
	for i := range a {
		if a[i].ID == c[i].ID {
			same++
		}
	}
	if same == len(a) {
		t.Error("sample didn't change in the next hour")
	}
	if got := randomSample(media[:3], t0, 0); len(got) != 3 {
		t.Errorf("small library: got %d assets, want all 3", len(got))
	}
}

func TestSystemUpdateIDChangesWithAlbums(t *testing.T) {
	var albums atomic.Value
	albums.Store(`[{"id":"al1","albumName":"Trip","assetCount":1}]`)
	fakeImmich := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/albums":
			_, _ = w.Write([]byte(albums.Load().(string)))
		case "/api/people":
			_, _ = w.Write([]byte(`{"people":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(fakeImmich.Close)
	cfg := &config.Config{ImmichURL: fakeImmich.URL, APIKeys: []string{"k"}, FriendlyName: "Test"}
	srv := NewServer(cfg, []UserClient{{Client: immich.New(fakeImmich.URL, "k")}}, nil)

	getID := func() string {
		body := `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:GetSystemUpdateID xmlns:u="urn:schemas-upnp-org:service:ContentDirectory:1"/></s:Body></s:Envelope>`
		_, resp := soapPost(t, srv, "/ctl/ContentDirectory", body)
		i := strings.Index(resp, "<Id>")
		return resp[i+4 : strings.Index(resp, "</Id>")]
	}
	if id := getID(); id != "1" {
		t.Fatalf("initial SystemUpdateID = %s, want 1", id)
	}
	if id := getID(); id != "1" {
		t.Fatalf("unchanged library bumped SystemUpdateID to %s", id)
	}
	albums.Store(`[{"id":"al1","albumName":"Trip","assetCount":2}]`)
	if id := getID(); id != "2" {
		t.Errorf("after an album changed, SystemUpdateID = %s, want 2", id)
	}
}

func TestHealthz(t *testing.T) {
	var up atomic.Bool
	up.Store(true)
	fakeImmich := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/server/ping" && up.Load() {
			_, _ = w.Write([]byte(`{"res":"pong"}`))
			return
		}
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(fakeImmich.Close)
	cfg := &config.Config{ImmichURL: fakeImmich.URL, APIKeys: []string{"k"}}
	srv := NewServer(cfg, []UserClient{{Client: immich.New(fakeImmich.URL, "k")}}, nil)

	rec := httptest.NewRecorder()
	srv.Mux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("healthy: status = %d", rec.Code)
	}
	up.Store(false)
	rec = httptest.NewRecorder()
	srv.Mux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("Immich down: status = %d, want 503", rec.Code)
	}
}
