package pipeline

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

type RamdiskConfig struct {
	SizeMB     int    `toml:"size_mb"`
	MountPoint string `toml:"mount_point"`
	Label      string `toml:"label"`
}

type EncoderConfig struct {
	Codec      string            `toml:"codec"`
	FPS        int               `toml:"fps"`
	CRF        int               `toml:"crf"`
	Maxrate    string            `toml:"maxrate"`
	Bufsize    string            `toml:"bufsize"`
	SkipFrames int               `toml:"skip_frames"`
	ExtraOpts  map[string]string `toml:"extra_opts"`
}

type OutputConfig struct {
	Dir             string `toml:"dir"`
	FilenamePattern string `toml:"filename_pattern"`
	TotalFrames     int    `toml:"total_frames"`
	Hibernate       bool   `toml:"hibernate"`
}

type MonitorConfig struct {
	FramePrefix        string `toml:"frame_prefix"`
	FrameDigits        int    `toml:"frame_digits"`
	PollIntervalMS     int    `toml:"poll_interval_ms"`
	NoNewFrameTimeoutS int    `toml:"no_new_frame_timeout_s"`
}

type Config struct {
	Ramdisk RamdiskConfig `toml:"ramdisk"`
	Encoder EncoderConfig `toml:"encoder"`
	Output  OutputConfig  `toml:"output"`
	Monitor MonitorConfig `toml:"monitor"`
}

func DefaultConfig() Config {
	return Config{
		Ramdisk: RamdiskConfig{SizeMB: 2048, MountPoint: "R", Label: "FrameDisk"},
		Encoder: EncoderConfig{FPS: 60, CRF: 18},
		Output:  OutputConfig{Dir: "D:\\Videos", FilenamePattern: "{n}.mp4"},
		Monitor: MonitorConfig{FramePrefix: "mmdrecord_", FrameDigits: 4, PollIntervalMS: 50, NoNewFrameTimeoutS: 60},
	}
}

// LoadConfig reads a TOML file on top of the built-in defaults, so any key the
// user omits keeps its default value.
func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()
	meta, err := toml.DecodeFile(path, &cfg)
	if err != nil {
		return Config{}, fmt.Errorf("loading config %s: %w", path, err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, k := range undecoded {
			keys[i] = k.String()
		}
		log.Printf("warning: unknown config keys ignored: %s", strings.Join(keys, ", "))
	}

	mountPoint, err := NormalizeMountPoint(cfg.Ramdisk.MountPoint)
	if err != nil {
		return Config{}, err
	}
	cfg.Ramdisk.MountPoint = mountPoint
	return cfg, nil
}

// Save writes the config to path as TOML. Comments from a hand-written config
// file are not preserved.
func (c Config) Save(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating config %s: %w", path, err)
	}
	defer f.Close()
	if err := toml.NewEncoder(f).Encode(c); err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}
	return nil
}

// NormalizeMountPoint reduces a raw mount point to either "X:" (drive letter)
// or "X:\path" (directory mount).
func NormalizeMountPoint(raw string) (string, error) {
	s := strings.ReplaceAll(strings.TrimSpace(raw), "/", "\\")
	switch {
	case len(s) == 1 && isAlpha(s[0]):
		return strings.ToUpper(s) + ":", nil
	case len(s) == 2 && isAlpha(s[0]) && s[1] == ':':
		return strings.ToUpper(s[:1]) + ":", nil
	case len(s) == 3 && isAlpha(s[0]) && s[1] == ':' && s[2] == '\\':
		return strings.ToUpper(s[:1]) + ":", nil
	case len(s) >= 4 && isAlpha(s[0]) && s[1] == ':' && s[2] == '\\':
		return s, nil
	}
	return "", fmt.Errorf("invalid mount_point %q: use a drive letter (R, R:, R:\\) or a full path (R:\\Frames)", raw)
}

func isAlpha(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

const DefaultConfigTOML = `[ramdisk]
size_mb = 2048
mount_point = "R"          # "R" / "R:" -> 盘符；"R:/Frames" -> 目录挂载
label = "FrameDisk"

[encoder]
# codec = "libx265"        # 取消注释可强制指定编码器 (libx265/hevc_amf/hevc_nvenc/hevc_qsv)
fps = 60
crf = 18
# maxrate = "20M"          # 最大码率（可选）
# bufsize = "40M"          # 缓冲区大小，通常为 maxrate 的 2 倍
skip_frames = 0            # 跳过前 N 帧不编码

[encoder.extra_opts]       # 额外编码器选项（键值对，值为字符串），如 preset = "medium"

[output]
dir = "D:/Videos"
filename_pattern = "{n}.mp4"
total_frames = 18000       # 总帧数（300秒 * 60fps）
hibernate = false          # 完成后是否休眠

[monitor]
frame_prefix = "mmdrecord_"
frame_digits = 4
poll_interval_ms = 50
no_new_frame_timeout_s = 60
`
