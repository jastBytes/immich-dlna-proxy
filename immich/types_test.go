package immich

import (
	"encoding/json"
	"testing"
	"time"
)

func TestAssetIsPhoto(t *testing.T) {
	cases := []struct {
		assetType string
		want      bool
	}{
		{"IMAGE", true},
		{"VIDEO", false},
		{"", false},
	}
	for _, c := range cases {
		a := Asset{Type: c.assetType}
		if got := a.IsPhoto(); got != c.want {
			t.Errorf("Asset{Type: %q}.IsPhoto() = %v, want %v", c.assetType, got, c.want)
		}
	}
}

func TestAssetCapturedAt(t *testing.T) {
	cases := []struct {
		name          string
		fileCreatedAt string
		want          time.Time
	}{
		{"valid RFC3339", "2024-06-01T12:30:00Z", time.Date(2024, 6, 1, 12, 30, 0, 0, time.UTC)},
		{"valid with fractional seconds", "2024-06-01T12:30:00.804Z", time.Date(2024, 6, 1, 12, 30, 0, 804000000, time.UTC)},
		{"empty", "", time.Time{}},
		{"unparseable", "not-a-date", time.Time{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := Asset{FileCreatedAt: c.fileCreatedAt}
			if got := a.CapturedAt(); !got.Equal(c.want) {
				t.Errorf("Asset{FileCreatedAt: %q}.CapturedAt() = %v, want %v", c.fileCreatedAt, got, c.want)
			}
		})
	}
}

func TestAssetIsVideo(t *testing.T) {
	cases := []struct {
		assetType string
		want      bool
	}{
		{"VIDEO", true},
		{"IMAGE", false},
		{"", false},
	}
	for _, c := range cases {
		a := Asset{Type: c.assetType}
		if got := a.IsVideo(); got != c.want {
			t.Errorf("Asset{Type: %q}.IsVideo() = %v, want %v", c.assetType, got, c.want)
		}
	}
}

func TestPersonIsNamed(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"Alice", true},
		{"", false},
	}
	for _, c := range cases {
		p := Person{Name: c.name}
		if got := p.IsNamed(); got != c.want {
			t.Errorf("Person{Name: %q}.IsNamed() = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestPersonHasThumbnail(t *testing.T) {
	cases := []struct {
		thumbnailPath string
		want          bool
	}{
		{"/thumbs/p1.jpg", true},
		{"", false},
	}
	for _, c := range cases {
		p := Person{ThumbnailPath: c.thumbnailPath}
		if got := p.HasThumbnail(); got != c.want {
			t.Errorf("Person{ThumbnailPath: %q}.HasThumbnail() = %v, want %v", c.thumbnailPath, got, c.want)
		}
	}
}

func TestDLNADuration(t *testing.T) {
	cases := []struct {
		typ, durationJSON, want string
	}{
		// Immich 3.x: integer milliseconds, null for photos.
		{"VIDEO", `65250`, "0:01:05.250"},
		{"VIDEO", `3723500`, "1:02:03.500"},
		{"VIDEO", `0`, ""},
		{"VIDEO", `null`, ""},
		// Immich 1.x/2.x: "H:MM:SS.ffffff" strings.
		{"VIDEO", `"0:01:05.250000"`, "0:01:05.250"},
		{"VIDEO", `"1:02:03.5"`, "1:02:03.500"},
		{"VIDEO", `"12:00:00"`, "12:00:00.000"},
		{"VIDEO", `"0:00:00.00000"`, ""}, // zero length
		{"VIDEO", `""`, ""},
		{"VIDEO", `"garbage"`, ""},
		{"VIDEO", `"0:1:05.2"`, ""},
		// Anything else decodes as unknown instead of failing.
		{"VIDEO", `{"weird":true}`, ""},
		{"IMAGE", `"0:00:05.000000"`, ""}, // photos never get a duration
	}
	for _, c := range cases {
		var a Asset
		if err := json.Unmarshal([]byte(`{"type":"`+c.typ+`","duration":`+c.durationJSON+`}`), &a); err != nil {
			t.Errorf("%s %s: decode failed: %v", c.typ, c.durationJSON, err)
			continue
		}
		if got := a.DLNADuration(); got != c.want {
			t.Errorf("%s %s: got %q, want %q", c.typ, c.durationJSON, got, c.want)
		}
	}
}

// Regression test: Immich 3 sends duration as a number. While Asset
// decoded it into a string, every search page containing a video failed
// to decode, so the Timeline, On this day and any album with a video in it
// came out empty.
func TestSearchPageInImmich3FormatDecodes(t *testing.T) {
	page := `{"assets":{"nextPage":null,"items":[
		{"id":"v1","type":"VIDEO","originalMimeType":"video/mp4","fileCreatedAt":"2024-05-01T10:00:00.000Z","localDateTime":"2024-05-01T12:00:00.000Z","duration":13204,"exifInfo":{"exifImageWidth":3840,"exifImageHeight":2160,"fileSizeInByte":123,"country":null,"city":null}},
		{"id":"p1","type":"IMAGE","originalMimeType":"image/jpeg","fileCreatedAt":"2024-05-01T10:00:00.000Z","duration":null,"exifInfo":null}
	]}}`
	var out struct {
		Assets struct {
			Items []Asset `json:"items"`
		} `json:"assets"`
	}
	if err := json.Unmarshal([]byte(page), &out); err != nil {
		t.Fatalf("Immich 3 search page failed to decode: %v", err)
	}
	if len(out.Assets.Items) != 2 || out.Assets.Items[0].DLNADuration() != "0:00:13.204" {
		t.Errorf("decoded %+v", out.Assets.Items)
	}
}
