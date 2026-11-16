//go:build windows

package main

import (
	"fmt"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// Linux input key codes understood by the emulator's virtual keyboard and
// the Android TV key layout, with the Android keycode used for the adb fallback.
type tvKey struct{ linux, android int }

var (
	kUp        = tvKey{103, 19}
	kDown      = tvKey{108, 20}
	kLeft      = tvKey{105, 21}
	kRight     = tvKey{106, 22}
	kSelect    = tvKey{28, 66}  // ENTER = select/OK
	kBack      = tvKey{158, 4}  // BACK
	kPlayPause = tvKey{164, 85} // MEDIA_PLAY_PAUSE
	kSearch    = tvKey{217, 84} // SEARCH
	kRewind    = tvKey{168, 89} // MEDIA_REWIND (seek back)
	kFastFwd   = tvKey{208, 90} // MEDIA_FAST_FORWARD (seek forward)
	kMenu      = tvKey{139, 82} // MENU
	kNext      = tvKey{163, 87} // MEDIA_NEXT
	kPrev      = tvKey{165, 88} // MEDIA_PREVIOUS
)

const (
	xDpadUp    = 0x0001
	xDpadDown  = 0x0002
	xDpadLeft  = 0x0004
	xDpadRight = 0x0008
	xStart     = 0x0010
	xBack      = 0x0020
	xLB        = 0x0100
	xRB        = 0x0200
	xA         = 0x1000
	xB         = 0x2000
	xX         = 0x4000
	xY         = 0x8000
)

type xinputState struct {
	packet  uint32
	buttons uint16
	lt, rt  uint8
	lx, ly  int16
	rx, ry  int16
}

func loadXInput() *syscall.LazyProc {
	for _, dll := range []string{"xinput1_4.dll", "xinput1_3.dll", "xinput9_1_0.dll"} {
		d := syscall.NewLazyDLL(dll)
		if d.Load() == nil {
			p := d.NewProc("XInputGetState")
			if p.Find() == nil {
				logf("gamepad: using %s", dll)
				return p
			}
		}
	}
	return nil
}

// runGamepad polls all XInput controllers and turns them into a TV remote.
// Input is only forwarded while the emulator window has focus.
// send(key, down); quit() is called on View+Menu (Back+Start).
func runGamepad(allowed func() bool, send func(k tvKey, down bool), quit func(), refocus func() bool, stop <-chan struct{}) {
	get := loadXInput()
	gi := loadGameInput()
	ri := startRawInput()
	if get == nil && gi == nil && ri == nil {
		logf("gamepad: no controller API available")
		return
	}
	giLogged := 0
	held := map[tvKey]bool{}
	const dead = 16000
	tick := time.NewTicker(16 * time.Millisecond)
	defer tick.Stop()
	quitArmed := true
	var connected [4]bool
	var lastPacket [4]uint32
	rawLogged := 0
	var lastFocusLog time.Time
	pressesLogged := 0
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
		}
		want := map[tvKey]bool{}
		var anyBtns uint16
		var states []xinputState
		for i := uintptr(0); get != nil && i < 4; i++ {
			var s xinputState
			r, _, _ := get.Call(i, uintptr(unsafe.Pointer(&s)))
			if (r == 0) != connected[i] {
				connected[i] = r == 0
				logf("gamepad: controller %d connected=%v", i, connected[i])
			}
			if r != 0 {
				continue
			}
			if s.packet != lastPacket[i] {
				lastPacket[i] = s.packet
				if rawLogged < 15 {
					rawLogged++
					logf("gamepad: raw slot %d packet=%d buttons=%04x lt=%d rt=%d lx=%d ly=%d", i, s.packet, s.buttons, s.lt, s.rt, s.lx, s.ly)
				}
			}
			states = append(states, s)
		}
		if gi != nil {
			if s, ok := gi.read(); ok {
				if s.buttons != 0 && giLogged < 5 {
					giLogged++
					logf("gameinput: buttons=%04x lt=%d rt=%d lx=%d ly=%d", s.buttons, s.lt, s.rt, s.lx, s.ly)
				}
				states = append(states, s)
			}
		}
		if ri != nil {
			states = append(states, ri.states()...)
		}
		for _, s := range states {
			b := s.buttons
			anyBtns |= b
			set := func(cond bool, k tvKey) {
				if cond {
					want[k] = true
				}
			}
			set(b&xDpadUp != 0 || s.ly > dead, kUp)
			set(b&xDpadDown != 0 || s.ly < -dead, kDown)
			set(b&xDpadLeft != 0 || s.lx < -dead, kLeft)
			set(b&xDpadRight != 0 || s.lx > dead, kRight)
			set(b&xA != 0, kSelect)
			set(b&xB != 0, kBack)
			set(b&xX != 0, kPlayPause)
			set(b&xY != 0, kSearch)
			set(b&xLB != 0, kRewind)
			set(b&xRB != 0, kFastFwd)
			set(b&xStart != 0 && b&xBack == 0, kMenu)
			set(s.lt > 200, kPrev)
			set(s.rt > 200, kNext)
		}
		if anyBtns&(xBack|xStart) == xBack|xStart {
			if quitArmed {
				quitArmed = false
				quit()
			}
			continue
		}
		quitArmed = true
		if len(want) > 0 && !allowed() {
			// A controller press while another window (e.g. Playnite) is in
			// front brings SmartTube back to the front; that press is consumed.
			pid, title := foregroundInfo()
			ok := refocus != nil && refocus()
			if time.Since(lastFocusLog) > 5*time.Second {
				lastFocusLog = time.Now()
				logf("gamepad: SmartTube not focused (focused: pid %d %q); refocus ok=%v", pid, title, ok)
			}
			for k := range want {
				held[k] = true // swallow this press; its release is sent harmlessly later
			}
			want = map[tvKey]bool{}
		}
		if len(want) > 0 && pressesLogged < 3 {
			pressesLogged++
			logf("gamepad: press %v", want)
		}
		for k := range held {
			if !want[k] {
				send(k, false)
				delete(held, k)
			}
		}
		for k := range want {
			if !held[k] {
				send(k, true)
				held[k] = true
			}
		}
	}
}

