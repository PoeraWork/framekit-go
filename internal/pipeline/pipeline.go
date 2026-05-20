package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/asticode/go-astiav"
)

func init() {
	astiav.SetLogLevel(astiav.LogLevelError)
}

// MountStatus is the result of inspecting a configured mount point.
type MountStatus int

const (
	MountOK         MountStatus = iota // free drive letter, or empty/missing directory
	MountDriveInUse                    // drive letter already in use
	MountDirNonEmpty                   // directory exists and is not empty
)

// InspectMountPoint checks a normalized mount point ("X:" or "X:\path") so the
// caller can warn before an in-use drive or a non-empty directory is shadowed.
func InspectMountPoint(mountPoint string) (MountStatus, error) {
	if mountPoint == "" {
		return MountOK, errors.New("挂载点不能为空")
	}
	if len(mountPoint) == 2 && mountPoint[1] == ':' {
		if _, err := os.Stat(mountPoint + "\\"); err == nil {
			return MountDriveInUse, nil
		}
		return MountOK, nil
	}
	entries, err := os.ReadDir(mountPoint)
	if err != nil {
		if os.IsNotExist(err) {
			return MountOK, nil
		}
		return MountOK, fmt.Errorf("inspecting %s: %w", mountPoint, err)
	}
	if len(entries) > 0 {
		return MountDirNonEmpty, nil
	}
	return MountOK, nil
}

// nextOutputPath finds the next free output filename so existing videos are
// never overwritten.
func nextOutputPath(dir, pattern string) string {
	for n := 1; ; n++ {
		name := strings.ReplaceAll(pattern, "{n}", strconv.Itoa(n))
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			return path
		}
	}
}

// Run executes the frame-to-video pipeline. It blocks until the pipeline
// finishes, ctx is cancelled, or an error occurs. If onProgress is non-nil it
// is called as frames are encoded and must not block.
func Run(ctx context.Context, cfg Config, onProgress func(done, total int)) error {
	if cfg.Output.TotalFrames <= 0 {
		return errors.New("total_frames must be > 0")
	}
	total := cfg.Output.TotalFrames
	skip := cfg.Encoder.SkipFrames

	log.Println("pre-flight checks...")
	ramdisk, err := NewRamdisk(cfg.Ramdisk)
	if err != nil {
		return err
	}
	log.Println("dependencies ok")

	if err := os.MkdirAll(cfg.Output.Dir, 0o755); err != nil {
		return fmt.Errorf("creating output directory: %w", err)
	}
	outputPath := nextOutputPath(cfg.Output.Dir, cfg.Output.FilenamePattern)

	mount, err := ramdisk.Create()
	if err != nil {
		return err
	}
	var removeOnce sync.Once
	removeRamdisk := func() {
		removeOnce.Do(func() {
			if err := ramdisk.Remove(); err != nil {
				log.Printf("warning: %v", err)
			}
		})
	}
	defer removeRamdisk()

	log.Printf("ramdisk mounted at %s", mount)

	// A bare drive letter needs a trailing separator (R: -> R:\).
	watchDir := mount
	if len(mount) == 2 && mount[1] == ':' {
		watchDir = mount + "\\"
	}
	monitor := NewMonitor(watchDir, cfg.Monitor)

	log.Printf("waiting for frames in %s ...", watchDir)
	for !monitor.HasAnyFile() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
	log.Println("first frame detected, starting pipeline")

	if skip > 0 {
		log.Printf("skipping first %d frames...", skip)
		for i := 0; i < skip; i++ {
			path, ok := monitor.WaitFrame(ctx, i)
			if !ok {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return fmt.Errorf("timeout waiting for frame %d", i)
			}
			if err := os.Remove(path); err != nil {
				log.Printf("warning: could not delete %s", path)
			}
		}
		log.Println("skip complete, starting encoding")
	}

	encoder, err := NewEncoder(cfg.Encoder, outputPath)
	if err != nil {
		return err
	}

	produced := 0
	var runErr error
	for i := skip; i < total; i++ {
		path, ok := monitor.WaitFrame(ctx, i)
		if !ok {
			if ctx.Err() != nil {
				runErr = ctx.Err()
			} else {
				log.Printf("timeout waiting for frame %d, stopping", i)
			}
			break
		}
		frame, err := decodeImageWithRetry(path, 10, 100*time.Millisecond)
		if err != nil {
			return fmt.Errorf("decoding frame %d (%s): %w", i, path, err)
		}
		err = encoder.Encode(frame)
		frame.Free()
		if err != nil {
			return fmt.Errorf("encoding frame %d: %w", i, err)
		}
		if err := os.Remove(path); err != nil {
			log.Printf("warning: could not delete %s", path)
		}
		produced++
		if onProgress != nil {
			onProgress(produced, total-skip)
		}
		if produced%60 == 0 {
			log.Printf("frame %d / %d (actual idx %d)", produced, total-skip, i)
		}
	}

	if produced == 0 {
		if runErr != nil {
			return runErr
		}
		return errors.New("no frames were produced")
	}
	if err := encoder.Close(); err != nil {
		return fmt.Errorf("finalizing video: %w", err)
	}
	log.Printf("done — %d frames -> %s", produced, outputPath)

	if runErr != nil {
		return runErr
	}

	if cfg.Output.Hibernate {
		if err := exec.Command("shutdown", "/h").Run(); err != nil {
			log.Printf("warning: hibernate failed: %v", err)
		}
	}
	return nil
}
