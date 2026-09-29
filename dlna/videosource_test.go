package dlna

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/jastBytes/immich-dlna-proxy/config"
)

func TestVideoSourceTranscodedAdvertisesMP4(t *testing.T) {
	ts, _ := newMediaSourceServer(t, &config.Config{VideoSource: VideoSourceTranscoded})
	vid := itemRes(t, didlResult(t, browse(t, ts, "timeline", "BrowseDirectChildren")), "asset:vid1")
	if !strings.Contains(vid, "video/mp4:DLNA.ORG_OP=01;DLNA.ORG_CI=1;") {
		t.Errorf("transcoded video should be advertised as converted video/mp4: %s", vid)
	}
	if strings.Contains(vid, "size=") || strings.Contains(vid, "resolution=") {
		t.Errorf("transcoded video must not carry the original's size or resolution: %s", vid)
	}
	if !strings.Contains(vid, `duration="0:01:05.250"`) {
		t.Errorf("transcoded video should keep its duration: %s", vid)
	}
}

func TestVideoSourceTranscodedServesPlayback(t *testing.T) {
	ts, fake := newMediaSourceServer(t, &config.Config{VideoSource: VideoSourceTranscoded})

	req, _ := http.NewRequest(http.MethodGet, ts+"/media/vid1", nil)
	req.Header.Set("Range", "bytes=2-4")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent || string(body) != "234" {
		t.Errorf("status %d body %q, want 206 %q from the playback endpoint", resp.StatusCode, body, "234")
	}

	fake.mu.Lock()
	hits := strings.Join(fake.hits, ",")
	fake.mu.Unlock()
	if strings.Contains(hits, "/original") || !strings.Contains(hits, "/api/assets/vid1/video/playback") {
		t.Errorf("Immich hits %q, want only the playback endpoint", hits)
	}

	// Photos are unaffected by VIDEO_SOURCE.
	if got, _ := getMedia(t, ts+"/media/jpg1"); got != "original-bytes" {
		t.Errorf("photo body = %q, want the original", got)
	}
}

func TestVideoSourceOriginalIsDefault(t *testing.T) {
	ts, fake := newMediaSourceServer(t, &config.Config{})
	vid := itemRes(t, didlResult(t, browse(t, ts, "timeline", "BrowseDirectChildren")), "asset:vid1")
	if !strings.Contains(vid, "video/quicktime:") || !strings.Contains(vid, `resolution="1920x1080"`) {
		t.Errorf("original video should be advertised as-is: %s", vid)
	}
	if got, _ := getMedia(t, ts+"/media/vid1"); got != "original-bytes" {
		t.Errorf("video body = %q, want the original", got)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if strings.Contains(strings.Join(fake.hits, ","), "/video/playback") {
		t.Errorf("playback endpoint used without VIDEO_SOURCE=transcoded: %v", fake.hits)
	}
}

// With MAX_RESOLUTION set, a JPEG/PNG may be served smaller than the
// original file, so its original size must not be advertised.
func TestDownscalingOmitsPhotoSize(t *testing.T) {
	ts, _ := newMediaSourceServer(t, &config.Config{MaxWidth: 1920, MaxHeight: 1080})
	didl := didlResult(t, browse(t, ts, "timeline", "BrowseDirectChildren"))
	if jpg := itemRes(t, didl, "asset:jpg1"); strings.Contains(jpg, "size=") {
		t.Errorf("downscalable JPEG must not advertise the original size: %s", jpg)
	}
	if vid := itemRes(t, didl, "asset:vid1"); !strings.Contains(vid, `size="3000"`) {
		t.Errorf("videos are never downscaled and should keep their size: %s", vid)
	}
}
