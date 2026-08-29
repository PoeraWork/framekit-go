package gui

import (
	"errors"
	"os"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// relaunchElevated starts a new, elevated instance of this program that opens
// the GUI and immediately begins the run (scheme A: UAC is only requested when
// the user actually clicks "开始"). It returns an error if the user declines the
// UAC prompt or the launch otherwise fails.
func relaunchElevated(configPath, debugLogPath string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		cwd = ""
	}

	args := "gui -autostart -c " + quoteArg(configPath)
	if debugLogPath != "" {
		args += " -debug-log " + quoteArg(debugLogPath)
	}

	verbPtr, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return err
	}
	exePtr, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return err
	}
	argsPtr, err := windows.UTF16PtrFromString(args)
	if err != nil {
		return err
	}
	var cwdPtr *uint16
	if cwd != "" {
		if cwdPtr, err = windows.UTF16PtrFromString(cwd); err != nil {
			return err
		}
	}

	// showCmd 1 == SW_SHOWNORMAL
	return windows.ShellExecute(0, verbPtr, exePtr, argsPtr, cwdPtr, 1)
}

// isUserCancelledUAC reports whether err is the "user declined elevation" case.
func isUserCancelledUAC(err error) bool {
	var errno syscall.Errno
	return errors.As(err, &errno) && errno == syscall.Errno(1223) // ERROR_CANCELLED
}

// quoteArg wraps a command-line argument in double quotes when it contains
// spaces, so the elevated process parses it as a single argument.
func quoteArg(s string) string {
	if s == "" {
		return `""`
	}
	if !strings.ContainsAny(s, " \t\"") {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}