// gamepadTest prints live XInput state in a console window (SmartTube.exe --gamepad-test).
// The console is this process's own window, so it shows whether the controller
// reports buttons to a focused app.
func gamepadTest() {
	// Runs in the launcher's own full-screen window (not a console), so in the
	// Xbox full-screen experience the app in front is the one reading the pad.
	showSplash("Controller test")
	get := loadXInput()
	gi := loadGameInput()
	ri := startRawInput()
	var lastRI xinputState
	var lines []string
	add := func(l string) {
		l = time.Now().Format("15:04:05") + "  " + l
		logf("gamepad-test: %s", l)
		lines = append(lines, l)
		if len(lines) > 10 {
			lines = lines[len(lines)-10:]
		}
		_, title := foregroundInfo()
		splashText(fmt.Sprintf("SmartTube controller test   (XInput: %v   GameInput: %v   RawInput: %v)\nPress buttons. Hold View + Menu, or press Alt+F4, to close.\nIn front: %q\n\n%s",
			get != nil, gi != nil, ri != nil, title, strings.Join(lines, "\n")))
	}
	add("started")
	installKeyHook(func(vk uint32, down bool) bool {
		if vk >= 0xC3 && vk <= 0xDA {
			add(fmt.Sprintf("GAMEPAD key vk=0x%02X down=%v", vk, down))
		}
		return false
	})
	var last [4]xinputState
	var conn [4]bool
	var lastGI xinputState
	done := splashDone()
	for {
		select {
		case <-done:
			return
		case <-time.After(16 * time.Millisecond):
		}
		var all uint16
		for i := uintptr(0); get != nil && i < 4; i++ {
			var s xinputState
			r, _, _ := get.Call(i, uintptr(unsafe.Pointer(&s)))
			ok := r == 0
			if ok != conn[i] {
				conn[i] = ok
				add(fmt.Sprintf("XInput slot %d connected=%v", i, ok))
			}
			s.packet = 0
			if ok && s != last[i] {
				last[i] = s
				add(fmt.Sprintf("XInput%d    buttons=%04x LT=%d RT=%d LX=%d LY=%d", i, s.buttons, s.lt, s.rt, s.lx, s.ly))
			}
			all |= s.buttons
		}
		if gi != nil {
			if s, ok := gi.read(); ok && s != lastGI {
				lastGI = s
				add(fmt.Sprintf("GameInput buttons=%04x LT=%d RT=%d LX=%d LY=%d", s.buttons, s.lt, s.rt, s.lx, s.ly))
				all |= s.buttons
			}
		}
		if ri != nil {
			var agg xinputState
			for _, st := range ri.states() {
				agg.buttons |= st.buttons
				if st.lx != 0 || st.ly != 0 {
					agg.lx, agg.ly = st.lx, st.ly
				}
			}
			if agg != lastRI {
				lastRI = agg
				add(fmt.Sprintf("RawInput  buttons=%04x LX=%d LY=%d", agg.buttons, agg.lx, agg.ly))
			}
			all |= agg.buttons
		}
		if all&(xBack|xStart) == xBack|xStart {
			closeSplash()
			return
		}
	}
}
