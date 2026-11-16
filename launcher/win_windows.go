//go:build windows

package main

import (
	"os"
	"strings"
	"syscall"
	"unsafe"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")

	procMessageBoxW              = user32.NewProc("MessageBoxW")
	procEnumWindows              = user32.NewProc("EnumWindows")
	procGetWindowTextW           = user32.NewProc("GetWindowTextW")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	procIsWindowVisible          = user32.NewProc("IsWindowVisible")
	procShowWindow               = user32.NewProc("ShowWindow")
	procGetWindowLongPtrW        = user32.NewProc("GetWindowLongPtrW")
	procSetWindowLongPtrW        = user32.NewProc("SetWindowLongPtrW")
	procSetWindowPos             = user32.NewProc("SetWindowPos")
	procGetWindowRect            = user32.NewProc("GetWindowRect")
	procMonitorFromWindow        = user32.NewProc("MonitorFromWindow")
	procGetMonitorInfoW          = user32.NewProc("GetMonitorInfoW")
	procSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	procGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	procPostMessageW             = user32.NewProc("PostMessageW")
	procAllocConsole             = kernel32.NewProc("AllocConsole")
	procFreeConsole              = kernel32.NewProc("FreeConsole")
	procSetConsoleTitleW         = kernel32.NewProc("SetConsoleTitleW")
	procCreateMutexW             = kernel32.NewProc("CreateMutexW")
	procShellExecuteW            = shell32.NewProc("ShellExecuteW")
)

const (
	mbOK             = 0x0
	mbYesNo          = 0x4
	mbIconError      = 0x10
	mbIconQuest      = 0x20
	mbIconWarn       = 0x30
	mbIconInfo       = 0x40
	idYes            = 6
	gwlStyle         = -16
	wsCaption        = 0x00C00000
	wsThickFrame     = 0x00040000
	wsSysMenu        = 0x00080000
	wsMinimizeBox    = 0x00020000
	wsMaximizeBox    = 0x00010000
	swHide           = 0
	swShow           = 5
	swRestore        = 9
	swpFrameChgd     = 0x0020
	swpShowWindow    = 0x0040
	hwndTop          = 0
	monitorNearest   = 2
	wmClose          = 0x0010
	errAlreadyExists = 183
	createNoWindow   = 0x08000000
)

func u16(s string) *uint16 { p, _ := syscall.UTF16PtrFromString(s); return p }

func messageBox(title, text string, flags uintptr) int {
	r, _, _ := procMessageBoxW.Call(0, uintptr(unsafe.Pointer(u16(text))), uintptr(unsafe.Pointer(u16(title))), flags|0x00010000 /*MB_SETFOREGROUND*/)
	return int(r)
}

func showError(text string)     { messageBox("SmartTube", text, mbOK|mbIconError) }
func showInfo(text string)      { messageBox("SmartTube", text, mbOK|mbIconInfo) }
func askYesNo(text string) bool { return messageBox("SmartTube", text, mbYesNo|mbIconQuest) == idYes }

var consoleAllocated bool

// ensureConsole opens a console window so first-run progress is visible
// (the launcher itself is a GUI-subsystem program with no console).
func ensureConsole(title string) {
	if consoleAllocated {
		return
	}
	r, _, _ := procAllocConsole.Call()
	if r == 0 {
		return
	}
	consoleAllocated = true
	procSetConsoleTitleW.Call(uintptr(unsafe.Pointer(u16(title))))
	if f, err := os.OpenFile("CONOUT$", os.O_RDWR, 0); err == nil {
		os.Stdout = f
		os.Stderr = f
	}
	if f, err := os.OpenFile("CONIN$", os.O_RDWR, 0); err == nil {
		os.Stdin = f
	}
}

func closeConsole() {
	if consoleAllocated {
		procFreeConsole.Call()
		consoleAllocated = false
		os.Stdout, _ = os.Open(os.DevNull)
		os.Stderr = os.Stdout
	}
}

// singleInstance returns false if another launcher is already running.
func singleInstance() bool {
	_, _, err := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(u16(`Local\SmartTubeWindowsLauncher`))))
	if errno, ok := err.(syscall.Errno); ok && errno == errAlreadyExists {
		return false
	}
	return true
}

func shellExecuteAdmin(file, params string) bool {
	r, _, _ := procShellExecuteW.Call(0, uintptr(unsafe.Pointer(u16("runas"))), uintptr(unsafe.Pointer(u16(file))),
		uintptr(unsafe.Pointer(u16(params))), 0, swShow)
	return r > 32
}

type winInfo struct {
	hwnd  uintptr
	pid   uint32
	title string
	w, h  int32
}

func listWindows() []winInfo {
	var out []winInfo
	cb := syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
		vis, _, _ := procIsWindowVisible.Call(hwnd)
		if vis == 0 {
			return 1
		}
		var pid uint32
		procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		buf := make([]uint16, 256)
		procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), 256)
		var r [4]int32
		procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r[0])))
		out = append(out, winInfo{hwnd, pid, syscall.UTF16ToString(buf), r[2] - r[0], r[3] - r[1]})
		return 1
	})
	procEnumWindows.Call(cb, 0)
	return out
}

