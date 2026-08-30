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

func enableFFmpegDebugLogging() {
	astiav.SetLogLevel(astiav.LogLevelDebug)
	astiav.SetLogCallback(func(c astiav.Classer, level astiav.LogLevel, _ string, message string) {
		message = strings.TrimSpace(message)
		if message == "" {
			return
		}
		className := ""
		if c != nil && c.Class() != nil {
			className = c.Class().String()
		}
		log.Printf("ffmpeg: level=%d class=%q message=%s", level, className, message)
	})
}

func disableFFmpegDebugLogging() {
	astiav.ResetLogCallback()
	astiav.SetLogLevel(astiav.LogLevelError)
}

// MountStatus is the result of inspecting a configured mount point.
type MountStatus int

const (
	MountOK          MountStatus = iota // free drive letter, or empty/missing directory
	MountDriveInUse                     // drive letter already in use
	MountDirNonEmpty                    // directory exists and is not empty
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
func Run(ctx context.Context, cfg Config, onProgress func(done, total int)) (runResult error) {
	if cfg.Output.TotalFrames <= 0 {
		return errors.New("total_frames must be > 0")
	}
	total := cfg.Output.TotalFrames
	skip := cfg.Encoder.SkipFrames
	if cfg.Debug.Enabled {
		enableFFmpegDebugLogging()
		defer disableFFmpegDebugLogging()
		log.Printf("debug: run config mount=%q size_mb=%d output_dir=%q output_pattern=%q total_frames=%d codec=%q fps=%d skip_frames=%d monitor_pattern=%q poll_ms=%d timeout_s=%d hibernate=%t",
			cfg.Ramdisk.MountPoint, cfg.Ramdisk.SizeMB, cfg.Output.Dir, cfg.Output.FilenamePattern, total,
			cfg.Encoder.Codec, cfg.Encoder.FPS, skip, cfg.Monitor.Pattern, cfg.Monitor.PollIntervalMS, cfg.Monitor.NoNewFrameTimeoutS,
			cfg.Output.Hibernate)
	}

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
	if cfg.Debug.Enabled {
		log.Printf("debug: selected output path=%q", outputPath)
	}

	mount, err := ramdisk.Create()
	if err != nil {
		return err
	}
	var removeOnce sync.Once
	var removeErr error
	removeRamdisk := func() error {
		removeOnce.Do(func() {
			log.Printf("removing RAM disk at %s ...", mount)
			cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancelCleanup()
			removeErr = ramdisk.Remove(cleanupCtx)
			if removeErr != nil {
				log.Printf("warning: RAM disk removal failed: %v", removeErr)
			} else {
				log.Printf("RAM disk removed from %s", mount)
			}
		})
		return removeErr
	}
	defer func() {
		if err := removeRamdisk(); err != nil {
			cleanupErr := fmt.Errorf("removing RAM disk: %w", err)
			if runResult == nil || errors.Is(runResult, context.Canceled) {
				runResult = cleanupErr
			} else {
				runResult = errors.Join(runResult, cleanupErr)
			}
		}
	}()

	log.Printf("ramdisk mounted at %s", mount)

	// A bare drive letter needs a trailing separator (R: -> R:\).
	watchDir := mount
	if len(mount) == 2 && mount[1] == ':' {
		watchDir = mount + "\\"
	}

	// Safety check: a freshly formatted RAM disk must be empty. If files are
	// visible immediately after mounting, the original folder contents leaked
	// through — abort immediately before any os.Remove is called.
	if entries, err := os.ReadDir(watchDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				_ = removeRamdisk()
				return fmt.Errorf("安全检查失败：挂载后目录 %s 内仍可见已有文件，操作已中止以防数据丢失。\n请确保挂载点是空目录，切勿使用含有重要文件的文件夹。", mount)
			}
		}
	}
	monitor := NewMonitor(watchDir, cfg.Monitor, cfg.Debug.Enabled)

	log.Printf("waiting for frames in %s ...", watchDir)
	if err := monitor.AwaitTemplate(ctx); err != nil {
		return err
	}
	log.Println("first frame detected, starting pipeline")

	if skip > 0 {
		log.Printf("skipping first %d frames...", skip)
		for i := 0; i < skip; i++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			path, ok := monitor.WaitFrame(ctx, i)
			if !ok {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return fmt.Errorf("timeout waiting for frame %d", i)
			}
			if err := os.Remove(path); err != nil {
				log.Printf("warning: could not delete %s: %v", path, err)
			}
		}
		log.Println("skip complete, starting encoding")
	}

	encoder, err := NewEncoder(cfg.Encoder, outputPath)
	if err != nil {
		return err
	}
	defer encoder.Abort()

	produced := 0
	var runErr error
	for i := skip; i < total; i++ {
		if err := ctx.Err(); err != nil {
			runErr = err
			break
		}
		path, ok := monitor.WaitFrame(ctx, i)
		if !ok {
			if ctx.Err() != nil {
				runErr = ctx.Err()
			} else {
				runErr = fmt.Errorf("timeout waiting for frame %d", i)
				log.Printf("%v", runErr)
			}
			break
		}
		frame, err := decodeImageWithRetry(ctx, path, 10, 100*time.Millisecond, cfg.Debug.Enabled)
		if err != nil {
			if ctx.Err() != nil {
				runErr = ctx.Err()
				break
			}
			if cfg.Debug.Enabled {
				copyPath, copyErr := preserveFailedFrame(path, i)
				if copyErr != nil {
					log.Printf("warning: failed to preserve frame %d: %v", i, copyErr)
				} else {
					log.Printf("debug: failed frame preserved at %s", copyPath)
				}
			}
			return fmt.Errorf("decoding frame %d (%s): %w", i, path, err)
		}
		err = encoder.Encode(frame)
		frame.Free()
		if err != nil {
			return fmt.Errorf("encoding frame %d: %w", i, err)
		}
		if err := os.Remove(path); err != nil {
			log.Printf("warning: could not delete %s: %v", path, err)
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
	if errors.Is(runErr, context.Canceled) {
		log.Println("stop requested; finalizing the partial video ...")
	}
	if err := encoder.Close(); err != nil {
		return fmt.Errorf("finalizing video %s (file may be incomplete): %w", outputPath, err)
	}
	if err := removeRamdisk(); err != nil {
		cleanupErr := fmt.Errorf("removing RAM disk: %w", err)
		if runErr != nil && !errors.Is(runErr, context.Canceled) {
			return errors.Join(runErr, cleanupErr)
		}
		return cleanupErr
	}

	if runErr != nil {
		if errors.Is(runErr, context.Canceled) {
			log.Printf("stopped — preserved %d frames -> %s", produced, outputPath)
		}
		return runErr
	}
	log.Printf("done — %d frames -> %s", produced, outputPath)

	if cfg.Output.Hibernate {
		if err := exec.CommandContext(ctx, "shutdown", "/h").Run(); err != nil {
			log.Printf("warning: hibernate failed: %v", err)
		}
	}
	return nil
}
