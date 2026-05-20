package pipeline

import (
	"errors"
	"fmt"
	"time"

	"github.com/asticode/go-astiav"
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

	if err := formatContext.OpenInput(path, nil, nil); err != nil {
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

// decodeImageWithRetry retries decoding because the recording tool may still
// hold a lock on a freshly written frame file.
func decodeImageWithRetry(path string, retries int, delay time.Duration) (*astiav.Frame, error) {
	var lastErr error
	for attempt := 0; attempt < retries; attempt++ {
		frame, err := decodeImage(path)
		if err == nil {
			return frame, nil
		}
		lastErr = err
		if attempt < retries-1 {
			time.Sleep(delay)
		}
	}
	return nil, fmt.Errorf("after %d attempts: %w", retries, lastErr)
}