// findEmulatorWindow locates the emulator's main window ("Android Emulator - SmartTube:5554").
func findEmulatorWindow(port int) (winInfo, bool) {
	key := avdName + ":" + itoa(port)
	for _, w := range listWindows() {
		if strings.Contains(w.title, key) {
			return w, true
		}
	}
	return winInfo{}, false
}

// makeFullscreen hides the emulator's side toolbar and stretches the device
// window borderless over the whole monitor, without raising or focusing it
// (it stays hidden behind the start-up screen until revealWindow).
func hideEmulatorToolbar(main winInfo) {
	for _, w := range listWindows() {
		if w.pid == main.pid && w.hwnd != main.hwnd && w.w < 200 && w.h > w.w {
			procShowWindow.Call(w.hwnd, swHide) // extended-controls toolbar
		}
	}
}

func makeFullscreen(main winInfo) {
	hideEmulatorToolbar(main)
	style, _, _ := procGetWindowLongPtrW.Call(main.hwnd, uintptr(intptrNeg(gwlStyle)))
	style &^= wsCaption | wsThickFrame | wsSysMenu | wsMinimizeBox | wsMaximizeBox
	procSetWindowLongPtrW.Call(main.hwnd, uintptr(intptrNeg(gwlStyle)), style)
	mon, _, _ := procMonitorFromWindow.Call(main.hwnd, monitorNearest)
	var mi struct {
		cbSize  uint32
		monitor [4]int32
		work    [4]int32
		flags   uint32
	}
	mi.cbSize = uint32(unsafe.Sizeof(mi))
	procGetMonitorInfoW.Call(mon, uintptr(unsafe.Pointer(&mi)))
	m := mi.monitor
	procSetWindowPos.Call(main.hwnd, hwndTop, uintptr(m[0]), uintptr(m[1]), uintptr(m[2]-m[0]), uintptr(m[3]-m[1]), swpFrameChgd|swpShowWindow|swpNoZOrder|swpNoActivate)
}

const (
	swpNoZOrder   = 0x0004
	swpNoActivate = 0x0010
)

// revealWindow puts the (already full-screen) window on top and gives it focus.
func revealWindow(hwnd uintptr) {
	setTopmost(hwnd, true)
	focusWindow(hwnd)
}

var (
	procKeybdEvent         = user32.NewProc("keybd_event")
	procBringWindowToTop   = user32.NewProc("BringWindowToTop")
	procAttachThreadInput  = user32.NewProc("AttachThreadInput")
	procGetCurrentThreadId = kernel32.NewProc("GetCurrentThreadId")
)

// focusWindow brings hwnd to the front. Windows refuses SetForegroundWindow
// from background processes, so use the usual Alt-tap / AttachThreadInput workaround.
func focusWindow(hwnd uintptr) {
	procShowWindow.Call(hwnd, swRestore)
	fg, _, _ := procGetForegroundWindow.Call()
	if fg == hwnd {
		return
	}
	fgThread, _, _ := procGetWindowThreadProcessId.Call(fg, 0)
	me, _, _ := procGetCurrentThreadId.Call()
	if fgThread != 0 && fgThread != me {
		procAttachThreadInput.Call(me, fgThread, 1)
		defer procAttachThreadInput.Call(me, fgThread, 0)
	}
	procKeybdEvent.Call(0x12, 0, 0, 0)      // Alt down
	procKeybdEvent.Call(0x12, 0, 0x0002, 0) // Alt up
	procBringWindowToTop.Call(hwnd)
	procSetForegroundWindow.Call(hwnd)
	if fg2, _, _ := procGetForegroundWindow.Call(); fg2 != hwnd {
		procSwitchToThisWindow.Call(hwnd, 1)
	}
}

var (
	procSwitchToThisWindow         = user32.NewProc("SwitchToThisWindow")
	procAllowSetForegroundWindow   = user32.NewProc("AllowSetForegroundWindow")
	procGetCursorPos               = user32.NewProc("GetCursorPos")
	procSetCursorPos               = user32.NewProc("SetCursorPos")
	procOpenProcess                = kernel32.NewProc("OpenProcess")
	procCloseHandle                = kernel32.NewProc("CloseHandle")
	procQueryFullProcessImageNameW = kernel32.NewProc("QueryFullProcessImageNameW")
)

// allowAnyForeground passes our right to change the foreground window on to
// child processes (the emulator), while we still have it from our launcher.
func allowAnyForeground() {
	r, _, err := procAllowSetForegroundWindow.Call(^uintptr(0)) // ASFW_ANY
	logf("focus: AllowSetForegroundWindow(ASFW_ANY)=%d %v", r, err)
}

