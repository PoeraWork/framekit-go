package gui

import "testing"

func TestLogWriterKeepsFFmpegDebugOutOfGUI(t *testing.T) {
	ch := make(chan string, 1)
	w := logWriter{ch: ch}

	ffmpegLine := []byte("2026/08/30 13:57:40.790786 ffmpeg: level=48 class=\"AVFormatContext\" message=test\n")
	if n, err := w.Write(ffmpegLine); err != nil || n != len(ffmpegLine) {
		t.Fatalf("Write() = (%d, %v); want (%d, nil)", n, err, len(ffmpegLine))
	}
	select {
	case line := <-ch:
		t.Fatalf("FFmpeg debug line reached GUI channel: %q", line)
	default:
	}

	appLine := []byte("2026/08/30 14:06:56.938556 done\n")
	if _, err := w.Write(appLine); err != nil {
		t.Fatalf("Write(): %v", err)
	}
	select {
	case line := <-ch:
		if line != string(appLine) {
			t.Fatalf("GUI line = %q; want %q", line, appLine)
		}
	default:
		t.Fatal("application log line did not reach GUI channel")
	}
}
