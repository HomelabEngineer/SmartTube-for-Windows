//go:build windows

package main

import (
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// The launcher opens its own full-screen splash window immediately. Windows
// gives the first window of a process started by the foreground app (Playnite,
// the Xbox shell, Explorer) the foreground, the same way any game gets focus.
// Owning the foreground then lets us hand it to the emulator's window, which
// belongs to a different process and would otherwise be blocked by the
// foreground lock (strictly enforced in the Xbox full-screen experience).

var (
	procRegisterClassExW = user32.NewProc("RegisterClassExW")
	procCreateWindowExW  = user32.NewProc("CreateWindowExW")
	procDefWindowProcW   = user32.NewProc("DefWindowProcW")
	procDestroyWindow    = user32.NewProc("DestroyWindow")
	procPostQuitMessage  = user32.NewProc("PostQuitMessage")
	procTranslateMessage = user32.NewProc("TranslateMessage")
	procDispatchMessageW = user32.NewProc("DispatchMessageW")
	procBeginPaint       = user32.NewProc("BeginPaint")
	procEndPaint         = user32.NewProc("EndPaint")
	procDrawTextW        = user32.NewProc("DrawTextW")
	procGetClientRect    = user32.NewProc("GetClientRect")
	procInvalidateRect   = user32.NewProc("InvalidateRect")
	procGetSystemMetrics = user32.NewProc("GetSystemMetrics")
	procLoadCursorW      = user32.NewProc("LoadCursorW")
	gdi32                = syscall.NewLazyDLL("gdi32.dll")
	procGetStockObject   = gdi32.NewProc("GetStockObject")
	procSetTextColor     = gdi32.NewProc("SetTextColor")
	procSetBkMode        = gdi32.NewProc("SetBkMode")
	procCreateFontW      = gdi32.NewProc("CreateFontW")
	procSelectObject     = gdi32.NewProc("SelectObject")
)

type splashWin struct {
	mu       sync.Mutex
	hwnd     uintptr
	text     string
	font     uintptr
	done     chan struct{}
	child    uintptr // embedded emulator window
	embedRes chan bool
}

const (
	wmAppEmbed   = 0x8001 // WM_APP+1
	wmAppDestroy = 0x8002
)

var (
	procSetParent  = user32.NewProc("SetParent")
	procSetFocus   = user32.NewProc("SetFocus")
	procMoveWindow = user32.NewProc("MoveWindow")
)

var (
	splash    *splashWin
	splashSeq int
)

type wndClassEx struct {
	size       uint32
	style      uint32
	wndProc    uintptr
	clsExtra   int32
	wndExtra   int32
	instance   uintptr
	icon       uintptr
	cursor     uintptr
	background uintptr
	menuName   *uint16
	className  *uint16
	iconSm     uintptr
}

type paintStruct struct {
	hdc       uintptr
	erase     int32
	rc        [4]int32
	restore   int32
	incUpdate int32
	reserved  [32]byte
}

// showSplash opens the splash window and returns once it exists.
func showSplash(text string) {
	s := &splashWin{text: text, done: make(chan struct{}), embedRes: make(chan bool, 1)}
	ready := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer close(s.done)
		// A fresh window class per splash: a class's window procedure is fixed
		// when it's registered, so reusing one would draw the old splash's text.
		splashSeq++
		className := u16("SmartTubeSplash" + itoa(splashSeq))
		mod, _, _ := procGetModuleHandleW.Call(0)
		cursor, _, _ := procLoadCursorW.Call(0, 32512) // IDC_ARROW
		brush, _, _ := procGetStockObject.Call(4)      // BLACK_BRUSH
		wndProc := syscall.NewCallback(func(hwnd, msg, wparam, lparam uintptr) uintptr {
			switch msg {
			case 0x000F: // WM_PAINT
				var ps paintStruct
				hdc, _, _ := procBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
				var rc [4]int32
				procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&rc[0])))
				procSetBkMode.Call(hdc, 1) // TRANSPARENT
				procSetTextColor.Call(hdc, 0x00E0E0E0)
				if s.font != 0 {
					procSelectObject.Call(hdc, s.font)
				}
				s.mu.Lock()
				t := s.text
				s.mu.Unlock()
				rc[1] = rc[3] / 3
				procDrawTextW.Call(hdc, uintptr(unsafe.Pointer(u16(t))), ^uintptr(0), uintptr(unsafe.Pointer(&rc[0])),
					0x1|0x10) // DT_CENTER|DT_WORDBREAK
				procEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
				return 0
			case 0x0010: // WM_CLOSE: with SmartTube embedded, ask the emulator to shut down
				if s.child != 0 {
					procPostMessageW.Call(s.child, wmClose, 0, 0)
					return 0
				}
				procDestroyWindow.Call(hwnd)
				return 0
			case wmAppDestroy:
				procDestroyWindow.Call(hwnd)
				return 0
			case wmAppEmbed:
				s.embedRes <- s.embed(hwnd, lparam)
				return 0
			case 0x0005: // WM_SIZE
				if s.child != 0 {
					var rc [4]int32
					procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&rc[0])))
					procMoveWindow.Call(s.child, 0, 0, uintptr(rc[2]), uintptr(rc[3]), 1)
				}
			case 0x0007, 0x0006: // WM_SETFOCUS, WM_ACTIVATE: keyboard focus belongs to SmartTube
				if s.child != 0 && (msg == 0x0007 || wparam&0xFFFF != 0) {
					procSetFocus.Call(s.child)
				}
			case 0x0100, 0x0101, 0x0104, 0x0105: // key messages that reach us: pass them on
				if s.child != 0 {
					procPostMessageW.Call(s.child, msg, wparam, lparam)
					return 0
				}
			case 0x0002: // WM_DESTROY
				procPostQuitMessage.Call(0)
				return 0
			}
			r, _, _ := procDefWindowProcW.Call(hwnd, msg, wparam, lparam)
			return r
		})
		wc := wndClassEx{wndProc: wndProc, instance: mod, cursor: cursor, background: brush, className: className}
		wc.size = uint32(unsafe.Sizeof(wc))
		procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
		s.font, _, _ = procCreateFontW.Call(^uintptr(47), 0, 0, 0, 400, 0, 0, 0, 0, 0, 0, 5, 0, uintptr(unsafe.Pointer(u16("Segoe UI"))))
		w, _, _ := procGetSystemMetrics.Call(0) // SM_CXSCREEN
		h, _, _ := procGetSystemMetrics.Call(1) // SM_CYSCREEN
		// Topmost, so the emulator's window can be resized to full screen
		// behind it without the in-between window sizes showing.
		hwnd, _, err := procCreateWindowExW.Call(0x00000008 /*WS_EX_TOPMOST*/, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(u16("SmartTube"))),
			0x80000000|0x10000000, // WS_POPUP|WS_VISIBLE
			0, 0, w, h, 0, 0, mod, 0)
		if hwnd == 0 {
			logf("splash: CreateWindowEx failed: %v", err)
			close(ready)
			return
		}
		s.hwnd = hwnd
		procSetForegroundWindow.Call(hwnd)
		logf("splash: shown (foreground=%v)", isForeground(hwnd))
		close(ready)
		var msg [48]byte
		for {
			r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg[0])), 0, 0, 0)
			if int32(r) <= 0 {
				return
			}
			procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg[0])))
			procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg[0])))
		}
	}()
	<-ready
	splash = s
}

