package config

import "testing"

func TestParseMaxResolution(t *testing.T) {
	cases := []struct {
		in          string
		wantW       int
		wantH       int
		wantErr     bool
		description string
	}{
		{"", 0, 0, false, "empty means disabled"},
		{"1920x1080", 1920, 1080, false, "lowercase x"},
		{"1920X1080", 1920, 1080, false, "uppercase X"},
		{" 1920x1080 ", 1920, 1080, false, "surrounding whitespace"},
		{"3840x2160", 3840, 2160, false, "4K"},
		{"1920", 0, 0, true, "missing height"},
		{"1920x", 0, 0, true, "empty height"},
		{"x1080", 0, 0, true, "empty width"},
		{"0x1080", 0, 0, true, "zero width rejected"},
		{"1920x0", 0, 0, true, "zero height rejected"},
		{"-1920x1080", 0, 0, true, "negative width rejected"},
		{"abcxdef", 0, 0, true, "non-numeric"},
	}

	for _, c := range cases {
		t.Run(c.description, func(t *testing.T) {
			w, h, err := parseMaxResolution(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("input %q: expected error, got none", c.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("input %q: unexpected error: %v", c.in, err)
			}
			if w != c.wantW || h != c.wantH {
				t.Fatalf("input %q: got %dx%d, want %dx%d", c.in, w, h, c.wantW, c.wantH)
			}
		})
	}
}
