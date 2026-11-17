//go:build windows

package main

import (
	"runtime"
	"strings"
	"time"
)

// Keep Windows' screen on while a video plays. Windows can't see playback
// inside the emulator, so ask Android every 15 s whether SmartTube is
// playing (media session state, with active audio as a fallback) and hold a
// "display required" request while it is, like any Windows video player.

var procSetThreadExecutionState = kernel32.NewProc("SetThreadExecutionState")

const (
	esContinuous      = 0x80000000
	esSystemRequired  = 0x00000001
	esDisplayRequired = 0x00000002
)

// isPlaying asks Android whether SmartTube is playing.
func isPlaying(serial, pkg string) bool {
	o, _ := runHidden(10*time.Second, adbExe(), "-s", serial, "shell", "dumpsys", "media_session")
	if sessionPlaying(o, pkg) {
		return true
	}
	// Fallback: any audio actively playing (SmartTube is the only app playing).
	a, _ := runHidden(10*time.Second, adbExe(), "-s", serial, "shell", "dumpsys", "audio")
	return strings.Contains(a, "state:started")
}

// keepAwake runs until stop is closed. mode: "playing" (default), "always" or "off".
func keepAwake(serial, pkg, mode string, stop <-chan struct{}) {
	if mode == "off" {
		return
	}
	runtime.LockOSThread() // the request belongs to this thread
	defer runtime.UnlockOSThread()
	set := func(on bool) {
		flags := uintptr(esContinuous)
		if on {
			flags |= esSystemRequired | esDisplayRequired
		}
		procSetThreadExecutionState.Call(flags)
	}
	defer set(false)
	awake := false
	for {
		want := mode == "always" || isPlaying(serial, pkg)
		if want != awake {
			awake = want
			set(awake)
			debugf("keep awake: %v", awake)
		}
		select {
		case <-stop:
			return
		case <-time.After(15 * time.Second):
		}
	}
}
