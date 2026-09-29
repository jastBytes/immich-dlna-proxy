package dlna

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jastBytes/immich-dlna-proxy/config"
	"github.com/jastBytes/immich-dlna-proxy/immich"
)

// photoSourceImmich fakes an Immich server with a JPEG, a HEIC and a video
// asset, recording which binary endpoints were requested.
type photoSourceImmich struct {
	mu   sync.Mutex
	hits []string
}

const photoSourceAssets = `[
	{"id":"jpg1","originalFileName":"a.jpg","originalMimeType":"image/jpeg","type":"IMAGE","exifInfo":{"fileSizeInByte":1000}},
	{"id":"heic1","originalFileName":"b.HEIC","originalMimeType":"image/heic","type":"IMAGE","exifInfo":{"fileSizeInByte":2000}},
	{"id":"vid1","originalFileName":"c.mov","originalMimeType":"video/quicktime","type":"VIDEO","duration":"0:01:05.250000","exifInfo":{"fileSizeInByte":3000,"exifImageWidth":1920,"exifImageHeight":1080}}
]`

func (f *photoSourceImmich) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/api/search/metadata":
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"assets":{"nextPage":null,"items":` + photoSourceAssets + `}}`))
	case strings.HasPrefix(r.URL.Path, "/api/assets/") && strings.Count(r.URL.Path, "/") == 3:
		id := strings.TrimPrefix(r.URL.Path, "/api/assets/")
		for _, part := range strings.Split(strings.Trim(photoSourceAssets, "[]\n\t "), "},\n\t{") {
			if strings.Contains(part, `"id":"`+id+`"`) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte("{" + strings.Trim(part, "{}\n\t ") + "}}"))
				return
			}
		}
		http.NotFound(w, r)
	case strings.HasSuffix(r.URL.Path, "/original"):
		f.record(r.URL.Path)
		mime := map[string]string{"jpg1": "image/jpeg", "heic1": "image/heic", "vid1": "video/quicktime"}[strings.Split(r.URL.Path, "/")[3]]
		w.Header().Set("Content-Type", mime)
		_, _ = w.Write([]byte("original-bytes"))
	case strings.HasSuffix(r.URL.Path, "/thumbnail"):
		f.record(r.URL.Path + "?" + r.URL.RawQuery)
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("preview-bytes"))
	default:
		http.NotFound(w, r)
	}
}

func (f *photoSourceImmich) record(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hits = append(f.hits, s)
}

func newPhotoSourceServer(t *testing.T, mode string) (string, *photoSourceImmich) {
	t.Helper()
	fake := &photoSourceImmich{}
	immichSrv := httptest.NewServer(fake)
	t.Cleanup(immichSrv.Close)
	cfg := &config.Config{ImmichURL: immichSrv.URL, APIKeys: []string{"k"}, FriendlyName: "Test", PhotoSource: mode}
	ts := httptest.NewServer(NewServer(cfg, []UserClient{{Client: immich.New(immichSrv.URL, "k")}}, nil).Mux())
	t.Cleanup(ts.Close)
	return ts.URL, fake
}

// itemRes returns the <res ...>...</res> element of the item with the
// given id.
func itemRes(t *testing.T, didl, id string) string {
	t.Helper()
	i := strings.Index(didl, `<item id="`+id+`"`)
	if i < 0 {
		t.Fatalf("item %s missing in %s", id, didl)
	}
	item := didl[i:]
	item = item[:strings.Index(item, "</item>")]
	return item[strings.Index(item, "<res "):]
}

func TestPhotoSourceAutoAdvertisesHEICAsJPEG(t *testing.T) {
	ts, _ := newPhotoSourceServer(t, PhotoSourceAuto)
	didl := didlResult(t, browse(t, ts, "timeline", "BrowseDirectChildren"))

	jpg := itemRes(t, didl, "asset:jpg1")
	if !strings.Contains(jpg, "image/jpeg:DLNA.ORG_OP=01;DLNA.ORG_CI=0;") || !strings.Contains(jpg, `size="1000"`) {
		t.Errorf("JPEG original should be advertised unchanged: %s", jpg)
	}
	heic := itemRes(t, didl, "asset:heic1")
	if !strings.Contains(heic, "image/jpeg:DLNA.ORG_OP=01;DLNA.ORG_CI=1;") {
		t.Errorf("HEIC should be advertised as converted JPEG: %s", heic)
	}
	if strings.Contains(heic, "size=") || strings.Contains(heic, "image/heic") {
		t.Errorf("HEIC item must not carry the original's size or MIME type: %s", heic)
	}
	vid := itemRes(t, didl, "asset:vid1")
	if !strings.Contains(vid, "video/quicktime:DLNA.ORG_OP=01;DLNA.ORG_CI=0;DLNA.ORG_FLAGS=0170") ||
		!strings.Contains(vid, `duration="0:01:05.250"`) || !strings.Contains(vid, `resolution="1920x1080"`) {
		t.Errorf("video should carry streaming flags, duration and resolution: %s", vid)
	}
}

func TestPhotoSourceOriginalKeepsHEIC(t *testing.T) {
	ts, _ := newPhotoSourceServer(t, PhotoSourceOriginal)
	heic := itemRes(t, didlResult(t, browse(t, ts, "timeline", "BrowseDirectChildren")), "asset:heic1")
	if !strings.Contains(heic, "image/heic:") || !strings.Contains(heic, `size="2000"`) {
		t.Errorf("PHOTO_SOURCE=original should advertise the HEIC as-is: %s", heic)
	}
}

func getMedia(t *testing.T, url string) (string, http.Header) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return string(b), resp.Header
}

func TestPhotoSourceMediaServesPreviewWhereAdvertised(t *testing.T) {
	cases := []struct {
		mode, asset, wantBody, wantHit string
	}{
		{PhotoSourceAuto, "jpg1", "original-bytes", "/api/assets/jpg1/original"},
		{PhotoSourceAuto, "heic1", "preview-bytes", "/api/assets/heic1/thumbnail?size=preview"},
		{PhotoSourceOriginal, "heic1", "original-bytes", "/api/assets/heic1/original"},
		{PhotoSourcePreview, "jpg1", "preview-bytes", "/api/assets/jpg1/thumbnail?size=preview"},
		// Videos are never replaced by a preview image.
		{PhotoSourcePreview, "vid1", "original-bytes", "/api/assets/vid1/original"},
	}
	for _, c := range cases {
		ts, fake := newPhotoSourceServer(t, c.mode)
		body, _ := getMedia(t, ts+"/media/"+c.asset)
		if body != c.wantBody {
			t.Errorf("%s/%s: body = %q, want %q", c.mode, c.asset, body, c.wantBody)
		}
		fake.mu.Lock()
		hits := strings.Join(fake.hits, ",")
		fake.mu.Unlock()
		if !strings.Contains(hits, c.wantHit) {
			t.Errorf("%s/%s: Immich hits %q, want %q", c.mode, c.asset, hits, c.wantHit)
		}
	}
}

func TestMediaSendsDLNAHeaders(t *testing.T) {
	ts, _ := newPhotoSourceServer(t, PhotoSourceAuto)
	_, h := getMedia(t, ts+"/media/jpg1")
	if h.Get("transferMode.dlna.org") != "Interactive" || !strings.HasPrefix(h.Get("contentFeatures.dlna.org"), "DLNA.ORG_OP=01;") {
		t.Errorf("photo DLNA headers = %q / %q", h.Get("transferMode.dlna.org"), h.Get("contentFeatures.dlna.org"))
	}
}
