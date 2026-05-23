package gui

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"github.com/ncruces/zenity"

	"framekit/internal/pipeline"
)

const deviceAuto = "自动 (探测最佳编码器)"

type ui struct {
	configPath string
	win        fyne.Window

	sizeMB     *widget.Entry
	mountPoint *widget.Entry
	label      *widget.Entry

	device     *widget.Select
	fps        *widget.Entry
	crf        *widget.Entry
	maxrate    *widget.Entry
	bufsize    *widget.Entry
	skipFrames *widget.Entry

	outputDir       *widget.Entry
	filenamePattern *widget.Entry
	totalFrames     *widget.Entry
	hibernate       *widget.Check

	pattern          *widget.Entry
	pollInterval     *widget.Entry
	noFrameTimeout   *widget.Entry
	minimizeOnStart  *widget.Check

	startBtn *widget.Button
	stopBtn  *widget.Button
	saveBtn  *widget.Button
	progress *widget.ProgressBar

	logLabel  *widget.Label
	logScroll *container.Scroll
	logText   string

	encoders        []pipeline.EncoderInfo
	loadedExtraOpts map[string]string

	logCh  chan string
	cancel context.CancelFunc
	done   chan struct{}
}

// Run opens the framekit GUI. When autostart is true (used by the elevated
// relaunch) the run begins as soon as the window is shown.
func Run(configPath string, autostart bool) {
	a := app.NewWithID("io.framekit.app")
	w := a.NewWindow("framekit — MMD 帧序列转视频")

	u := &ui{
		configPath: configPath,
		win:        w,
		logCh:      make(chan string, 256),
	}
	u.encoders = pipeline.AvailableEncoders()

	cfg, loadErr := pipeline.LoadConfig(configPath)
	if loadErr != nil {
		cfg = pipeline.DefaultConfig()
	}

	u.build()
	u.loadInto(cfg)
	w.SetContent(u.content())
	w.Resize(fyne.NewSize(660, 770))
	w.CenterOnScreen()
	w.SetCloseIntercept(u.onClose)

	log.SetOutput(logWriter{ch: u.logCh})
	go u.logPump()

	if loadErr != nil {
		log.Printf("未找到配置 %s，已载入默认值", configPath)
	} else {
		log.Printf("已载入配置 %s", configPath)
	}

	if autostart {
		go func() { fyne.Do(u.autoStart) }()
	}

	w.ShowAndRun()
}

func (u *ui) build() {
	u.sizeMB = widget.NewEntry()
	u.mountPoint = widget.NewEntry()
	u.mountPoint.SetPlaceHolder(`盘符 R / 目录 R:\Frames`)
	u.label = widget.NewEntry()

	deviceOpts := []string{deviceAuto}
	for _, e := range u.encoders {
		if e.Available {
			deviceOpts = append(deviceOpts, e.Label)
		}
	}
	u.device = widget.NewSelect(deviceOpts, nil)

	u.fps = widget.NewEntry()
	u.crf = widget.NewEntry()
	u.maxrate = widget.NewEntry()
	u.maxrate.SetPlaceHolder("可选，如 20M")
	u.bufsize = widget.NewEntry()
	u.bufsize.SetPlaceHolder("可选，如 40M")
	u.skipFrames = widget.NewEntry()

	u.outputDir = widget.NewEntry()
	u.filenamePattern = widget.NewEntry()
	u.totalFrames = widget.NewEntry()
	u.hibernate = widget.NewCheck("", nil)

	u.pattern = widget.NewEntry()
	u.pattern.SetPlaceHolder(`留空自动识别；如 ^(\d+)_9\.bmp$`)
	u.pollInterval = widget.NewEntry()
	u.noFrameTimeout = widget.NewEntry()
	u.minimizeOnStart = widget.NewCheck("", nil)
	u.minimizeOnStart.SetChecked(true)

	u.startBtn = widget.NewButton("开始", u.onStart)
	u.startBtn.Importance = widget.HighImportance
	u.stopBtn = widget.NewButton("停止", u.onStop)
	u.stopBtn.Disable()
	u.saveBtn = widget.NewButton("保存配置", u.onSave)

	u.progress = widget.NewProgressBar()

	u.logLabel = widget.NewLabel("")
	u.logLabel.Wrapping = fyne.TextWrapWord
	u.logScroll = container.NewVScroll(u.logLabel)
}

