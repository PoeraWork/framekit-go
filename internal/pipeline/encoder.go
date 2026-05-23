package pipeline

import (
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/asticode/go-astiav"
)

// hevcCandidates is probed in order when no codec is forced in the config.
// Hardware encoders come first because libx265 almost never keeps up with
// real-time frame production; it stays in the list as a software fallback.
var hevcCandidates = []string{"hevc_nvenc", "hevc_amf", "hevc_qsv", "libx265"}

// probeCRF is an arbitrary mid-range quality used only during availability
// probing; the actual run uses EncoderConfig.CRF.
const probeCRF = 23

var encoderLabels = map[string]string{
	"libx265":    "软件 (libx265)",
	"hevc_amf":   "AMD 硬件 (hevc_amf)",
	"hevc_nvenc": "NVIDIA 硬件 (hevc_nvenc)",
	"hevc_qsv":   "Intel 硬件 (hevc_qsv)",
}

// EncoderInfo describes one HEVC encoder candidate.
type EncoderInfo struct {
	Name      string // ffmpeg encoder name, e.g. "hevc_nvenc"
	Label     string // human-friendly label for the GUI
	Available bool   // can actually be opened (hardware present and usable)
}

// AvailableEncoders probes every HEVC candidate and reports which ones can
// actually be opened on this machine, so the GUI only offers usable devices.
func AvailableEncoders() []EncoderInfo {
	astiav.SetLogLevel(astiav.LogLevelQuiet)
	defer astiav.SetLogLevel(astiav.LogLevelError)

	infos := make([]EncoderInfo, 0, len(hevcCandidates))
	for _, name := range hevcCandidates {
		label := encoderLabels[name]
		if label == "" {
			label = name
		}
		infos = append(infos, EncoderInfo{
			Name:      name,
			Label:     label,
			Available: probeEncoder(name),
		})
	}
	return infos
}

// probeEncoder reports whether an encoder can be opened on a small dummy
// context with the same rate-control options the real run uses. Passing the
// real options here keeps the GUI list honest — if a knob is invalid for an
// encoder, that encoder is hidden instead of failing later at frame 0.
func probeEncoder(name string) bool {
	codec := astiav.FindEncoderByName(name)
	if codec == nil {
		return false
	}
	codecContext := astiav.AllocCodecContext(codec)
	if codecContext == nil {
		return false
	}
	defer codecContext.Free()
	codecContext.SetWidth(256)
	codecContext.SetHeight(256)
	codecContext.SetPixelFormat(pickPixelFormat(codec))
	codecContext.SetTimeBase(astiav.NewRational(1, 30))
	codecContext.SetFramerate(astiav.NewRational(30, 1))
	options := rateControlOptions(name, probeCRF)
	defer options.Free()
	return codecContext.Open(codec, options) == nil
}

// rateControlOptions returns the per-encoder quality options. Each hardware
// HEVC encoder uses a different rc enum and qp field name, so we can't share
// one set:
//   - libx265: software CRF
//   - hevc_amf: rc=cqp + qp_i / qp_p (no flat qp option)
//   - hevc_nvenc: rc=constqp (NOT cqp) + flat qp
//   - hevc_qsv: no rc option; ICQ mode driven by global_quality
func rateControlOptions(codecName string, crf int) *astiav.Dictionary {
	d := astiav.NewDictionary()
	noFlags := astiav.NewDictionaryFlags()
	qp := strconv.Itoa(crf)
	switch codecName {
	case "libx265":
		_ = d.Set("crf", qp, noFlags)
	case "hevc_amf":
		_ = d.Set("rc", "cqp", noFlags)
		_ = d.Set("qp_i", qp, noFlags)
		_ = d.Set("qp_p", qp, noFlags)
		_ = d.Set("bf", "0", noFlags) // disable B-frames — AMF reference-frame corruption causes green screens
	case "hevc_nvenc":
		_ = d.Set("rc", "constqp", noFlags)
		_ = d.Set("qp", qp, noFlags)
		_ = d.Set("bf", "0", noFlags) // disable B-frames
	case "hevc_qsv":
		_ = d.Set("global_quality", qp, noFlags)
		_ = d.Set("bf", "0", noFlags) // disable B-frames
	}
	return d
}

// Encoder turns decoded image frames into an HEVC MP4 file via libav*.
//
// It is initialised lazily on the first frame so the output resolution and the
// scaler can be derived from the actual input.
type Encoder struct {
	cfg        EncoderConfig
	outputPath string

	formatContext *astiav.FormatContext
	codecContext  *astiav.CodecContext
	stream        *astiav.Stream
	ioContext     *astiav.IOContext
	scaler        *astiav.SoftwareScaleContext
	dstFrame      *astiav.Frame
	packet        *astiav.Packet

	codecName string
	dstPixFmt astiav.PixelFormat
	srcWidth  int
	srcHeight int
	srcPixFmt astiav.PixelFormat
	frameIdx  int64
	started   bool
}

