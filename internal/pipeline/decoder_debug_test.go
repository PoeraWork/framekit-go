package pipeline

import (
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/image/bmp"
)

func TestDescribeImageFileBMP(t *testing.T) {
	header := make([]byte, 54)
	copy(header[:2], "BM")
	binary.LittleEndian.PutUint32(header[2:6], 123456)
	binary.LittleEndian.PutUint32(header[10:14], 54)
	binary.LittleEndian.PutUint32(header[14:18], 40)
	binary.LittleEndian.PutUint32(header[18:22], 1920)
	binary.LittleEndian.PutUint32(header[22:26], 1080)
	binary.LittleEndian.PutUint16(header[28:30], 24)

	path := filepath.Join(t.TempDir(), "frame.bmp")
	if err := os.WriteFile(path, header, 0o644); err != nil {
		t.Fatal(err)
	}

	got := describeImageFile(path)
	for _, want := range []string{
		"size=54",
		"bmp_declared_size=123456",
		"pixel_offset=54",
		"width=1920",
		"height=1080",
		"bpp=24",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("describeImageFile() = %q, want %q", got, want)
		}
	}
}

func TestDecodeImageWithRetryHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := decodeImageWithRetry(ctx, filepath.Join(t.TempDir(), "missing.bmp"), 10, time.Second, false)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("decodeImageWithRetry() error = %v; want context.Canceled", err)
	}
}

func TestImageInputFormat(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"frame.bmp", "bmp_pipe"},
		{"frame.PNG", "png_pipe"},
		{"frame.jpg", "jpeg_pipe"},
		{"frame.JPEG", "jpeg_pipe"},
		{"frame.webp", ""},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got, err := imageInputFormat(tt.path)
			if err != nil {
				t.Fatalf("imageInputFormat(): %v", err)
			}
			if tt.want == "" {
				if got != nil {
					t.Fatalf("imageInputFormat() = %q; want automatic detection", got.Name())
				}
				return
			}
			if got == nil || got.Name() != tt.want {
				gotName := "<nil>"
				if got != nil {
					gotName = got.Name()
				}
				t.Fatalf("imageInputFormat() = %q; want %q", gotName, tt.want)
			}
		})
	}
}

func TestDecodeImageBMPWithForcedInputFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "frame.bmp")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := bmp.Encode(f, img); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	frame, err := decodeImage(path)
	if err != nil {
		t.Fatalf("decodeImage(): %v", err)
	}
	defer frame.Free()
	if frame.Width() != 4 || frame.Height() != 3 {
		t.Fatalf("decoded size = %dx%d; want 4x3", frame.Width(), frame.Height())
	}
}

func TestDescribeImageFilePartialHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "partial.bmp")
	if err := os.WriteFile(path, []byte("BM\x00\x01"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := describeImageFile(path)
	if !strings.Contains(got, "size=4") || !strings.Contains(got, "header_prefix=424d0001") {
		t.Fatalf("describeImageFile() = %q", got)
	}
}
