package gui

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32              = windows.NewLazySystemDLL("user32.dll")
	procFindWindowW     = user32.NewProc("FindWindowW")
	procShowWindow      = user32.NewProc("ShowWindow")
	procSetForeground   = user32.NewProc("SetForegroundWindow")
)

const (
	swMinimize = 6
	swRestore  = 9
)

func findHwnd(title string) uintptr {
	p, err := windows.UTF16PtrFromString(title)
	if err != nil {
		return 0
	}
	h, _, _ := procFindWindowW.Call(0, uintptr(unsafe.Pointer(p)))
	return h
}

func minimizeHwnd(hwnd uintptr) {
	if hwnd != 0 {
		procShowWindow.Call(hwnd, swMinimize)
	}
}

func restoreHwnd(hwnd uintptr) {
	if hwnd != 0 {
		procShowWindow.Call(hwnd, swRestore)
		procSetForeground.Call(hwnd)
	}
}
