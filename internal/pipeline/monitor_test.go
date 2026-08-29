package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatalf("writing %s: %v", p, err)
	}
	return p
}

func fastMonitor(dir string) *Monitor {
	return NewMonitor(dir, MonitorConfig{PollIntervalMS: 1, NoNewFrameTimeoutS: 1}, false)
}

func TestAwaitTemplateAutoDetect(t *testing.T) {
	cases := []struct {
		name       string
		firstFile  string
		wantPrefix string
		wantDigits int
		wantExt    string
		wantStart  int
	}{
		{"mmd zero-padded", "mmdrecord_0001.png", "mmdrecord_", 4, "png", 1},
		{"start at zero", "frame_0000.bmp", "frame_", 4, "bmp", 0},
		{"no padding", "frame_1.png", "frame_", 1, "png", 1},
		{"embedded digits caught at trailing", "image_v2_007.jpg", "image_v2_", 3, "jpg", 7},
		{"constant prefix digits", "9_42.bmp", "9_", 2, "bmp", 42},
		{"no prefix bare number", "0.bmp", "", 1, "bmp", 0},
		{"no prefix padded", "0001.bmp", "", 4, "bmp", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, c.firstFile)
			m := fastMonitor(dir)
			if err := m.AwaitTemplate(context.Background()); err != nil {
				t.Fatalf("AwaitTemplate: %v", err)
			}
			if m.tpl.prefix != c.wantPrefix || m.tpl.digits != c.wantDigits ||
				m.tpl.ext != c.wantExt || m.tpl.startIdx != c.wantStart {
				t.Errorf("template = {prefix:%q digits:%d ext:%q start:%d}; want {%q %d %q %d}",
					m.tpl.prefix, m.tpl.digits, m.tpl.ext, m.tpl.startIdx,
					c.wantPrefix, c.wantDigits, c.wantExt, c.wantStart)
			}
		})
	}
}

func TestAwaitTemplateSubdirectory(t *testing.T) {
	root := t.TempDir()

	// Old subfolder with stale frames — should be ignored in favour of newer one.
	old := filepath.Join(root, "2026-05-23T10-00-00")
	if err := os.Mkdir(old, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, old, "frame_0001.bmp")

	// Tiny sleep so the newer folder gets a strictly later modtime.
	time.Sleep(5 * time.Millisecond)

	newer := filepath.Join(root, "2026-05-23T22-18-46")
	if err := os.Mkdir(newer, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, newer, "frame_0001.bmp")

	m := fastMonitor(root)
	if err := m.AwaitTemplate(context.Background()); err != nil {
		t.Fatalf("AwaitTemplate: %v", err)
	}
	if m.dir != newer {
		t.Errorf("dir = %s; want newest subdir %s", m.dir, newer)
	}
}

func TestAwaitTemplateRootBeforeSubdir(t *testing.T) {
	root := t.TempDir()
	// Files directly in root take priority over any subdirectory.
	writeFile(t, root, "frame_0001.bmp")
	sub := filepath.Join(root, "2026-05-23T22-18-46")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, sub, "frame_0001.bmp")

	m := fastMonitor(root)
	if err := m.AwaitTemplate(context.Background()); err != nil {
		t.Fatalf("AwaitTemplate: %v", err)
	}
	if m.dir != root {
		t.Errorf("dir = %s; want root %s", m.dir, root)
	}
}

func TestAwaitTemplatePicksLowestIndex(t *testing.T) {
	dir := t.TempDir()
	// File system order isn't guaranteed; both files exist before scan.
	writeFile(t, dir, "shot_0005.png")
	writeFile(t, dir, "shot_0003.png")
	writeFile(t, dir, "shot_0007.png")

	m := fastMonitor(dir)
	if err := m.AwaitTemplate(context.Background()); err != nil {
		t.Fatalf("AwaitTemplate: %v", err)
	}
	if m.tpl.startIdx != 3 {
		t.Errorf("startIdx = %d, want 3 (smallest)", m.tpl.startIdx)
	}
}

func TestAwaitTemplateCancellation(t *testing.T) {
	dir := t.TempDir()
	m := fastMonitor(dir)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err := m.AwaitTemplate(ctx)
	if err == nil {
		t.Fatal("AwaitTemplate returned nil; want ctx error")
	}
}

