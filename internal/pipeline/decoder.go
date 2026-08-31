package pipeline

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/asticode/go-astiav"

	"framekit/internal/applog"
)

// decodeImage decodes a single image file (PNG, BMP, JPEG, ...) into a frame
// using libavcodec. The returned frame is owned by the caller and must be
// Free()'d once it is no longer needed.
func decodeImage(path string) (*astiav.Frame, error) {
	formatContext := astiav.AllocFormatContext()
	if formatContext == nil {
		return nil, errors.New("could not allocate format context")
	}
	defer formatContext.Free()

	inputFormat, err := imageInputFormat(path)
	if err != nil {
		return nil, err
	}
	if err := formatContext.OpenInput(path, inputFormat, nil); err != nil {
		return nil, fmt.Errorf("opening image: %w", err)
	}
	defer formatContext.CloseInput()

	if err := formatContext.FindStreamInfo(nil); err != nil {
		return nil, fmt.Errorf("reading image info: %w", err)
	}

	var videoStream *astiav.Stream
	for _, s := range formatContext.Streams() {
		if s.CodecParameters().MediaType() == astiav.MediaTypeVideo {
			videoStream = s
			break
		}
	}
	if videoStream == nil {
		return nil, errors.New("no video stream in image")
	}

	codec := astiav.FindDecoder(videoStream.CodecParameters().CodecID())
	if codec == nil {
		return nil, errors.New("no decoder available for image format")
	}
	codecContext := astiav.AllocCodecContext(codec)
	if codecContext == nil {
		return nil, errors.New("could not allocate decoder context")
	}
	defer codecContext.Free()
	if err := videoStream.CodecParameters().ToCodecContext(codecContext); err != nil {
		return nil, fmt.Errorf("configuring decoder: %w", err)
	}
	if err := codecContext.Open(codec, nil); err != nil {
		return nil, fmt.Errorf("opening decoder: %w", err)
	}

	packet := astiav.AllocPacket()
	defer packet.Free()
	frame := astiav.AllocFrame()

	for {
		if err := formatContext.ReadFrame(packet); err != nil {
			if errors.Is(err, astiav.ErrEof) {
				break
			}
			frame.Free()
			return nil, fmt.Errorf("reading packet: %w", err)
		}
		if packet.StreamIndex() != videoStream.Index() {
			packet.Unref()
			continue
		}
		err := codecContext.SendPacket(packet)
		packet.Unref()
		if err != nil {
			frame.Free()
			return nil, fmt.Errorf("decoding image: %w", err)
		}
		err = codecContext.ReceiveFrame(frame)
		if err == nil {
			return frame, nil
		}
		if errors.Is(err, astiav.ErrEagain) {
			continue
		}
		frame.Free()
		return nil, fmt.Errorf("receiving decoded frame: %w", err)
	}

	// Flush the decoder.
	if err := codecContext.SendPacket(nil); err != nil {
		frame.Free()
		return nil, fmt.Errorf("flushing decoder: %w", err)
	}
	if err := codecContext.ReceiveFrame(frame); err == nil {
		return frame, nil
	}
	frame.Free()
	return nil, errors.New("no frame decoded from image")
}

func imageInputFormat(path string) (*astiav.InputFormat, error) {
	var name string
	switch strings.ToLower(filepath.Ext(path)) {
	case ".bmp":
		name = "bmp_pipe"
	case ".png":
		name = "png_pipe"
	case ".jpg", ".jpeg":
		name = "jpeg_pipe"
	default:
		return nil, nil
	}

	inputFormat := astiav.FindInputFormat(name)
	if inputFormat == nil {
		return nil, fmt.Errorf("FFmpeg input format %q is unavailable", name)
	}
	return inputFormat, nil
}

// decodeImageWithRetry retries decoding because the recording tool may still
// hold a lock on a freshly written frame file.
func decodeImageWithRetry(ctx context.Context, path string, retries int, delay time.Duration, debug bool) (*astiav.Frame, error) {
	var lastErr error
	started := time.Now()
	for attempt := 0; attempt < retries; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		frame, err := decodeImage(path)
		if err == nil {
			if err := ctx.Err(); err != nil {
				frame.Free()
				return nil, err
			}
			if debug && attempt > 0 {
				log.Printf("debug: decode succeeded attempt=%d/%d elapsed=%s path=%q", attempt+1, retries, time.Since(started).Round(time.Millisecond), path)
			}
			return frame, nil
		}
		lastErr = err
		if debug {
			log.Printf("debug: decode failed attempt=%d/%d elapsed=%s path=%q %s error=%v", attempt+1, retries, time.Since(started).Round(time.Millisecond), path, describeImageFile(path), err)
		}
		if attempt < retries-1 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return nil, fmt.Errorf("after %d attempts: %w", retries, lastErr)
}

func describeImageFile(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Sprintf("stat_error=%q", err)
	}
	result := fmt.Sprintf("size=%d modtime=%s", info.Size(), info.ModTime().Format(time.RFC3339Nano))
	f, err := os.Open(path)
	if err != nil {
		return result + fmt.Sprintf(" open_error=%q", err)
	}
	defer f.Close()

	header := make([]byte, 54)
	n, readErr := f.Read(header)
	result += fmt.Sprintf(" header_bytes=%d", n)
	if n >= 34 && string(header[:2]) == "BM" {
		result += fmt.Sprintf(
			" bmp_declared_size=%d pixel_offset=%d dib_size=%d width=%d height=%d bpp=%d compression=%d",
			binary.LittleEndian.Uint32(header[2:6]),
			binary.LittleEndian.Uint32(header[10:14]),
			binary.LittleEndian.Uint32(header[14:18]),
			int32(binary.LittleEndian.Uint32(header[18:22])),
			int32(binary.LittleEndian.Uint32(header[22:26])),
			binary.LittleEndian.Uint16(header[28:30]),
			binary.LittleEndian.Uint32(header[30:34]),
		)
	} else if n > 0 {
		prefixLen := n
		if prefixLen > 16 {
			prefixLen = 16
		}
		result += fmt.Sprintf(" header_prefix=%x", header[:prefixLen])
	}
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		result += fmt.Sprintf(" read_error=%q", readErr)
	}
	return result
}

func preserveFailedFrame(path string, frameIndex int) (string, error) {
	dir, err := applog.DiagnosticsDir()
	if err != nil {
		return "", err
	}
	src, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("opening failed frame: %w", err)
	}
	defer src.Close()

	ext := filepath.Ext(path)
	name := fmt.Sprintf("failed-frame-%d-%s-%d%s", frameIndex, time.Now().Format("20060102-150405.000"), os.Getpid(), ext)
	dstPath := filepath.Join(dir, name)
	dst, err := os.OpenFile(dstPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return "", fmt.Errorf("creating diagnostic copy: %w", err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		_ = os.Remove(dstPath)
		return "", fmt.Errorf("copying failed frame: %w", err)
	}
	if err := dst.Close(); err != nil {
		_ = os.Remove(dstPath)
		return "", fmt.Errorf("closing diagnostic copy: %w", err)
	}
	return dstPath, nil
}
