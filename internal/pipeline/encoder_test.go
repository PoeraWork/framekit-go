package pipeline

import "testing"

func TestParseBitrate(t *testing.T) {
	cases := []struct {
		input string
		want  int64
		ok    bool
	}{
		{"20M", 20_000_000, true},
		{"20m", 20_000_000, true},
		{"500k", 500_000, true},
		{"500K", 500_000, true},
		{"1G", 1_000_000_000, true},
		{"1g", 1_000_000_000, true},
		{"1024", 1024, true},
		{"0M", 0, true},
		{"", 0, false},
		{"   ", 0, false},
		{"abc", 0, false},
		{"M", 0, false}, // multiplier with no number
	}
	for _, c := range cases {
		got, ok := parseBitrate(c.input)
		if ok != c.ok {
			t.Errorf("parseBitrate(%q): ok=%v, want %v", c.input, ok, c.ok)
			continue
		}
		if ok && got != c.want {
			t.Errorf("parseBitrate(%q) = %d, want %d", c.input, got, c.want)
		}
	}
}