type mouseInput struct {
	typ   uint32
	_     uint32
	dx    int32
	dy    int32
	data  uint32
	flags uint32
	time  uint32
	_     uint32
	extra uintptr
}

// clickToFocus activates hwnd with a synthesized left click in its
// bottom-right corner (mouse activation isn't subject to the foreground lock).
// The guest sees a tap in the screen corner, where Android TV apps show nothing.
func clickToFocus(hwnd uintptr) {
	var r [4]int32
	procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r[0])))
	var saved [2]int32
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&saved[0])))
	procSetCursorPos.Call(uintptr(r[2]-3), uintptr(r[3]-3))
	down := mouseInput{typ: 0, flags: 0x0002}
	up := mouseInput{typ: 0, flags: 0x0004}
	procSendInput.Call(1, uintptr(unsafe.Pointer(&down)), unsafe.Sizeof(down))
	procSendInput.Call(1, uintptr(unsafe.Pointer(&up)), unsafe.Sizeof(up))
	procSetCursorPos.Call(uintptr(saved[0]), uintptr(saved[1]))
}

// processName returns the executable name for a pid (for diagnostics).
func processName(pid uint32) string {
	h, _, _ := procOpenProcess.Call(0x1000 /*PROCESS_QUERY_LIMITED_INFORMATION*/, 0, uintptr(pid))
	if h == 0 {
		return "?"
	}
	defer procCloseHandle.Call(h)
	buf := make([]uint16, 520)
	n := uint32(len(buf))
	if r, _, _ := procQueryFullProcessImageNameW.Call(h, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n))); r == 0 {
		return "?"
	}
	full := syscall.UTF16ToString(buf[:n])
	if i := strings.LastIndexAny(full, `\/`); i >= 0 {
		return full[i+1:]
	}
	return full
}

// isForeground reports whether hwnd is the active window.
func isForeground(hwnd uintptr) bool {
	fg, _, _ := procGetForegroundWindow.Call()
	return fg == hwnd
}

// setTopmost keeps the full-screen SmartTube window above full-screen
// front-ends such as Playnite.
func setTopmost(hwnd uintptr, on bool) {
	after := ^uintptr(0) // HWND_TOPMOST (-1)
	if !on {
		after = ^uintptr(1) // HWND_NOTOPMOST (-2)
	}
	procSetWindowPos.Call(hwnd, after, 0, 0, 0, 0, 0x0001|0x0002|0x0010) // NOSIZE|NOMOVE|NOACTIVATE
}

func foregroundInfo() (uint32, string) {
	h, _, _ := procGetForegroundWindow.Call()
	if h == 0 {
		return 0, ""
	}
	var pid uint32
	procGetWindowThreadProcessId.Call(h, uintptr(unsafe.Pointer(&pid)))
	buf := make([]uint16, 256)
	procGetWindowTextW.Call(h, uintptr(unsafe.Pointer(&buf[0])), 256)
	return pid, syscall.UTF16ToString(buf)
}

// emulatorHasFocus reports whether the focused window belongs to the emulator
// (its qemu process, emulator.exe, or any window titled for our device).
func emulatorHasFocus(port int, windowPID, emulatorPID uint32) bool {
	pid, title := foregroundInfo()
	if pid == 0 {
		return false
	}
	if pid == windowPID || pid == emulatorPID || pid == uint32(os.Getpid()) {
		return true
	}
	if strings.Contains(title, avdName+":"+itoa(port)) {
		return true
	}
	for _, w := range listWindows() {
		if w.pid == pid && strings.Contains(w.title, avdName+":"+itoa(port)) {
			return true
		}
	}
	return false
}

func foregroundPID() uint32 {
	h, _, _ := procGetForegroundWindow.Call()
	if h == 0 {
		return 0
	}
	var pid uint32
	procGetWindowThreadProcessId.Call(h, uintptr(unsafe.Pointer(&pid)))
	return pid
}

func closeWindow(hwnd uintptr) { procPostMessageW.Call(hwnd, wmClose, 0, 0) }

func intptrNeg(v int) int { return v }

func noConsoleProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: createNoWindow}
}

func hiddenProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}

var procSetPriorityClass = kernel32.NewProc("SetPriorityClass")

// setProcessPriority raises the emulator's Windows priority (config "emulatorPriority").
func setProcessPriority(pid uint32, level string) {
	class := uintptr(0)
	switch level {
	case "above":
		class = 0x00008000 // ABOVE_NORMAL_PRIORITY_CLASS
	case "high":
		class = 0x00000080 // HIGH_PRIORITY_CLASS
	default:
		return
	}
	h, _, _ := procOpenProcess.Call(0x0200 /*PROCESS_SET_INFORMATION*/, 0, uintptr(pid))
	if h == 0 {
		logf("priority: cannot open emulator process %d", pid)
		return
	}
	defer procCloseHandle.Call(h)
	r, _, err := procSetPriorityClass.Call(h, class)
	logf("priority: emulator (%s) set to %s: ok=%v %v", processName(pid), level, r != 0, err)
}
