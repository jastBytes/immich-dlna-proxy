package immich

import (
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
