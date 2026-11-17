//go:build windows

package main

import (
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

// Raw Input (WM_INPUT) reader for HID gamepads. This is the path SDL - and so
// AntiMicroX - uses for Xbox controllers. With RIDEV_INPUTSINK it receives
// input while another app is in front, which XInput/GameInput don't deliver to
// background apps in the Xbox full-screen experience.

var (
	procRegisterRawInputDevices  = user32.NewProc("RegisterRawInputDevices")
	procGetRawInputData          = user32.NewProc("GetRawInputData")
	procGetRawInputDeviceInfoW   = user32.NewProc("GetRawInputDeviceInfoW")
	hidDLL                       = syscall.NewLazyDLL("hid.dll")
	procHidPGetUsages            = hidDLL.NewProc("HidP_GetUsages")
	procHidPGetUsageValue        = hidDLL.NewProc("HidP_GetUsageValue")
	procHidPGetSpecificValueCaps = hidDLL.NewProc("HidP_GetSpecificValueCaps")
)

const (
	hidpStatusSuccess = 0x00110000
	ridInput          = 0x10000003
	ridiPreparsedData = 0x20000005
	rimTypeHID        = 2
	wmInput           = 0x00FF
)

type rawDevice struct {
	preparsed []byte
	ranges    map[uint16][2]int32 // usage -> logical min/max
	state     xinputState
}

type rawInputReader struct {
	mu      sync.Mutex
	devices map[uintptr]*rawDevice
	logged  int
}

var rawReader *rawInputReader

// startRawInput registers for gamepad/joystick HID input on a hidden window.
func startRawInput() *rawInputReader {
	if hidDLL.Load() != nil {
		logf("rawinput: hid.dll not available")
		return nil
	}
	r := &rawInputReader{devices: map[uintptr]*rawDevice{}}
	ready := make(chan bool, 1)
	go func() {
		runtime.LockOSThread()
		className := u16("SmartTubeRawInput")
		mod, _, _ := procGetModuleHandleW.Call(0)
		wndProc := syscall.NewCallback(func(hwnd, msg, wparam, lparam uintptr) uintptr {
			if msg == wmInput {
				r.handle(lparam)
			}
			res, _, _ := procDefWindowProcW.Call(hwnd, msg, wparam, lparam)
			return res
		})
		wc := wndClassEx{wndProc: wndProc, instance: mod, className: className}
		wc.size = uint32(unsafe.Sizeof(wc))
		procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
		hwnd, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(u16("SmartTube input"))),
			0x80000000 /*WS_POPUP, never shown*/, 0, 0, 0, 0, 0, 0, mod, 0)
		if hwnd == 0 {
			logf("rawinput: window failed: %v", err)
			ready <- false
			return
		}
		type rid struct {
			usagePage, usage uint16
			flags            uint32
			target           uintptr
		}
		devs := []rid{
			{0x01, 0x05, 0x00000100, hwnd}, // gamepad, RIDEV_INPUTSINK
			{0x01, 0x04, 0x00000100, hwnd}, // joystick
		}
		ok, _, err := procRegisterRawInputDevices.Call(uintptr(unsafe.Pointer(&devs[0])), uintptr(len(devs)), unsafe.Sizeof(devs[0]))
		if ok == 0 {
			logf("rawinput: RegisterRawInputDevices failed: %v", err)
			ready <- false
			return
		}
		debugf("rawinput: ready")
		ready <- true
		var msg [48]byte
		for {
			rr, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg[0])), 0, 0, 0)
			if int32(rr) <= 0 {
				return
			}
			procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg[0])))
			procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg[0])))
		}
	}()
	if !<-ready {
		return nil
	}
	rawReader = r
	return r
}

const rawHeaderSize = 24 // RAWINPUTHEADER on 64-bit: dwType, dwSize, hDevice, wParam

func (r *rawInputReader) handle(lparam uintptr) {
	var size uint32
	procGetRawInputData.Call(lparam, ridInput, 0, uintptr(unsafe.Pointer(&size)), rawHeaderSize)
	if size == 0 || size > 1<<16 {
		return
	}
	buf := make([]byte, size)
	if n, _, _ := procGetRawInputData.Call(lparam, ridInput, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), rawHeaderSize); int32(n) <= 0 {
		return
	}
	if *(*uint32)(unsafe.Pointer(&buf[0])) != rimTypeHID {
		return
	}
	hDevice := *(*uintptr)(unsafe.Pointer(&buf[8]))
	sizeHid := *(*uint32)(unsafe.Pointer(&buf[rawHeaderSize]))
	count := *(*uint32)(unsafe.Pointer(&buf[rawHeaderSize+4]))
	data := buf[rawHeaderSize+8:]
	if sizeHid == 0 || count == 0 || int(sizeHid*count) > len(data) {
		return
	}
	report := data[int(sizeHid*(count-1)):int(sizeHid*count)] // latest report

	r.mu.Lock()
	defer r.mu.Unlock()
	dev := r.devices[hDevice]
	if dev == nil {
		dev = r.openDevice(hDevice)
		if dev == nil {
			return
		}
		r.devices[hDevice] = dev
	}
	st := r.parse(dev, report)
	if st != dev.state && r.logged < 8 && st.buttons != 0 {
		r.logged++
		debugf("rawinput: buttons=%04x lx=%d ly=%d", st.buttons, st.lx, st.ly)
	}
	dev.state = st
}

