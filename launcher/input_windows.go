//go:build windows

package main

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unsafe"
)

var (
	procSendInput      = user32.NewProc("SendInput")
	procMapVirtualKeyW = user32.NewProc("MapVirtualKeyW")
)

// Windows virtual-key chords the emulator window understands for each TV key.
// Arrow keys/Enter go through the emulator's normal keyboard path;
// Back and Menu use the emulator's own hardware-button shortcuts.
type vkChord struct {
	ctrl bool
	vk   uint16
	ext  bool
}

var keyboardMap = map[tvKey]vkChord{
	kUp:     {false, 0x26, true},
	kDown:   {false, 0x28, true},
	kLeft:   {false, 0x25, true},
	kRight:  {false, 0x27, true},
	kSelect: {false, 0x0D, false},
	kBack:   {true, 0x08, false}, // Ctrl+Backspace = BACK
	kMenu:   {true, 0x4D, false}, // Ctrl+M = MENU
}

type keybdInput struct {
	typ   uint32
	_     uint32
	vk    uint16
	scan  uint16
	flags uint32
	time  uint32
	_     uint32
	extra uintptr
	_     [8]byte
}

const (
	inputKeyboard   = 1
	keyeventfExtKey = 0x0001
	keyeventfKeyUp  = 0x0002
	vkControl       = 0x11
)

func sendVK(vk uint16, ext, up bool) {
	scan, _, _ := procMapVirtualKeyW.Call(uintptr(vk), 0)
	in := keybdInput{typ: inputKeyboard, vk: vk, scan: uint16(scan)}
	if ext {
		in.flags |= keyeventfExtKey
	}
	if up {
		in.flags |= keyeventfKeyUp
	}
	procSendInput.Call(1, uintptr(unsafe.Pointer(&in)), unsafe.Sizeof(in))
}

func sendChord(c vkChord, down bool) {
	if down {
		if c.ctrl {
			sendVK(vkControl, false, false)
		}
		sendVK(c.vk, c.ext, false)
		if c.ctrl { // shortcuts act on press; release immediately
			sendVK(c.vk, c.ext, true)
			sendVK(vkControl, false, true)
		}
		return
	}
	if !c.ctrl {
		sendVK(c.vk, c.ext, true)
	}
}

// consoleReachesAndroid checks that console key events actually arrive in
// Android's input stack (the console can answer OK while the guest has no
// matching input device). It sends a lone KEY_DOWN release, which is harmless.
func consoleReachesAndroid(con *emuConsole, serial string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, adbExe(), "-s", serial, "shell", "getevent", "-lq")
	cmd.SysProcAttr = hiddenProcAttr()
	out, err := cmd.StdoutPipe()
	if err != nil || cmd.Start() != nil {
		return false
	}
	defer cmd.Process.Kill()
	seen := make(chan bool, 1)
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			if strings.Contains(sc.Text(), "KEY_DOWN") {
				seen <- true
				return
			}
		}
	}()
	time.Sleep(2 * time.Second) // let getevent open the devices
	for i := 0; i < 2; i++ {
		_ = con.key(kDown.linux, false)
		select {
		case <-seen:
			return true
		case <-time.After(1500 * time.Millisecond):
		}
	}
	return false
}

// keySender returns a function that injects TV keys.
//   - "console": emulator telnet console (used only if verified to reach Android)
//   - "keyboard": synthesized keystrokes into the focused emulator window for
//     navigation/select/back/menu, plus `adb shell input keyevent` for media keys.
func keySender(port int, serial string, method string, focused func() bool) func(tvKey, bool) {
	home := os.Getenv("USERPROFILE")
	tokenDirs := []string{home, emuHome, filepath.Join(home, ".android"), filepath.Join(emuHome, ".android")}

	var con *emuConsole
	mode := "keyboard"
	// "auto" goes straight to keyboard: the console path has never reached
	// Android on current emulator images, and testing it delayed the
	// controller by ~5 s after SmartTube opened. "console" still forces it.
	if method == "console" {
		c, err := dialConsole(port, tokenDirs)
		if err != nil {
			logf("input: console unavailable: %v", err)
		} else if method == "console" || consoleReachesAndroid(c, serial) {
			con, mode = c, "console"
		} else {
			logf("input: console accepted events but Android did not receive them")
			c.Close()
		}
	}
	logf("input: method=%s (configured %q)", mode, method)

	// Media/search keys via adb, in order, off the polling thread.
	adbQ := make(chan int, 32)
	go func() {
		for code := range adbQ {
			out, err := runHidden(5*time.Second, adbExe(), "-s", serial, "shell", "input", "keyevent", itoa(code))
			if err != nil {
				logf("input: keyevent %d failed: %v %s", code, err, strings.TrimSpace(out))
			}
		}
	}()
	logged := 0
	return func(k tvKey, down bool) {
		if logged < 6 {
			logged++
			logf("input: %s key %d down=%v", mode, k.linux, down)
		}
		if mode == "console" && con != nil {
			if err := con.key(k.linux, down); err == nil {
				return
			}
			logf("input: console failed; switching to keyboard")
			con.Close()
			con, mode = nil, "keyboard"
		}
		if c, ok := keyboardMap[k]; ok {
			// Keystrokes go to whatever window is focused, so only send them to the emulator.
			if !down || focused() {
				sendChord(c, down)
			}
			return
		}
		if down {
			select {
			case adbQ <- k.android:
			default:
			}
		}
	}
}
