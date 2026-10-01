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

// slowTimelineImmich serves a one-asset timeline whose asset ID is
// "gen<N>" for the N-th full listing, optionally delaying or failing
// listings, and counts them.
type slowTimelineImmich struct {
	listings atomic.Int32
	delay    atomic.Int64 // nanoseconds
	fail     atomic.Bool
}

func (f *slowTimelineImmich) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/search/metadata" {
		http.NotFound(w, r)
		return
	}
	n := f.listings.Add(1)
	time.Sleep(time.Duration(f.delay.Load()))
	if f.fail.Load() {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	item := map[string]any{"id": "gen" + string(rune('0'+n)), "originalFileName": "x.jpg", "originalMimeType": "image/jpeg", "type": "IMAGE"}
	_ = json.NewEncoder(w).Encode(map[string]any{"assets": map[string]any{"nextPage": nil, "items": []any{item}}})
}

func newSlowTimelineServer(t *testing.T, refresh time.Duration) (string, *slowTimelineImmich, *Server) {
	t.Helper()
	fake := &slowTimelineImmich{}
	immichSrv := httptest.NewServer(fake)
	t.Cleanup(immichSrv.Close)
	cfg := &config.Config{ImmichURL: immichSrv.URL, APIKeys: []string{"k"}, FriendlyName: "Test", TimelineRefresh: refresh}
	srv := NewServer(cfg, []UserClient{{Client: immich.New(immichSrv.URL, "k")}}, nil)
	ts := httptest.NewServer(srv.Mux())
	t.Cleanup(ts.Close)
	return ts.URL, fake, srv
}

// eventually polls cond until it's true or a few seconds have passed.
func eventually(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal(msg)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A stale copy is served immediately - even while the refresh is slow -
// and replaced once the background refresh completes.
func TestTimelineServesStaleCopyWhileRefreshing(t *testing.T) {
	ts, fake, srv := newSlowTimelineServer(t, time.Millisecond)
	if !strings.Contains(didlResult(t, browse(t, ts, "timeline", "BrowseDirectChildren")), "asset:gen1") {
		t.Fatal("first listing missing")
	}

	time.Sleep(5 * time.Millisecond) // let the copy go stale
	fake.delay.Store(int64(300 * time.Millisecond))
	start := time.Now()
	didl := didlResult(t, browse(t, ts, "timeline", "BrowseDirectChildren"))
	if !strings.Contains(didl, "asset:gen1") {
		t.Errorf("stale copy not served during refresh: %s", didl)
	}
	if took := time.Since(start); took > 200*time.Millisecond {
		t.Errorf("Browse waited %s for the refresh instead of answering from memory", took)
	}

	eventually(t, func() bool {
		assets, _ := srv.timeline.get(0, srv.users[0].Client)
		return len(assets) == 1 && assets[0].ID == "gen2"
	}, "background refresh never replaced the copy")
}

// While the very first load is still running, Browse gives up after
// timelineFirstLoadWait with a SOAP fault rather than hanging until the
// TV times out - and the load carries on, so a later Browse succeeds.
func TestTimelineFirstLoadTooSlow(t *testing.T) {
	old := timelineFirstLoadWait
	timelineFirstLoadWait = 50 * time.Millisecond
	defer func() { timelineFirstLoadWait = old }()

	ts, fake, _ := newSlowTimelineServer(t, time.Hour)
	fake.delay.Store(int64(300 * time.Millisecond))
	resp := browseExpectStatus(t, ts, "timeline", "BrowseDirectChildren", http.StatusInternalServerError)
	if !strings.Contains(resp, "<errorCode>501</errorCode>") {
		t.Errorf("expected Action Failed fault while loading, got: %s", resp)
	}

	time.Sleep(400 * time.Millisecond)
	if !strings.Contains(didlResult(t, browse(t, ts, "timeline", "BrowseDirectChildren")), "asset:gen1") {
		t.Error("load didn't complete in the background")
	}
	if n := fake.listings.Load(); n != 1 {
		t.Errorf("Immich listed %d times, want 1 (the retry should reuse the finished load)", n)
	}
}

// A failed first load is reported and retried on the next access.
func TestTimelineFirstLoadFailureIsRetried(t *testing.T) {
	ts, fake, _ := newSlowTimelineServer(t, time.Hour)
	fake.fail.Store(true)
	browseExpectStatus(t, ts, "timeline", "BrowseDirectChildren", http.StatusInternalServerError)

	fake.fail.Store(false)
	if !strings.Contains(didlResult(t, browse(t, ts, "timeline", "BrowseDirectChildren")), "asset:gen2") {
		t.Error("retry after a failed first load didn't succeed")
	}
}

// WarmUp loads the timeline before anyone browses it, so the first Browse
// is answered from memory without another Immich listing.
func TestTimelineWarmUp(t *testing.T) {
	ts, fake, srv := newSlowTimelineServer(t, time.Hour)
	srv.WarmUp()
	eventually(t, func() bool { return fake.listings.Load() == 1 }, "WarmUp didn't load the timeline")
	time.Sleep(20 * time.Millisecond)

	if !strings.Contains(didlResult(t, browse(t, ts, "timeline", "BrowseDirectChildren")), "asset:gen1") {
		t.Error("warmed-up timeline not served")
	}
	if n := fake.listings.Load(); n != 1 {
		t.Errorf("Immich listed %d times, want 1", n)
	}
}