func (r *rawInputReader) openDevice(h uintptr) *rawDevice {
	var size uint32
	procGetRawInputDeviceInfoW.Call(h, ridiPreparsedData, 0, uintptr(unsafe.Pointer(&size)))
	if size == 0 {
		return nil
	}
	pp := make([]byte, size)
	if n, _, _ := procGetRawInputDeviceInfoW.Call(h, ridiPreparsedData, uintptr(unsafe.Pointer(&pp[0])), uintptr(unsafe.Pointer(&size))); int32(n) <= 0 {
		return nil
	}
	dev := &rawDevice{preparsed: pp, ranges: map[uint16][2]int32{}}
	for _, u := range []uint16{0x30, 0x31, 0x39} {
		var caps [72]byte // HIDP_VALUE_CAPS
		n := uint16(1)
		st, _, _ := procHidPGetSpecificValueCaps.Call(0, 0x01, 0, uintptr(u), uintptr(unsafe.Pointer(&caps[0])), uintptr(unsafe.Pointer(&n)), uintptr(unsafe.Pointer(&pp[0])))
		if uint32(st) == hidpStatusSuccess && n > 0 {
			lo := *(*int32)(unsafe.Pointer(&caps[40]))
			hi := *(*int32)(unsafe.Pointer(&caps[44]))
			dev.ranges[u] = [2]int32{lo, hi}
		}
	}
	debugf("rawinput: new HID controller (ranges %v)", dev.ranges)
	return dev
}

func (r *rawInputReader) value(dev *rawDevice, usage uint16, report []byte) (uint32, bool) {
	var v uint32
	st, _, _ := procHidPGetUsageValue.Call(0, 0x01, 0, uintptr(usage), uintptr(unsafe.Pointer(&v)),
		uintptr(unsafe.Pointer(&dev.preparsed[0])), uintptr(unsafe.Pointer(&report[0])), uintptr(len(report)))
	return v, uint32(st) == hidpStatusSuccess
}

// parse converts a HID report to an XInput-style state using the standard
// Xbox HID layout: buttons 1..10 = A B X Y LB RB View Menu LS RS,
// X/Y = left stick, hat switch = d-pad.
func (r *rawInputReader) parse(dev *rawDevice, report []byte) xinputState {
	var x xinputState
	usages := make([]uint16, 32)
	n := uint32(len(usages))
	st, _, _ := procHidPGetUsages.Call(0, 0x09, 0, uintptr(unsafe.Pointer(&usages[0])), uintptr(unsafe.Pointer(&n)),
		uintptr(unsafe.Pointer(&dev.preparsed[0])), uintptr(unsafe.Pointer(&report[0])), uintptr(len(report)))
	if uint32(st) == hidpStatusSuccess {
		btn := map[uint16]uint16{1: xA, 2: xB, 3: xX, 4: xY, 5: xLB, 6: xRB, 7: xBack, 8: xStart}
		for _, u := range usages[:n] {
			x.buttons |= btn[u]
		}
	}
	axis := func(usage uint16) int16 {
		v, ok := r.value(dev, usage, report)
		if !ok {
			return 0
		}
		rg, have := dev.ranges[usage]
		if !have || rg[1] <= rg[0] {
			rg = [2]int32{0, 65535}
		}
		mid := float32(rg[0]+rg[1]) / 2
		half := float32(rg[1]-rg[0]) / 2
		return int16(clampf((float32(int32(v))-mid)/half, -1, 1) * 32767)
	}
	x.lx = axis(0x30)
	x.ly = -axis(0x31) // HID Y grows downward
	if v, ok := r.value(dev, 0x39, report); ok {
		lo := int32(1)
		if rg, have := dev.ranges[0x39]; have {
			lo = rg[0]
		}
		d := int32(v) - lo // 0=up,1=up-right,...,7=up-left; out of range = centred
		if d >= 0 && d < 8 {
			dirs := []uint16{xDpadUp, xDpadUp | xDpadRight, xDpadRight, xDpadDown | xDpadRight,
				xDpadDown, xDpadDown | xDpadLeft, xDpadLeft, xDpadUp | xDpadLeft}
			x.buttons |= dirs[d]
		}
	}
	return x
}

// states returns the current state of every HID controller seen.
func (r *rawInputReader) states() []xinputState {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]xinputState, 0, len(r.devices))
	for _, d := range r.devices {
		out = append(out, d.state)
	}
	return out
}
