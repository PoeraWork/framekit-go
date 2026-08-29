package pipeline_test

import (
	"path/filepath"
	"testing"

	"framekit/internal/pipeline"
)

func TestNormalizeMountPoint(t *testing.T) {
	cases := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{"R", "R:", false},
		{"r", "R:", false},
		{"R:", "R:", false},
		{"r:", "R:", false},
		{`R:\`, "R:", false},
		{`R:\Frames`, `R:\Frames`, false},
		{"R:/Frames", `R:\Frames`, false}, // forward slash normalised
		{" R ", "R:", false},              // surrounding spaces stripped
		{"", "", true},
		{"1", "", true},   // not a letter
		{"RR:", "", true}, // two letters
		{"123", "", true},
	}
	for _, c := range cases {
		got, err := pipeline.NormalizeMountPoint(c.input)
		if c.wantErr {
			if err == nil {
				t.Errorf("NormalizeMountPoint(%q) = %q, want error", c.input, got)
			}
		} else {
			if err != nil {
				t.Errorf("NormalizeMountPoint(%q) unexpected error: %v", c.input, err)
			} else if got != c.want {
				t.Errorf("NormalizeMountPoint(%q) = %q, want %q", c.input, got, c.want)
			}
		}
	}
}

func TestDebugConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := pipeline.DefaultConfig()
	cfg.Debug.Enabled = true
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}

	got, err := pipeline.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Debug.Enabled {
		t.Fatal("debug.enabled was not preserved")
	}
}