func NewEncoder(cfg EncoderConfig, outputPath string) (*Encoder, error) {
	if cfg.FPS <= 0 {
		return nil, errors.New("encoder fps must be > 0")
	}
	return &Encoder{cfg: cfg, outputPath: outputPath}, nil
}

// Encode scales one decoded frame and feeds it to the HEVC encoder.
func (e *Encoder) Encode(src *astiav.Frame) error {
	if !e.started {
		if err := e.initFromFrame(src); err != nil {
			return err
		}
	} else if src.Width() != e.srcWidth || src.Height() != e.srcHeight || src.PixelFormat() != e.srcPixFmt {
		if err := e.rebuildScaler(src); err != nil {
			return err
		}
	}

	e.dstFrame.Unref()
	if err := e.scaler.ScaleFrame(src, e.dstFrame); err != nil {
		return fmt.Errorf("scaling frame: %w", err)
	}
	e.dstFrame.SetPts(e.frameIdx)
	e.frameIdx++

	if err := e.codecContext.SendFrame(e.dstFrame); err != nil {
		return fmt.Errorf("sending frame to encoder: %w", err)
	}
	return e.drain()
}

// Close flushes the encoder, writes the MP4 trailer and releases resources.
func (e *Encoder) Close() error {
	if !e.started {
		return errors.New("encoder was never started")
	}
	if err := e.codecContext.SendFrame(nil); err != nil {
		return fmt.Errorf("flushing encoder: %w", err)
	}
	if err := e.drain(); err != nil {
		return err
	}
	if err := e.formatContext.WriteTrailer(); err != nil {
		return fmt.Errorf("writing trailer: %w", err)
	}

	e.packet.Free()
	e.dstFrame.Free()
	e.scaler.Free()
	e.codecContext.Free()
	if e.ioContext != nil {
		_ = e.ioContext.Close()
	}
	e.formatContext.Free()
	return nil
}

func (e *Encoder) initFromFrame(src *astiav.Frame) error {
	e.srcWidth = src.Width()
	e.srcHeight = src.Height()
	e.srcPixFmt = src.PixelFormat()
	if e.srcWidth <= 0 || e.srcHeight <= 0 {
		return fmt.Errorf("invalid frame dimensions %dx%d", e.srcWidth, e.srcHeight)
	}

	// MP4 output format context (muxer guessed from the .mp4 extension).
	formatContext, err := astiav.AllocOutputFormatContext(nil, "", e.outputPath)
	if err != nil {
		return fmt.Errorf("allocating output context: %w", err)
	}
	if formatContext == nil {
		return errors.New("output format context is nil")
	}
	e.formatContext = formatContext
	globalHeader := formatContext.OutputFormat().Flags().Has(astiav.IOFormatFlagGlobalheader)

	if err := e.openCodec(globalHeader); err != nil {
		return err
	}
	log.Printf("using encoder: %s (%dx%d, %s)", e.codecName, e.srcWidth, e.srcHeight, e.dstPixFmt.Name())

	// Output stream.
	e.stream = e.formatContext.NewStream(nil)
	if e.stream == nil {
		return errors.New("could not create output stream")
	}
	if err := e.stream.CodecParameters().FromCodecContext(e.codecContext); err != nil {
		return fmt.Errorf("copying codec parameters: %w", err)
	}
	e.stream.SetTimeBase(e.codecContext.TimeBase())

	// Output file IO.
	if !e.formatContext.OutputFormat().Flags().Has(astiav.IOFormatFlagNofile) {
		ioContext, err := astiav.OpenIOContext(e.outputPath, astiav.NewIOContextFlags(astiav.IOContextFlagWrite), nil, nil)
		if err != nil {
			return fmt.Errorf("opening output file %s: %w", e.outputPath, err)
		}
		e.ioContext = ioContext
		e.formatContext.SetPb(ioContext)
	}
	// negative_cts_offsets avoids an MP4 edit list that would otherwise trim a
	// content frame when B-frames produce a negative starting DTS.
	muxerOptions := astiav.NewDictionary()
	_ = muxerOptions.Set("movflags", "+negative_cts_offsets", astiav.NewDictionaryFlags())
	err = e.formatContext.WriteHeader(muxerOptions)
	muxerOptions.Free()
	if err != nil {
		return fmt.Errorf("writing header: %w", err)
	}

	scaler, err := newScaler(e.srcWidth, e.srcHeight, e.srcPixFmt, e.srcWidth, e.srcHeight, e.dstPixFmt)
	if err != nil {
		return err
	}
	e.scaler = scaler
	e.dstFrame = astiav.AllocFrame()
	e.packet = astiav.AllocPacket()
	e.started = true
	return nil
}

