//go:build windows

package main

import (
	"runtime"
	"syscall"
	"unsafe"
)

// In the Xbox full-screen experience, Windows can deliver the controller to
// non-game apps as keyboard input using the VK_GAMEPAD_* virtual keys instead
// of through XInput/GameInput. The Android emulator ignores those keys, so a
// low-level keyboard hook catches them and turns them into TV-remote keys.

var (
	procSetWindowsHookExW = user32.NewProc("SetWindowsHookExW")
	procCallNextHookEx    = user32.NewProc("CallNextHookEx")
	procGetMessageW       = user32.NewProc("GetMessageW")
	procGetModuleHandleW  = kernel32.NewProc("GetModuleHandleW")
)

const (
	whKeyboardLL = 13
	wmKeyDown    = 0x0100
	wmKeyUp      = 0x0101
	wmSysKeyDown = 0x0104
	wmSysKeyUp   = 0x0105
)

type kbdLLHook struct {
	vk, scan, flags, time uint32
	extra                 uintptr
}

// gamepadVK maps Windows VK_GAMEPAD_* codes to TV keys.
var gamepadVK = map[uint32]tvKey{
	0xC3: kSelect,                                     // A
	0xC4: kBack,                                       // B
	0xC5: kPlayPause,                                  // X
	0xC6: kSearch,                                     // Y
	0xC7: kFastFwd,                                    // right shoulder
	0xC8: kRewind,                                     // left shoulder
	0xC9: kPrev,                                       // left trigger
	0xCA: kNext,                                       // right trigger
	0xCB: kUp, 0xCC: kDown, 0xCD: kLeft, 0xCE: kRight, // d-pad
	0xCF: kMenu,                                       // menu
	0xD3: kUp, 0xD4: kDown, 0xD5: kRight, 0xD6: kLeft, // left stick
}

// installKeyHook runs a low-level keyboard hook on its own thread.
// handle returns true to swallow the key.
func installKeyHook(handle func(vk uint32, down bool) bool) {
	go func() {
		runtime.LockOSThread()
		cb := syscall.NewCallback(func(code int, wparam uintptr, lparam uintptr) uintptr {
			if code >= 0 {
				k := (*kbdLLHook)(unsafe.Pointer(lparam))
				down := wparam == wmKeyDown || wparam == wmSysKeyDown
				up := wparam == wmKeyUp || wparam == wmSysKeyUp
				if (down || up) && handle(k.vk, down) {
					return 1
				}
			}
			r, _, _ := procCallNextHookEx.Call(0, uintptr(code), wparam, lparam)
			return r
		})
		mod, _, _ := procGetModuleHandleW.Call(0)
		h, _, err := procSetWindowsHookExW.Call(whKeyboardLL, cb, mod, 0)
		if h == 0 {
			logf("keyhook: SetWindowsHookEx failed: %v", err)
			return
		}
		debugf("keyhook: installed")
		var msg [48]byte
		for {
			r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg[0])), 0, 0, 0)
			if int32(r) <= 0 {
				return
			}
		}
	}()
}
