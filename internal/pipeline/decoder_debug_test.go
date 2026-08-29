package pipeline

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
