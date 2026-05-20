package pipeline

import "testing"

func TestFrameName(t *testing.T) {
	cases := []struct {
		prefix string
		digits int
		index  int
		want   string
	}{
		{"mmdrecord_", 4, 0, "mmdrecord_0000"},
		{"mmdrecord_", 4, 1, "mmdrecord_0001"},
		{"mmdrecord_", 4, 999, "mmdrecord_0999"},
		{"mmdrecord_", 4, 9999, "mmdrecord_9999"},
		{"mmdrecord_", 4, 10000, "mmdrecord_10000"}, // overflow: more digits than FrameDigits
		{"f", 3, 0, "f000"},
		{"f", 3, 42, "f042"},
	}
	for _, c := range cases {
		m := &Monitor{cfg: MonitorConfig{FramePrefix: c.prefix, FrameDigits: c.digits}}
		if got := m.frameName(c.index); got != c.want {
			t.Errorf("frameName(%d) with prefix=%q digits=%d = %q, want %q",
				c.index, c.prefix, c.digits, got, c.want)
		}
	}
}