func TestAwaitTemplateIgnoresNonNumberedFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "readme.txt")
	writeFile(t, dir, "screenshot.png") // no trailing digits

	m := fastMonitor(dir)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := m.AwaitTemplate(ctx); err == nil {
		t.Fatal("AwaitTemplate locked onto a non-numbered file; want ctx timeout")
	}
}

func TestWaitFrameAutoDetect(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "frame_0001.png")

	m := fastMonitor(dir)
	if err := m.AwaitTemplate(context.Background()); err != nil {
		t.Fatalf("AwaitTemplate: %v", err)
	}

	// Logical index 0 → file _0001 (startIdx applied).
	path, ok := m.WaitFrame(context.Background(), 0)
	if !ok {
		t.Fatal("WaitFrame(0) returned false")
	}
	if filepath.Base(path) != "frame_0001.png" {
		t.Errorf("WaitFrame(0) = %s; want frame_0001.png", filepath.Base(path))
	}

	// frame 1 hasn't appeared yet.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, ok := m.WaitFrame(ctx, 1); ok {
		t.Fatal("WaitFrame(1) returned true with no file")
	}

	// Once it does, lookup is O(1) stat.
	writeFile(t, dir, "frame_0002.png")
	path, ok = m.WaitFrame(context.Background(), 1)
	if !ok || filepath.Base(path) != "frame_0002.png" {
		t.Errorf("WaitFrame(1) = (%s, %v); want frame_0002.png", path, ok)
	}
}

func TestWaitFrameDigitsOverflow(t *testing.T) {
	// First frame says digits=4, but %0*d gracefully handles 5-digit indices.
	dir := t.TempDir()
	writeFile(t, dir, "f_0001.png")
	m := fastMonitor(dir)
	if err := m.AwaitTemplate(context.Background()); err != nil {
		t.Fatalf("AwaitTemplate: %v", err)
	}

	writeFile(t, dir, "f_10000.png")
	path, ok := m.WaitFrame(context.Background(), 9999) // 9999 + startIdx(1) = 10000
	if !ok || filepath.Base(path) != "f_10000.png" {
		t.Errorf("overflow lookup = (%s, %v); want f_10000.png", path, ok)
	}
}

func TestPatternMode(t *testing.T) {
	dir := t.TempDir()
	// Recording tool writes x_9.bmp style — incrementing digits are at the
	// front. Auto-detect would pick the constant trailing 9 and fail.
	writeFile(t, dir, "1_9.bmp")

	cfg := MonitorConfig{
		Pattern:            `^(\d+)_9\.bmp$`,
		PollIntervalMS:     1,
		NoNewFrameTimeoutS: 1,
	}
	m := NewMonitor(dir, cfg, false)
	if err := m.AwaitTemplate(context.Background()); err != nil {
		t.Fatalf("AwaitTemplate: %v", err)
	}
	if m.tpl.re == nil {
		t.Fatal("pattern mode: re is nil")
	}
	if m.tpl.startIdx != 1 {
		t.Errorf("startIdx = %d, want 1", m.tpl.startIdx)
	}

	path, ok := m.WaitFrame(context.Background(), 0)
	if !ok || filepath.Base(path) != "1_9.bmp" {
		t.Errorf("WaitFrame(0) = (%s, %v); want 1_9.bmp", path, ok)
	}

	writeFile(t, dir, "2_9.bmp")
	writeFile(t, dir, "noise.txt") // non-matching files ignored

	path, ok = m.WaitFrame(context.Background(), 1)
	if !ok || filepath.Base(path) != "2_9.bmp" {
		t.Errorf("WaitFrame(1) = (%s, %v); want 2_9.bmp", path, ok)
	}
}

func TestPatternErrors(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "anything_0001.png")

	cases := []struct {
		name    string
		pattern string
		wantSub string
	}{
		{"invalid regex", `(`, "invalid monitor.pattern"},
		{"no capture group", `^\d+_9\.bmp$`, "capture group"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := MonitorConfig{Pattern: c.pattern, PollIntervalMS: 1, NoNewFrameTimeoutS: 1}
			m := NewMonitor(dir, cfg, false)
			err := m.AwaitTemplate(context.Background())
			if err == nil {
				t.Fatal("want error, got nil")
			}
			if !strings.Contains(err.Error(), c.wantSub) {
				t.Errorf("error %q does not contain %q", err.Error(), c.wantSub)
			}
		})
	}
}
