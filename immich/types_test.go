package immich

import "testing"

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