// openCodec probes the candidate HEVC encoders and opens the first usable one.
func (e *Encoder) openCodec(globalHeader bool) error {
	candidates := hevcCandidates
	if e.cfg.Codec != "" {
		candidates = []string{e.cfg.Codec}
	}

	var lastErr error
	for _, name := range candidates {
		codec := astiav.FindEncoderByName(name)
		if codec == nil {
			lastErr = fmt.Errorf("encoder %q not compiled into ffmpeg", name)
			continue
		}
		pixFmt := pickPixelFormat(codec)

		codecContext := astiav.AllocCodecContext(codec)
		if codecContext == nil {
			lastErr = errors.New("could not allocate codec context")
			continue
		}
		codecContext.SetWidth(e.srcWidth)
		codecContext.SetHeight(e.srcHeight)
		codecContext.SetPixelFormat(pixFmt)
		codecContext.SetTimeBase(astiav.NewRational(1, e.cfg.FPS))
		codecContext.SetFramerate(astiav.NewRational(e.cfg.FPS, 1))
		if globalHeader {
			codecContext.SetFlags(codecContext.Flags().Add(astiav.CodecContextFlagGlobalHeader))
		}
		if maxrate, ok := parseBitrate(e.cfg.Maxrate); ok {
			codecContext.SetRateControlMaxRate(maxrate)
			bufsize := maxrate
			if bv, ok := parseBitrate(e.cfg.Bufsize); ok {
				bufsize = bv
			}
			codecContext.SetRateControlBufferSize(int(bufsize))
		}

		options := e.buildOptions(name)
		err := codecContext.Open(codec, options)
		options.Free()
		if err != nil {
			codecContext.Free()
			lastErr = fmt.Errorf("opening encoder %q: %w", name, err)
			if e.cfg.Codec == "" {
				log.Printf("encoder %q unavailable, trying next: %v", name, err)
			}
			continue
		}

		e.codecContext = codecContext
		e.codecName = name
		e.dstPixFmt = pixFmt
		return nil
	}
	return fmt.Errorf("no usable HEVC encoder found: %w", lastErr)
}

// buildOptions maps config quality settings to libav encoder options. Extra
// opts from the config are layered on top so users can override defaults.
func (e *Encoder) buildOptions(codecName string) *astiav.Dictionary {
	d := rateControlOptions(codecName, e.cfg.CRF)
	noFlags := astiav.NewDictionaryFlags()
	for k, v := range e.cfg.ExtraOpts {
		_ = d.Set(k, v, noFlags)
	}
	return d
}

func (e *Encoder) rebuildScaler(src *astiav.Frame) error {
	e.srcWidth = src.Width()
	e.srcHeight = src.Height()
	e.srcPixFmt = src.PixelFormat()
	e.scaler.Free()
	scaler, err := newScaler(e.srcWidth, e.srcHeight, e.srcPixFmt,
		e.codecContext.Width(), e.codecContext.Height(), e.dstPixFmt)
	if err != nil {
		return err
	}
	e.scaler = scaler
	return nil
}

// drain pulls every available packet out of the encoder and muxes it.
func (e *Encoder) drain() error {
	for {
		err := e.codecContext.ReceivePacket(e.packet)
		if errors.Is(err, astiav.ErrEagain) || errors.Is(err, astiav.ErrEof) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("receiving packet from encoder: %w", err)
		}
		e.packet.SetStreamIndex(e.stream.Index())
		// One frame's worth of duration (codec time base is 1/fps); an explicit
		// duration on every packet keeps the MP4 edit list from coming up short.
		e.packet.SetDuration(1)
		e.packet.RescaleTs(e.codecContext.TimeBase(), e.stream.TimeBase())
		writeErr := e.formatContext.WriteInterleavedFrame(e.packet)
		e.packet.Unref()
		if writeErr != nil {
			return fmt.Errorf("writing packet: %w", writeErr)
		}
	}
}

func newScaler(srcW, srcH int, srcFmt astiav.PixelFormat, dstW, dstH int, dstFmt astiav.PixelFormat) (*astiav.SoftwareScaleContext, error) {
	scaler, err := astiav.CreateSoftwareScaleContext(
		srcW, srcH, srcFmt,
		dstW, dstH, dstFmt,
		astiav.NewSoftwareScaleContextFlags(astiav.SoftwareScaleContextFlagBilinear),
	)
	if err != nil {
		return nil, fmt.Errorf("creating scaler: %w", err)
	}
	return scaler, nil
}

// pickPixelFormat chooses an 8-bit 4:2:0 format the encoder accepts.
func pickPixelFormat(codec *astiav.Codec) astiav.PixelFormat {
	formats := codec.SupportedPixelFormats()
	if len(formats) == 0 {
		return astiav.PixelFormatYuv420P
	}
	for _, f := range formats {
		if f == astiav.PixelFormatYuv420P {
			return f
		}
	}
	for _, f := range formats {
		if f == astiav.PixelFormatNv12 {
			return f
		}
	}
	return formats[0]
}

// parseBitrate converts strings like "20M" / "500k" into bits per second.
func parseBitrate(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	multiplier := int64(1)
	switch s[len(s)-1] {
	case 'k', 'K':
		multiplier = 1_000
		s = s[:len(s)-1]
	case 'm', 'M':
		multiplier = 1_000_000
		s = s[:len(s)-1]
	case 'g', 'G':
		multiplier = 1_000_000_000
		s = s[:len(s)-1]
	}
	value, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		log.Printf("warning: invalid bitrate %q, ignoring", s)
		return 0, false
	}
	return value * multiplier, true
}
