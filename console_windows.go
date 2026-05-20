package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modkernel32 = windows.NewLazySystemDLL("kernel32.dll")
	moduser32   = windows.NewLazySystemDLL("user32.dll")

	procGetConsoleWindow      = modkernel32.NewProc("GetConsoleWindow")
	procGetConsoleProcessList = modkernel32.NewProc("GetConsoleProcessList")
	procShowWindow            = moduser32.NewProc("ShowWindow")
)

const swHide = 0

// hideOwnConsole hides the console window, but only when this process is the
// sole owner of it — i.e. it was launched by double-click and Windows created a
// fresh console for it. When launched from an existing terminal the console is
// shared, so hiding it would also hide the user's terminal.
func hideOwnConsole() {
	hwnd, _, _ := procGetConsoleWindow.Call()
	if hwnd == 0 {
		return
	}
	var pids [2]uint32
	n, _, _ := procGetConsoleProcessList.Call(
		uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	if n != 1 {
		return
	}
	_, _, _ = procShowWindow.Call(hwnd, swHide)
}
