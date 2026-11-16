//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	avcOptionText  = "Codec selection: prefer avc over vp9"
	avcOptionPath  = "Settings -> Player -> Developer options -> Codec selection: prefer avc over vp9"
	playerDialogID = "Video presets" // first entry of SmartTube's Player settings dialog
)

// applySmartTubeDefaults switches on SmartTube's
//
//	Settings -> Player -> Developer options -> Codec selection: prefer avc over vp9
//
// by driving SmartTube's own menus with the remote's keys, reading the screen
// with uiautomator to see where the focus is. SmartTube keeps settings in
// private storage that adb can't write on these system images, so this runs
// once, behind the start-up screen. Returns true when the option is confirmed on.
func applySmartTubeDefaults(serial, pkg string, freshInstall bool) bool {
	sh := func(t time.Duration, cmd ...string) string {
		o, _ := runHidden(t, adbExe(), append([]string{"-s", serial, "shell"}, cmd...)...)
		return o
	}
	key := func(code string) {
		sh(5*time.Second, "input", "keyevent", code)
		time.Sleep(350 * time.Millisecond)
	}
	const (
		up, down, left, right, center, back = "19", "20", "21", "22", "23", "4"
	)
	dump := func() []uiNode {
		for i := 0; i < 3; i++ {
			o := sh(15*time.Second, "uiautomator dump /sdcard/st_ui.xml >/dev/null 2>&1; cat /sdcard/st_ui.xml")
			if strings.Contains(o, "<hierarchy") {
				return parseUINodes(o)
			}
			time.Sleep(700 * time.Millisecond)
		}
		return nil
	}
	// moveFocusTo presses `step` until the focus is on the item labelled t.
	moveFocusTo := func(t, step string, max int) bool {
		for i := 0; i <= max; i++ {
			if focusedOn(dump(), t) {
				return true
			}
			key(step)
		}
		logf("defaults: couldn't move focus to %q", t)
		return false
	}
	backHome := func() {
		key(back)
		key(back)
		sh(10*time.Second, "am", "start", "-n", pkg+"/com.liskovsoft.smartyoutubetv2.tv.launchers.HomeLauncherActivity")
		time.Sleep(time.Second)
	}

	logf("defaults: switching on %s", avcOptionPath)
	time.Sleep(8 * time.Second) // let SmartTube load its home screen

	// 1. Sidebar: open it, move down to "Settings", then Right to enter the settings grid.
	key(left)
	key(left)
	time.Sleep(time.Second)
	if !moveFocusTo("Settings", down, 20) {
		return false
	}
	key(right)
	time.Sleep(2 * time.Second)

	// 2. Settings grid: move to the "Player" card and press OK.
	opened := false
	for i := 0; i < 16 && !opened; i++ {
		nodes := dump()
		switch {
		case func() bool { _, ok := findText(nodes, playerDialogID); return ok }():
			opened = true
		case focusedOn(nodes, "Player"):
			key(center)
			time.Sleep(2 * time.Second)
		case i%6 == 5:
			key(down) // next row of cards
		default:
			key(right)
		}
	}
	if !opened {
		logf("defaults: couldn't open the Player settings")
		backHome()
		return false
	}

	// 3. Player dialog: down to "Developer options" (the last entry), OK.
	if !moveFocusTo("Developer options", down, 45) {
		backHome()
		return false
	}
	key(center)
	time.Sleep(2 * time.Second)

	// 4. Developer options: down to the AVC option and switch it on.
	done := false
	if moveFocusTo(avcOptionText, down, 40) {
		raw := sh(15*time.Second, "cat /sdcard/st_ui.xml")
		_ = os.WriteFile(filepath.Join(dataDir, "smarttube-ui.xml"), []byte(raw), 0o644)
		nodes := dump()
		n, _ := findText(nodes, avcOptionText)
		checked, known := checkStateNear(nodes, n)
		switch {
		case known && checked:
			logf("defaults: already on")
			done = true
		case known || freshInstall:
			key(center)
			time.Sleep(1500 * time.Millisecond)
			nodes = dump()
			if n2, ok := findText(nodes, avcOptionText); ok {
				if c, k := checkStateNear(nodes, n2); k {
					done = c
				} else {
					done = true
				}
			}
			logf("defaults: toggled (confirmed on=%v)", done)
		default:
			logf("defaults: can't read the option's state; leaving it unchanged (screen saved to smarttube-ui.xml)")
		}
	}
	backHome()
	return done
}