func (u *ui) content() fyne.CanvasObject {
	withBrowse := func(e *widget.Entry, title string) fyne.CanvasObject {
		btn := widget.NewButton("浏览", func() {
			go func() {
				path, err := zenity.SelectFile(
					zenity.Title(title),
					zenity.Directory(),
				)
				if err == nil && path != "" {
					fyne.Do(func() { e.SetText(path) })
				}
			}()
		})
		return container.NewBorder(nil, nil, nil, btn, e)
	}

	ramTab := widget.NewForm(
		widget.NewFormItem("虚拟盘大小 (MB)", u.sizeMB),
		widget.NewFormItem("挂载点", withBrowse(u.mountPoint, "选择挂载目录")),
		widget.NewFormItem("卷标", u.label),
	)
	encTab := widget.NewForm(
		widget.NewFormItem("渲染设备", u.device),
		widget.NewFormItem("帧率 FPS", u.fps),
		widget.NewFormItem("画质 CRF/QP", u.crf),
		widget.NewFormItem("最大码率", u.maxrate),
		widget.NewFormItem("缓冲区大小", u.bufsize),
		widget.NewFormItem("跳过起始帧", u.skipFrames),
	)
	outTab := widget.NewForm(
		widget.NewFormItem("输出目录", withBrowse(u.outputDir, "选择输出目录")),
		widget.NewFormItem("文件名模式", u.filenamePattern),
		widget.NewFormItem("总帧数", u.totalFrames),
		widget.NewFormItem("完成后休眠", u.hibernate),
	)
	monTab := widget.NewForm(
		widget.NewFormItem("轮询间隔 (ms)", u.pollInterval),
		widget.NewFormItem("无新帧超时 (s)", u.noFrameTimeout),
		widget.NewFormItem("文件名正则 (高级)", u.pattern),
		widget.NewFormItem("启动后自动最小化", u.minimizeOnStart),
	)

	tabs := container.NewAppTabs(
		container.NewTabItem("RAM 盘", ramTab),
		container.NewTabItem("编码", encTab),
		container.NewTabItem("输出", outTab),
		container.NewTabItem("监控", monTab),
	)

	controls := container.NewGridWithColumns(3, u.startBtn, u.stopBtn, u.saveBtn)

	header := container.NewVBox(
		tabs,
		controls,
		u.progress,
		widget.NewLabelWithStyle(u.elevationHint(), fyne.TextAlignLeading, fyne.TextStyle{Italic: true}),
		widget.NewSeparator(),
		widget.NewLabelWithStyle("日志", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
	)
	return container.NewBorder(header, nil, nil, nil, u.logScroll)
}

func (u *ui) elevationHint() string {
	if pipeline.IsElevated() {
		return "已具备管理员权限。"
	}
	return "提示：点击「开始」时会弹出 UAC 请求管理员权限。"
}

func (u *ui) loadInto(cfg pipeline.Config) {
	u.loadedExtraOpts = cfg.Encoder.ExtraOpts

	u.sizeMB.SetText(strconv.Itoa(cfg.Ramdisk.SizeMB))
	u.mountPoint.SetText(cfg.Ramdisk.MountPoint)
	u.label.SetText(cfg.Ramdisk.Label)

	u.device.SetSelected(u.deviceLabelFor(cfg.Encoder.Codec))
	u.fps.SetText(strconv.Itoa(cfg.Encoder.FPS))
	u.crf.SetText(strconv.Itoa(cfg.Encoder.CRF))
	u.maxrate.SetText(cfg.Encoder.Maxrate)
	u.bufsize.SetText(cfg.Encoder.Bufsize)
	u.skipFrames.SetText(strconv.Itoa(cfg.Encoder.SkipFrames))

	u.outputDir.SetText(cfg.Output.Dir)
	u.filenamePattern.SetText(cfg.Output.FilenamePattern)
	u.totalFrames.SetText(strconv.Itoa(cfg.Output.TotalFrames))
	u.hibernate.SetChecked(cfg.Output.Hibernate)

	u.pattern.SetText(cfg.Monitor.Pattern)
	u.pollInterval.SetText(strconv.Itoa(cfg.Monitor.PollIntervalMS))
	u.noFrameTimeout.SetText(strconv.Itoa(cfg.Monitor.NoNewFrameTimeoutS))
}

// collect builds a Config from the form fields and validates it.
func (u *ui) collect() (pipeline.Config, error) {
	cfg := pipeline.DefaultConfig()
	var err error
	atoi := func(s, field string) int {
		if err != nil {
			return 0
		}
		v, e := strconv.Atoi(strings.TrimSpace(s))
		if e != nil {
			err = fmt.Errorf("「%s」必须是整数", field)
		}
		return v
	}

	cfg.Ramdisk.SizeMB = atoi(u.sizeMB.Text, "虚拟盘大小")
	mp, mErr := pipeline.NormalizeMountPoint(u.mountPoint.Text)
	if mErr != nil && err == nil {
		err = mErr
	}
	cfg.Ramdisk.MountPoint = mp
	cfg.Ramdisk.Label = strings.TrimSpace(u.label.Text)

	cfg.Encoder.Codec = u.codecForDevice(u.device.Selected)
	cfg.Encoder.FPS = atoi(u.fps.Text, "帧率")
	cfg.Encoder.CRF = atoi(u.crf.Text, "画质 CRF/QP")
	cfg.Encoder.Maxrate = strings.TrimSpace(u.maxrate.Text)
	cfg.Encoder.Bufsize = strings.TrimSpace(u.bufsize.Text)
	cfg.Encoder.SkipFrames = atoi(u.skipFrames.Text, "跳过起始帧")
	cfg.Encoder.ExtraOpts = u.loadedExtraOpts
	if cfg.Encoder.ExtraOpts == nil {
		cfg.Encoder.ExtraOpts = map[string]string{}
	}

	cfg.Output.Dir = strings.TrimSpace(u.outputDir.Text)
	cfg.Output.FilenamePattern = strings.TrimSpace(u.filenamePattern.Text)
	cfg.Output.TotalFrames = atoi(u.totalFrames.Text, "总帧数")
	cfg.Output.Hibernate = u.hibernate.Checked

	cfg.Monitor.Pattern = strings.TrimSpace(u.pattern.Text)
	cfg.Monitor.PollIntervalMS = atoi(u.pollInterval.Text, "轮询间隔")
	cfg.Monitor.NoNewFrameTimeoutS = atoi(u.noFrameTimeout.Text, "无新帧超时")

	if err == nil {
		switch {
		case cfg.Ramdisk.SizeMB <= 0:
			err = errors.New("虚拟盘大小必须大于 0")
		case cfg.Encoder.FPS <= 0:
			err = errors.New("帧率必须大于 0")
		case cfg.Output.TotalFrames <= 0:
			err = errors.New("总帧数必须大于 0")
		case cfg.Output.Dir == "":
			err = errors.New("输出目录不能为空")
		case cfg.Output.FilenamePattern == "":
			err = errors.New("文件名模式不能为空")
		case !strings.Contains(cfg.Output.FilenamePattern, "{n}"):
			err = errors.New("文件名模式必须包含 {n}（用于生成不重名的序号）")
		}
	}
	return cfg, err
}

func (u *ui) onSave() {
	cfg, err := u.collect()
	if err != nil {
		dialog.ShowError(err, u.win)
		return
	}
	if err := cfg.Save(u.configPath); err != nil {
		dialog.ShowError(err, u.win)
		return
	}
	u.loadedExtraOpts = cfg.Encoder.ExtraOpts
	log.Printf("配置已保存到 %s", u.configPath)
	dialog.ShowInformation("已保存", "配置已写入 "+u.configPath, u.win)
}

func (u *ui) onStart() {
	cfg, err := u.collect()
	if err != nil {
		dialog.ShowError(err, u.win)
		return
	}
	status, ierr := pipeline.InspectMountPoint(cfg.Ramdisk.MountPoint)
	if ierr != nil {
		dialog.ShowError(ierr, u.win)
		return
	}
	switch status {
	case pipeline.MountDriveInUse:
		dialog.ShowError(fmt.Errorf("盘符 %s 已被占用，请改用其他挂载点", cfg.Ramdisk.MountPoint), u.win)
		return
	case pipeline.MountDirNonEmpty:
		dialog.ShowConfirm("挂载点非空",
			fmt.Sprintf("目录 %s 中已有文件。\n挂载 RAM 盘会暂时遮蔽其中的内容（卸载后恢复）。\n\n确定继续吗？", cfg.Ramdisk.MountPoint),
			func(ok bool) {
				if ok {
					u.proceed(cfg)
				}
			}, u.win)
		return
	}
	u.proceed(cfg)
}

// autoStart is the entry point for the elevated relaunch: the non-empty-mount
// warning was already accepted in the non-elevated instance, so only an in-use
// drive still blocks here.
func (u *ui) autoStart() {
	cfg, err := u.collect()
	if err != nil {
		dialog.ShowError(err, u.win)
		return
	}
	if status, _ := pipeline.InspectMountPoint(cfg.Ramdisk.MountPoint); status == pipeline.MountDriveInUse {
		dialog.ShowError(fmt.Errorf("盘符 %s 已被占用，请改用其他挂载点", cfg.Ramdisk.MountPoint), u.win)
		return
	}
	u.startRun(cfg)
}

// proceed runs the pipeline directly when already elevated, otherwise saves the
// config and relaunches an elevated instance (scheme A).
func (u *ui) proceed(cfg pipeline.Config) {
	if pipeline.IsElevated() {
		u.startRun(cfg)
		return
	}
	if err := cfg.Save(u.configPath); err != nil {
		dialog.ShowError(fmt.Errorf("保存配置失败：%w", err), u.win)
		return
	}
	if err := relaunchElevated(u.configPath); err != nil {
		if isUserCancelledUAC(err) {
			dialog.ShowInformation("已取消", "未获得管理员权限，操作已取消。", u.win)
		} else {
			dialog.ShowError(fmt.Errorf("提权启动失败：%w", err), u.win)
		}
		return
	}
	// The elevated instance is now starting; hand off by closing this one.
	u.win.Close()
}

func (u *ui) startRun(cfg pipeline.Config) {
	ctx, cancel := context.WithCancel(context.Background())
	u.cancel = cancel
	u.done = make(chan struct{})

	u.startBtn.Disable()
	u.saveBtn.Disable()
	u.stopBtn.Enable()
	u.progress.SetValue(0)
	log.Println("====== 开始 ======")

	var hwnd uintptr
	if u.minimizeOnStart.Checked {
		hwnd = findHwnd(u.win.Title())
		go func() {
			time.Sleep(300 * time.Millisecond)
			minimizeHwnd(hwnd)
		}()
	}

	go func() {
		defer close(u.done)
		lastPermille := -1
		err := pipeline.Run(ctx, cfg, func(done, total int) {
			if total <= 0 {
				return
			}
			pm := done * 1000 / total
			if pm == lastPermille {
				return
			}
			lastPermille = pm
			v := float64(done) / float64(total)
			fyne.Do(func() { u.progress.SetValue(v) })
		})
		fyne.Do(func() {
			u.cancel = nil
			u.startBtn.Enable()
			u.saveBtn.Enable()
			u.stopBtn.Disable()
			if hwnd != 0 {
				restoreHwnd(hwnd)
			}
			switch {
			case err == nil:
				u.progress.SetValue(1)
				dialog.ShowInformation("完成", "视频已生成。", u.win)
			case errors.Is(err, context.Canceled):
				log.Println("已停止，RAM 盘已清理")
			default:
				dialog.ShowError(err, u.win)
			}
		})
	}()
}

func (u *ui) onStop() {
	if u.cancel != nil {
		log.Println("正在停止...")
		u.stopBtn.Disable()
		u.cancel()
	}
}

func (u *ui) onClose() {
	if u.cancel == nil {
		u.win.Close()
		return
	}
	dialog.ShowConfirm("正在运行",
		"任务尚未完成，退出将停止并清理 RAM 盘。确定退出吗？",
		func(ok bool) {
			if !ok {
				return
			}
			done := u.done
			u.cancel()
			go func() {
				<-done
				fyne.Do(u.win.Close)
			}()
		}, u.win)
}

func (u *ui) deviceLabelFor(codec string) string {
	if codec == "" {
		return deviceAuto
	}
	for _, e := range u.encoders {
		if e.Name == codec && e.Available {
			return e.Label
		}
	}
	return deviceAuto
}

func (u *ui) codecForDevice(label string) string {
	if label == "" || label == deviceAuto {
		return ""
	}
	for _, e := range u.encoders {
		if e.Label == label {
			return e.Name
		}
	}
	return ""
}

// logWriter funnels standard-library log output into the GUI log channel.
type logWriter struct{ ch chan string }

func (w logWriter) Write(p []byte) (int, error) {
	select {
	case w.ch <- string(p):
	default: // drop when the GUI is backed up; never block the logger
	}
	return len(p), nil
}

func (u *ui) logPump() {
	for line := range u.logCh {
		l := line
		fyne.Do(func() { u.appendLog(l) })
	}
}

func (u *ui) appendLog(s string) {
	text := u.logText + s
	if lines := strings.Split(text, "\n"); len(lines) > 400 {
		text = strings.Join(lines[len(lines)-400:], "\n")
	}
	u.logText = text
	u.logLabel.SetText(text)
	u.logScroll.ScrollToBottom()
}
