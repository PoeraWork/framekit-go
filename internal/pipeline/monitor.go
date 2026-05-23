package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// autoDetectRe matches "<prefix><digits>.<ext>" with the digit run being the
// longest trailing one. It is the default template detector when no user
// pattern is configured.
var autoDetectRe = regexp.MustCompile(`^(.+?)(\d+)\.(\w+)$`)

// Monitor watches a directory for frame files produced by the recording tool.
// Call AwaitTemplate once to lock in the naming template (auto-detected from
// the first matching file or driven by MonitorConfig.Pattern), then WaitFrame
// to pull each subsequent frame by its logical index.
type Monitor struct {
	dir string
	cfg MonitorConfig
	tpl template
}

// template records what AwaitTemplate learned about the recording tool's
// file-naming scheme. In pattern mode only re and startIdx are populated; in
// auto-detect mode the predictable prefix / digits / ext let WaitFrame do an
// O(1) os.Stat instead of scanning the directory.
type template struct {
	re *regexp.Regexp

	prefix string
	digits int
	ext    string

	startIdx int
}

func NewMonitor(dir string, cfg MonitorConfig) *Monitor {
	// Resolve symlinks/junctions so file operations use the real path.
	// On Windows, folder mount points are reparse points; os.Stat through
	// them can miss newly created files whereas the resolved path works reliably.
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	return &Monitor{dir: dir, cfg: cfg}
}

// AwaitTemplate blocks until a file matching the configured pattern — or, if
// the pattern is empty, any file with a trailing digit run in its name —
// appears in the watch directory. It returns ctx.Err() on cancellation.
func (m *Monitor) AwaitTemplate(ctx context.Context) error {
	var userRe *regexp.Regexp
	if strings.TrimSpace(m.cfg.Pattern) != "" {
		re, err := regexp.Compile(m.cfg.Pattern)
		if err != nil {
			return fmt.Errorf("invalid monitor.pattern: %w", err)
		}
		if re.NumSubexp() < 1 {
			return errors.New("monitor.pattern must have a capture group for the frame index")
		}
		userRe = re
	}

	poll := time.Duration(m.cfg.PollIntervalMS) * time.Millisecond
	for {
		if tpl, ok := m.detectOnce(userRe); ok {
			m.tpl = tpl
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}

// detectOnce scans the watch directory once and derives a template from the
// smallest-numbered matching file (so a race that surfaces frames out of order
// still locks onto the true starting index).
func (m *Monitor) detectOnce(userRe *regexp.Regexp) (template, bool) {
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		log.Printf("monitor: ReadDir %s: %v", m.dir, err)
		return template{}, false
	}
	var (
		best    template
		bestIdx int
		found   bool
	)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		var tpl template
		var idx int
		if userRe != nil {
			match := userRe.FindStringSubmatch(name)
			if match == nil {
				continue
			}
			n, err := strconv.Atoi(match[1])
			if err != nil {
				continue
			}
			tpl = template{re: userRe, startIdx: n}
			idx = n
		} else {
			match := autoDetectRe.FindStringSubmatch(name)
			if match == nil {
				continue
			}
			n, err := strconv.Atoi(match[2])
			if err != nil {
				continue
			}
			tpl = template{
				prefix:   match[1],
				digits:   len(match[2]),
				ext:      match[3],
				startIdx: n,
			}
			idx = n
		}
		if !found || idx < bestIdx {
			best = tpl
			bestIdx = idx
			found = true
		}
	}
	return best, found
}

// findFrame returns the path to the file holding logical frame i, or "".
func (m *Monitor) findFrame(i int) string {
	target := i + m.tpl.startIdx
	if m.tpl.re != nil {
		entries, err := os.ReadDir(m.dir)
		if err != nil {
			return ""
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			match := m.tpl.re.FindStringSubmatch(e.Name())
			if match == nil {
				continue
			}
			n, err := strconv.Atoi(match[1])
			if err != nil || n != target {
				continue
			}
			return filepath.Join(m.dir, e.Name())
		}
		return ""
	}
	// %0*d uses digits as a minimum width: overflow (e.g. frame 10000 when
	// digits=4) prints all digits without truncation, so we cover both
	// zero-padded and natural-width naming with one Stat.
	name := fmt.Sprintf("%s%0*d.%s", m.tpl.prefix, m.tpl.digits, target, m.tpl.ext)
	path := filepath.Join(m.dir, name)
	if _, err := os.Stat(path); err == nil {
		return path
	}
	return ""
}

// WaitFrame polls until logical frame i appears, returning false if ctx is
// cancelled or no frame shows up within the configured no-new-frame timeout.
func (m *Monitor) WaitFrame(ctx context.Context, i int) (string, bool) {
	poll := time.Duration(m.cfg.PollIntervalMS) * time.Millisecond
	timeout := time.Duration(m.cfg.NoNewFrameTimeoutS) * time.Second
	var waited time.Duration
	for {
		if p := m.findFrame(i); p != "" {
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
