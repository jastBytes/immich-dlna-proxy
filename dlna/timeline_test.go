package dlna

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jastBytes/immich-dlna-proxy/config"
	"github.com/jastBytes/immich-dlna-proxy/immich"
)

func newTimelineTestServer(t *testing.T, grouping string) string {
	t.Helper()
	fakeImmich := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/search/metadata" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"assets":{"nextPage":null,"items":[
			{"id":"a1","originalFileName":"dec.jpg","originalMimeType":"image/jpeg","type":"IMAGE","fileCreatedAt":"2024-12-24T18:00:00.000Z"},
			{"id":"a2","originalFileName":"may.jpg","originalMimeType":"image/jpeg","type":"IMAGE","fileCreatedAt":"2024-05-01T10:00:00.000Z"},
			{"id":"a3","originalFileName":"old.jpg","originalMimeType":"image/jpeg","type":"IMAGE","fileCreatedAt":"2023-07-04T09:00:00.000Z"},
			{"id":"a4","originalFileName":"nodate.jpg","originalMimeType":"image/jpeg","type":"IMAGE"}]}}`))
	}))
	t.Cleanup(fakeImmich.Close)
	cfg := &config.Config{ImmichURL: fakeImmich.URL, APIKeys: []string{"k"}, FriendlyName: "Test", TimelineGrouping: grouping}
	ts := httptest.NewServer(NewServer(cfg, []UserClient{{Client: immich.New(fakeImmich.URL, "k")}}, nil).Mux())
	t.Cleanup(ts.Close)
	return ts.URL
}

// assertOrder checks that every needle appears in haystack, in order.
func assertOrder(t *testing.T, haystack string, needles ...string) {
	t.Helper()
	pos := 0
	for _, n := range needles {
		i := strings.Index(haystack[pos:], n)
		if i < 0 {
			t.Fatalf("%q missing (or out of order) in:\n%s", n, haystack)
		}
		pos += i + len(n)
	}
}

func TestTimelineUngroupedIsFlat(t *testing.T) {
	ts := newTimelineTestServer(t, TimelineGroupingNone)
	didl := didlResult(t, browse(t, ts, "timeline", "BrowseDirectChildren"))
	assertOrder(t, didl, `id="asset:a1"`, `id="asset:a2"`, `id="asset:a3"`, `id="asset:a4"`)
	if strings.Contains(didl, "<container") {
		t.Errorf("ungrouped timeline contains containers:\n%s", didl)
	}
	browseExpectStatus(t, ts, "timeline:2024", "BrowseDirectChildren", http.StatusInternalServerError)
}

func TestTimelineGroupedByYear(t *testing.T) {
	ts := newTimelineTestServer(t, TimelineGroupingYear)

	root := didlResult(t, browse(t, ts, "timeline", "BrowseDirectChildren"))
	assertOrder(t, root,
		`id="timeline:2024" parentID="timeline"`, `childCount="2"`, `<dc:title>2024</dc:title>`,
		`id="timeline:2023"`, `<dc:title>2023</dc:title>`,
		`id="timeline:unknown"`, `<dc:title>Unknown date</dc:title>`)

	year := didlResult(t, browse(t, ts, "timeline:2024", "BrowseDirectChildren"))
	assertOrder(t, year, `id="asset:a1" parentID="timeline:2024"`, `id="asset:a2"`)
	if strings.Contains(year, "a3") {
		t.Errorf("2024 contains a 2023 asset:\n%s", year)
	}

	meta := didlResult(t, browse(t, ts, "timeline:2023", "BrowseMetadata"))
	assertOrder(t, meta, `id="timeline:2023" parentID="timeline"`, `childCount="1"`)

	// Month IDs don't exist in year mode.
	browseExpectStatus(t, ts, "timeline:2024-05", "BrowseDirectChildren", http.StatusInternalServerError)
}

func TestTimelineGroupedByMonth(t *testing.T) {
	ts := newTimelineTestServer(t, TimelineGroupingMonth)

	year := didlResult(t, browse(t, ts, "timeline:2024", "BrowseDirectChildren"))
	assertOrder(t, year,
		`id="timeline:2024-12" parentID="timeline:2024"`, `<dc:title>2024-12</dc:title>`,
		`id="timeline:2024-05"`)

	meta := didlResult(t, browse(t, ts, "timeline:2024", "BrowseMetadata"))
	assertOrder(t, meta, `id="timeline:2024"`, `childCount="2"`) // two months

	month := didlResult(t, browse(t, ts, "timeline:2024-05", "BrowseDirectChildren"))
	assertOrder(t, month, `id="asset:a2" parentID="timeline:2024-05"`)
	if strings.Contains(month, "a1") {
		t.Errorf("2024-05 contains a December asset:\n%s", month)
	}

	monthMeta := didlResult(t, browse(t, ts, "timeline:2024-05", "BrowseMetadata"))
	assertOrder(t, monthMeta, `id="timeline:2024-05" parentID="timeline:2024"`, `childCount="1"`)

	unknown := didlResult(t, browse(t, ts, "timeline:unknown", "BrowseDirectChildren"))
	assertOrder(t, unknown, `id="asset:a4"`)

	for _, bad := range []string{"timeline:1999", "timeline:2024-13x", "timeline:../x"} {
		browseExpectStatus(t, ts, bad, "BrowseDirectChildren", http.StatusInternalServerError)
	}
}