func splashText(t string) {
	s := splash
	if s == nil || s.hwnd == 0 {
		return
	}
	s.mu.Lock()
	s.text = t
	s.mu.Unlock()
	procInvalidateRect.Call(s.hwnd, 0, 1)
}

// splashOwnsForeground reports whether our splash is the active window.
func splashOwnsForeground() bool {
	s := splash
	return s != nil && s.hwnd != 0 && isForeground(s.hwnd)
}

func closeSplash() {
	s := splash
	if s == nil || s.hwnd == 0 {
		return
	}
	splash = nil
	procPostMessageW.Call(s.hwnd, wmAppDestroy, 0, 0)
}

// embed (runs on the splash thread) makes the emulator window a child of
// the splash window. The launcher's own window then stays the foreground
// window, so Windows treats the launcher - which reads the controller - as
// the app in front (needed in the Xbox full-screen experience).
func (s *splashWin) embed(host, child uintptr) bool {
	const (
		wsChild   = 0x40000000
		wsVisible = 0x10000000
		wsPopup   = 0x80000000
	)
	style, _, _ := procGetWindowLongPtrW.Call(child, uintptr(intptrNeg(gwlStyle)))
	style &^= wsPopup | wsCaption | wsThickFrame | wsSysMenu | wsMinimizeBox | wsMaximizeBox
	style |= wsChild | wsVisible
	procSetWindowLongPtrW.Call(child, uintptr(intptrNeg(gwlStyle)), style)
	if r, _, err := procSetParent.Call(child, host); r == 0 {
		logf("embed: SetParent failed: %v", err)
		return false
	}
	s.child = child
	var rc [4]int32
	procGetClientRect.Call(host, uintptr(unsafe.Pointer(&rc[0])))
	procSetWindowPos.Call(child, 0, 0, 0, uintptr(rc[2]), uintptr(rc[3]), swpFrameChgd|swpShowWindow)
	setTopmost(host, true)
	focusWindow(host)
	procSetFocus.Call(child)
	return true
}

// embedInSplash embeds hwnd into the splash window (if there is one).
func embedInSplash(hwnd uintptr) bool {
	s := splash
	if s == nil || s.hwnd == 0 {
		return false
	}
	procPostMessageW.Call(s.hwnd, wmAppEmbed, 0, hwnd)
	select {
	case ok := <-s.embedRes:
		return ok
	case <-time.After(5 * time.Second):
		return false
	}
}

// splashHost returns the splash window handle (0 if none).
func splashHost() uintptr {
	if s := splash; s != nil {
		return s.hwnd
	}
	return 0
}

// resizeEmbedded re-applies the child size (the emulator may re-layout itself).
func resizeEmbedded() {
	s := splash
	if s == nil || s.child == 0 {
		return
	}
	var rc [4]int32
	procGetClientRect.Call(s.hwnd, uintptr(unsafe.Pointer(&rc[0])))
	procMoveWindow.Call(s.child, 0, 0, uintptr(rc[2]), uintptr(rc[3]), 1)
}

// splashDone is closed when the splash window is gone.
func splashDone() <-chan struct{} {
	if s := splash; s != nil {
		return s.done
	}
	c := make(chan struct{})
	close(c)
	return c
}
