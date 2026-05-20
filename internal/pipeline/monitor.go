package pipeline

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"time"
)

// Monitor watches a directory for frame files produced by the recording tool.
type Monitor struct {
	dir string
	cfg MonitorConfig
}

func NewMonitor(dir string, cfg MonitorConfig) *Monitor {
	return &Monitor{dir: dir, cfg: cfg}
}

// frameName builds the expected file name (without extension) for a frame index.
func (m *Monitor) frameName(index int) string {
	return fmt.Sprintf("%s%0*d", m.cfg.FramePrefix, m.cfg.FrameDigits, index)
}

// FindFrame walks the watch directory recursively and returns the path of the
// file whose name (ignoring extension) matches the expected frame, or "".
func (m *Monitor) FindFrame(index int) string {
	want := m.frameName(index)
	var found string
	_ = filepath.WalkDir(m.dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if strings.TrimSuffix(name, filepath.Ext(name)) == want {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// WaitFrame polls until the frame appears, returning false if ctx is cancelled
// or no frame shows up within the configured no-new-frame timeout.
func (m *Monitor) WaitFrame(ctx context.Context, index int) (string, bool) {
	poll := time.Duration(m.cfg.PollIntervalMS) * time.Millisecond
	timeout := time.Duration(m.cfg.NoNewFrameTimeoutS) * time.Second
	var waited time.Duration
	for {
		if p := m.FindFrame(index); p != "" {
			return p, true
		}
		select {
		case <-ctx.Done():
			return "", false
		case <-time.After(poll):
		}
		waited += poll
		if waited > timeout {
			return "", false
		}
	}
}

// HasAnyFile reports whether the watch directory contains at least one file.
func (m *Monitor) HasAnyFile() bool {
	var any bool
	_ = filepath.WalkDir(m.dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			any = true
			return filepath.SkipAll
		}
		return nil
	})
	return any
}
