package pipeline

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/asticode/go-astiav"
)

// TestPipelineEndToEnd exercises decoder.go + encoder.go on synthetic PNG
// frames and verifies the produced MP4 decodes back to the same frame count.
func TestPipelineEndToEnd(t *testing.T) {
	dir := t.TempDir()
	const n, w, h = 10, 320, 240

	var paths []string
	for i := 0; i < n; i++ {
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		c := color.RGBA{uint8(i * 25), uint8(255 - i*25), 128, 255}
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				img.Set(x, y, c)
			}
		}
		p := filepath.Join(dir, "f"+strconv.Itoa(i)+".png")
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
		paths = append(paths, p)
	}

	out := filepath.Join(dir, "out.mp4")
	enc, err := NewEncoder(EncoderConfig{FPS: 30, CRF: 28}, out)
	if err != nil {
		t.Fatal(err)
	}
	for i, p := range paths {
		frame, err := decodeImage(p)
		if err != nil {
			t.Fatalf("decoding frame %d: %v", i, err)
		}
		if err := enc.Encode(frame); err != nil {
			t.Fatalf("encoding frame %d: %v", i, err)
		}
		frame.Free()
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("closing encoder: %v", err)
	}

	info, err := os.Stat(out)
	if err != nil {
		t.Fatalf("output missing: %v", err)
	}
	t.Logf("output mp4: %d bytes", info.Size())

	if got := countVideoFrames(t, out); got != n {
		t.Fatalf("expected %d frames in output, decoded %d", n, got)
	}
	t.Logf("verified %d frames round-tripped through the HEVC pipeline", n)
}

// TestIsElevatedMatchesNetSession cross-checks IsElevated() against the
// classic "net session" trick: it exits 0 only when running as admin.
func TestIsElevatedMatchesNetSession(t *testing.T) {
	cmd := exec.Command("net", "session")
	cmd.Stdout = nil
	cmd.Stderr = nil
	netErr := cmd.Run()
	wantAdmin := (netErr == nil)

	got := IsElevated()
	if got != wantAdmin {
		t.Errorf("IsElevated()=%v but net session says admin=%v", got, wantAdmin)
	}
	if got {
		t.Log("running as administrator")
	} else {
		t.Log("running as standard user")
	}
}

func countVideoFrames(t *testing.T, path string) int {
	formatContext := astiav.AllocFormatContext()
	defer formatContext.Free()
	if err := formatContext.OpenInput(path, nil, nil); err != nil {
		t.Fatal(err)
	}
	defer formatContext.CloseInput()
	if err := formatContext.FindStreamInfo(nil); err != nil {
		t.Fatal(err)
	}

	var videoStream *astiav.Stream
	for _, s := range formatContext.Streams() {
		if s.CodecParameters().MediaType() == astiav.MediaTypeVideo {
			videoStream = s
			break
		}
	}
	if videoStream == nil {
		t.Fatal("no video stream in output")
	}
	codec := astiav.FindDecoder(videoStream.CodecParameters().CodecID())
	if codec == nil {
		t.Fatal("no decoder for output stream")
	}
	t.Logf("output stream codec: %s", codec.Name())

	codecContext := astiav.AllocCodecContext(codec)
	defer codecContext.Free()
	if err := videoStream.CodecParameters().ToCodecContext(codecContext); err != nil {
		t.Fatal(err)
	}
	if err := codecContext.Open(codec, nil); err != nil {
		t.Fatal(err)
	}

	packet := astiav.AllocPacket()
	defer packet.Free()
	frame := astiav.AllocFrame()
	defer frame.Free()

	count := 0
	drain := func() {
		for {
			if err := codecContext.ReceiveFrame(frame); err != nil {
				return
			}
			count++
			frame.Unref()
		}
	}
	for {
		if err := formatContext.ReadFrame(packet); err != nil {
			break
		}
		if packet.StreamIndex() != videoStream.Index() {
			packet.Unref()
			continue
		}
		_ = codecContext.SendPacket(packet)
		packet.Unref()
		drain()
	}
	_ = codecContext.SendPacket(nil)
	drain()
	return count
}
