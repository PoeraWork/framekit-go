package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"

	"framekit/internal/gui"
	"framekit/internal/pipeline"
)

const defaultConfigPath = "config.toml"

func main() {
	log.SetFlags(log.LstdFlags)

	args := os.Args[1:]
	if len(args) == 0 {
		// Double-clicked from Explorer — launch the GUI.
		hideOwnConsole()
		gui.Run(defaultConfigPath, false)
		return
	}

	switch args[0] {
	case "gui":
		hideOwnConsole()
		cmdGUI(args[1:])
	case "init":
		cmdInit(args[1:])
	case "run":
		cmdRun(args[1:])
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "framekit — MMD 帧序列转视频流水线")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "用法:")
	fmt.Fprintln(os.Stderr, "  framekit                              启动图形界面")
	fmt.Fprintln(os.Stderr, "  framekit gui  [-c config.toml]        启动图形界面")
	fmt.Fprintln(os.Stderr, "  framekit init [-c config.toml] [-f]   生成默认配置文件")
	fmt.Fprintln(os.Stderr, "  framekit run  [-c config.toml] [-y]   运行帧转视频流水线")
}

func cmdGUI(args []string) {
	fs := flag.NewFlagSet("gui", flag.ExitOnError)
	configPath := fs.String("c", defaultConfigPath, "配置文件路径")
	autostart := fs.Bool("autostart", false, "启动后立即开始（提权重启时内部使用）")
	_ = fs.Parse(args)
	gui.Run(*configPath, *autostart)
}

func cmdInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	configPath := fs.String("c", defaultConfigPath, "配置文件路径")
	force := fs.Bool("f", false, "覆盖已存在的配置文件")
	_ = fs.Parse(args)

	if _, err := os.Stat(*configPath); err == nil && !*force {
		log.Fatalf("配置文件已存在: %s（使用 -f 覆盖）", *configPath)
	}
	if err := os.WriteFile(*configPath, []byte(pipeline.DefaultConfigTOML), 0o644); err != nil {
		log.Fatalf("写入配置失败: %v", err)
	}
	log.Printf("配置已写入 %s", *configPath)
}

func cmdRun(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	configPath := fs.String("c", defaultConfigPath, "配置文件路径")
	yes := fs.Bool("y", false, "对挂载点非空等警告自动确认")
	_ = fs.Parse(args)

	cfg, err := pipeline.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("%v", err)
	}

	status, err := pipeline.InspectMountPoint(cfg.Ramdisk.MountPoint)
	if err != nil {
		log.Fatalf("%v", err)
	}
	switch status {
	case pipeline.MountDriveInUse:
		log.Fatalf("盘符 %s 已被占用，请改用其他挂载点", cfg.Ramdisk.MountPoint)
	case pipeline.MountDirNonEmpty:
		prompt := fmt.Sprintf("目录 %s 非空，挂载 RAM 盘会暂时遮蔽其中内容。继续？", cfg.Ramdisk.MountPoint)
		if !*yes && !confirm(prompt) {
			log.Println("已取消")
			return
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		log.Println("收到中断信号，正在停止并清理 RAM 盘...")
		cancel()
	}()

	if err := pipeline.Run(ctx, cfg, nil); err != nil {
		if errors.Is(err, context.Canceled) {
			log.Println("已停止")
			os.Exit(1)
		}
		log.Fatalf("fatal error: %v", err)
	}
}

// confirm asks a yes/no question on stdin.
func confirm(prompt string) bool {
	fmt.Fprintf(os.Stderr, "%s [y/N] ", prompt)
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
	return answer == "y" || answer == "yes"
}
