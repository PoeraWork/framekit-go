# framekit

MikuMikuDance 帧序列 → HEVC 视频的实时转码流水线，Windows 专用。

MMD 渲染时逐帧写 PNG 到磁盘，framekit 把这个目录挂成 RAM 盘，边等边消化每一帧，实时压制成 MP4，完成后自动卸载 RAM 盘。

## 前置依赖

| 依赖                                                               | 说明                                              |
| ------------------------------------------------------------------ | ------------------------------------------------- |
| [ImDisk Toolkit](https://sourceforge.net/projects/imdisk-toolkit/) | RAM 盘驱动，framekit 通过 `imdisk.exe` 管理虚拟盘 |
| FFmpeg 运行时 DLL                                                  | 与 `framekit.exe` 放在同一目录，发布包已包含      |

> ImDisk 的创建/卸载操作需要管理员权限。GUI 模式下点击「开始」时会自动弹出 UAC；CLI 模式需在管理员终端中运行。

## 使用方式

### 图形界面（推荐）

直接双击 `framekit.exe`，或：

```
framekit gui [-c config.toml]
```

在界面中配置参数后点击「开始」，UAC 提示出现后确认，流水线即启动。

### 命令行

```
# 生成默认配置文件
framekit init [-c config.toml] [-f]

# 运行流水线
framekit run  [-c config.toml] [-y]
```

`-y` 跳过挂载点非空的二次确认提示。

## 工作原理

```
MMD 渲染进程
    │ 写 frame0001.png, frame0002.png ...
    ▼
  RAM 盘 (ImDisk)
    │ Monitor 以 50ms 间隔轮询，按序号等帧出现
    ▼
  libavcodec  解码 PNG → 原始像素帧
    │
    ▼
  libswscale  色彩空间转换 (RGB → YUV420P / NV12)
    │
    ▼
  HEVC 编码器  libx265 / hevc_amf / hevc_nvenc / hevc_qsv
    │ 每帧编码后立即捞出已攒够的压缩包写入文件（drain）
    │ 结束时冲洗编码器缓冲，写 MP4 trailer
    ▼
  output_{n}.mp4
```

**流式处理**：来一帧处理一帧，处理完立即从 RAM 盘删除，RAM 盘占用量接近于零。

**编码器自动探测**：启动时依次尝试 `hevc_nvenc → hevc_amf → hevc_qsv → libx265`，以第一个能实际打开的为准。GUI 中只展示当前机器可用的选项。

**B 帧禁用**：硬件编码器（AMF / NVENC / QSV）默认以 `bf=0` 关闭 B 帧。GPU 显存里的参考帧在特定驱动版本下会发生静默损坏，导致依赖该参考帧的后续帧全部绿屏；禁用 B 帧后每帧只依赖 I/P 帧，彻底消除这条故障路径，代价是文件体积略增（约 5%）。如需开启，在 `[encoder.extra_opts]` 中加 `bf = "2"` 即可覆盖。

**编码缓冲**：`SendFrame` 之后不一定立刻产出压缩包。`drain` 在每帧编码后都会调用，把当前已攒够的包全部写进 MP4；最后 `Close` 时发送 flush 信号再 drain 一次，确保尾帧不丢。

## 配置文件

默认路径为 `config.toml`，与 `framekit.exe` 同目录。GUI 的「保存配置」按钮会覆盖写入该文件。

```toml
[ramdisk]
size_mb = 2048
mount_point = "R"          # "R" / "R:" → 盘符；"R:/Frames" → 目录挂载
label = "FrameDisk"

[encoder]
# codec = "libx265"        # 留空则自动探测最佳编码器
fps = 60
crf = 18                   # 画质，越小越好（libx265 为 CRF，硬件编码器为 QP）
# maxrate = "20M"          # 最大码率（可选）
# bufsize = "40M"          # 码率缓冲，通常为 maxrate 的 2 倍
skip_frames = 0            # 跳过片头前 N 帧不编码

[encoder.extra_opts]       # 透传给编码器的额外键值对，如 preset = "medium"

[output]
dir = "D:/Videos"
filename_pattern = "{n}.mp4"   # {n} 为自增序号，防止覆盖已有文件
total_frames = 18000            # 预期总帧数（用于进度条和超时判断）
hibernate = false               # 完成后是否休眠

[monitor]
# pattern = ""                  # 高级：留空时从首帧文件名自动识别（前缀/位数/扩展名/起始序号）
                                # 仅当递增数字不在文件名末尾时才需要设置，如：^(\d+)_9\.bmp$
poll_interval_ms = 50           # 轮询间隔
no_new_frame_timeout_s = 60     # 超过此时间没有新帧则视为结束
```

## 构建

### 本地构建

从 [BtbN/FFmpeg-Builds](https://github.com/BtbN/FFmpeg-Builds/releases) 下载 `ffmpeg-*-win64-gpl-shared-*.zip`，解压后将根目录（含 `bin/`、`include/`、`lib/` 的那层）重命名为 `ffmpeg-dev`，放到项目根目录下（与 `go.mod` 同级）。该目录已在 `.gitignore` 中排除。

还需要 MinGW-w64 工具链（CGO），推荐通过 [MSYS2](https://www.msys2.org/) 安装：

```bash
pacman -S mingw-w64-x86_64-gcc
```

构建：

```powershell
.\build.ps1
```

产物输出到 `dist\`，包含 `framekit.exe` 和所需的 FFmpeg 运行时 DLL，可直接压缩分发。

### CI

推送到 `main` 或 Pull Request 时自动触发构建和 `go vet`。推送 `vX.Y.Z` 格式的 tag 时自动创建 GitHub Release 并附上发布包：

```bash
git tag v1.0.0 && git push origin v1.0.0
```
