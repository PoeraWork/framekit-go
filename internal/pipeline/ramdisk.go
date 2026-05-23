package pipeline

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// IsElevated reports whether the current process has administrator privileges.
func IsElevated() bool {
	var sid *windows.SID
	err := windows.AllocateAndInitializeSid(
		&windows.SECURITY_NT_AUTHORITY, 2,
		windows.SECURITY_BUILTIN_DOMAIN_RID,
		windows.DOMAIN_ALIAS_RID_ADMINS,
		0, 0, 0, 0, 0, 0, &sid,
	)
	if err != nil {
		return false
	}
	defer windows.FreeSid(sid)
	ok, err := windows.GetCurrentProcessToken().IsMember(sid)
	return err == nil && ok
}

// findImdisk locates imdisk.exe, falling back to System32 since it is often
// not on PATH for non-admin shells.
func findImdisk() (string, error) {
	if p, err := exec.LookPath("imdisk"); err == nil {
		return p, nil
	}
	sysRoot := os.Getenv("SystemRoot")
	if sysRoot == "" {
		sysRoot = `C:\Windows`
	}
	candidate := filepath.Join(sysRoot, "System32", "imdisk.exe")
	if _, err := os.Stat(candidate); err == nil {
		return candidate, nil
	}
	return "", fmt.Errorf("imdisk not found. Install ImDisk Toolkit: https://sourceforge.net/projects/imdisk-toolkit/")
}

// Ramdisk creates and removes an ImDisk virtual drive.
type Ramdisk struct {
	cfg    RamdiskConfig
	imdisk string
}

func NewRamdisk(cfg RamdiskConfig) (*Ramdisk, error) {
	if !IsElevated() {
		return nil, errors.New("创建 ImDisk 虚拟盘需要管理员权限。\n请右键点击 framekit.exe → \"以管理员身份运行\"，或在管理员终端中执行。")
	}
	imdisk, err := findImdisk()
	if err != nil {
		return nil, err
	}
	return &Ramdisk{cfg: cfg, imdisk: imdisk}, nil
}

func (r *Ramdisk) isDriveLetter() bool {
	return len(r.cfg.MountPoint) == 2 && strings.HasSuffix(r.cfg.MountPoint, ":")
}

// Create mounts the RAM disk and returns its mount target.
func (r *Ramdisk) Create() (string, error) {
	target := r.cfg.MountPoint
	if r.isDriveLetter() {
		if _, err := os.Stat(target + "\\"); err == nil {
			return "", fmt.Errorf("drive %s is already in use — choose another mount_point or unmount first", target)
		}
	} else if err := os.MkdirAll(target, 0o755); err != nil {
		return "", fmt.Errorf("creating mount directory %s: %w", target, err)
	}

	cmd := exec.Command(r.imdisk,
		"-a",
		"-s", fmt.Sprintf("%dM", r.cfg.SizeMB),
		"-m", target,
		"-p", "/fs:ntfs /q /y",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("imdisk create failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return target, nil
}

// Remove unmounts the RAM disk.
func (r *Ramdisk) Remove() error {
	cmd := exec.Command(r.imdisk, "-D", "-m", r.cfg.MountPoint)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		// "Not a mount point" means the path was never mounted or already removed — safe to ignore.
		if strings.Contains(strings.ToLower(msg), "not a mount point") {
			return nil
		}
		return fmt.Errorf("imdisk remove failed: %w: %s", err, msg)
	}
	return nil
}
