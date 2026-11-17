//go:build windows

package main

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// automating is true while SmartTube's menus are being driven.
var automating atomic.Bool

// applySmartTubeSettings applies each configured tickbox through SmartTube's
// own menus (SmartTube keeps settings in private storage that adb can't write
// on these system images). Returns true when every setting is done.
func applySmartTubeSettings(serial, pkg string, list []SmartTubeSetting, freshInstall bool) bool {
	automating.Store(true)
	defer automating.Store(false)
	all := true
	for _, s := range list {
		ok := applySmartTubeSetting(serial, pkg, s, freshInstall)
		logf("settings: %s = %v: %s", strings.Join(s.Path, " -> "), s.Checked, map[bool]string{true: "done", false: "FAILED"}[ok])
		all = all && ok
	}
	return all
}

// screenshot returns Android's screen (nil on failure); saved in debug mode.
func screenshot(serial string) image.Image {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, adbExe(), "-s", serial, "exec-out", "screencap", "-p")
	cmd.SysProcAttr = hiddenProcAttr()
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	if debugMode {
		_ = os.WriteFile(filepath.Join(dataDir, "smarttube-screen.png"), out, 0o644)
	}
	img, _ := png.Decode(bytes.NewReader(out))
	return img
}

// applySmartTubeSetting is the 1.4.2 menu automation, driven by s.Path:
//
//	Path[0]        sidebar section: Left to open the sidebar, Down until it's
//	               highlighted, then Right into its content
//	Path[1]        card in that section: Right (and Down every 6th step) until
//	               it's highlighted, then OK; the menu is open once s.OpensTo
//	               (its first entry) is on screen
//	Path[2..n-2]   menu entries: Down until highlighted, then OK
//	Path[n-1]      the tickbox: Down until highlighted, then OK if it isn't
//	               already s.Checked
//
// The highlight is read with uiautomator after every single key press.
func applySmartTubeSetting(serial, pkg string, s SmartTubeSetting, freshInstall bool) bool {
	if len(s.Path) < 3 {
		logf("settings: path %v is too short (need section, card, ..., option)", s.Path)
		return false
	}
	sh := func(t time.Duration, cmd ...string) string {
		o, _ := runHidden(t, adbExe(), append([]string{"-s", serial, "shell"}, cmd...)...)
		return o
	}
	key := func(code string) {
		sh(5*time.Second, "input", "keyevent", code)
		time.Sleep(350 * time.Millisecond)
	}
	const down, left, right, center, back = "20", "21", "22", "23", "4"
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
	moveFocusTo := func(t, step string, max int) bool {
		for i := 0; i <= max; i++ {
			if focusedOn(dump(), t) {
				return true
			}
			key(step)
		}
		logf("settings: couldn't move the focus to %q", t)
		return false
	}
	// capture saves what the launcher sees (debug mode only).
	capture := func(why string) {
		if !debugMode {
			return
		}
		raw := sh(15*time.Second, "uiautomator dump /sdcard/st_ui.xml >/dev/null 2>&1; cat /sdcard/st_ui.xml")
		_ = os.WriteFile(filepath.Join(dataDir, "smarttube-ui.xml"), []byte(raw), 0o644)
		screenshot(serial)
		logf("settings: screen saved to smarttube-ui.xml / smarttube-screen.png (%s)", why)
	}
	backHome := func() {
		key(back)
		key(back)
		sh(10*time.Second, "am", "start", "-n", pkg+"/com.liskovsoft.smartyoutubetv2.tv.launchers.HomeLauncherActivity")
		time.Sleep(time.Second)
	}
	section, card, option := s.Path[0], s.Path[1], s.Path[len(s.Path)-1]

	time.Sleep(8 * time.Second) // let SmartTube load its home screen

	// 1. Sidebar section, then Right into its content.
	key(left)
	key(left)
	time.Sleep(time.Second)
	if !moveFocusTo(section, down, 20) {
		capture("couldn't find " + section)
		return false
	}
	key(right)
	time.Sleep(2 * time.Second)

	// 2. Card: move to it and press OK until its menu (s.OpensTo) is on screen.
	opened := false
	for i := 0; i < 16 && !opened; i++ {
		nodes := dump()
		if _, ok := findText(nodes, s.OpensTo); ok && s.OpensTo != "" {
			opened = true
			break
		}
		switch {
		case focusedOn(nodes, card):
			key(center)
			time.Sleep(2 * time.Second)
		case i%6 == 5:
			key(down) // next row of cards
		default:
			key(right)
		}
	}
	if !opened {
		logf("settings: couldn't open %q (its menu should show %q)", card, s.OpensTo)
		capture("couldn't open " + card)
		backHome()
		return false
	}

	// 3. Menu entries.
	for _, item := range s.Path[2 : len(s.Path)-1] {
		if !moveFocusTo(item, down, 45) {
			capture("couldn't find " + item)
			backHome()
			return false
		}
		key(center)
		time.Sleep(2 * time.Second)
	}

	// 4. The tickbox.
	done := false
	if moveFocusTo(option, down, 40) {
		if debugMode {
			capture("tickbox reached")
		}
		nodes := dump()
		n, _ := findText(nodes, option)
		checked, known := checkStateNear(nodes, n)
		switch {
		case known && checked == s.Checked:
			debugf("settings: %q already %v", option, checked)
			done = true
		case known || (freshInstall && s.Checked):
			key(center)
			time.Sleep(1500 * time.Millisecond)
			nodes = dump()
			if n2, ok := findText(nodes, option); ok {
				if c, k := checkStateNear(nodes, n2); k {
					done = c == s.Checked
				} else {
					done = true
				}
			}
		default:
			logf("settings: can't read the state of %q; left unchanged", option)
		}
	} else {
		capture("couldn't find " + option)
	}
	backHome()
	return done
}
