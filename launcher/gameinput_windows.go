//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

// Microsoft GameInput (GameInput.dll, installed with the Xbox app / GameInput
// redistributable). On the ROG Xbox Ally in the Xbox full-screen experience,
// XInput reports a neutral pad to desktop apps while GameInput still delivers
// real input, and GameInput's default focus policy allows background input.
//
// Only the original (v0) ABI from GameInputCreate is used:
//   IGameInput vtable:        4 = GetCurrentReading(kind, device, **reading)
//   IGameInputReading vtable: 22 = GetGamepadState(*state) bool, 2 = Release

const (
	giKindGamepad = 0x00040000

	giMenu      = 0x0001
	giView      = 0x0002
	giA         = 0x0004
	giB         = 0x0008
	giX         = 0x0010
	giY         = 0x0020
	giDpadUp    = 0x0040
	giDpadDown  = 0x0080
	giDpadLeft  = 0x0100
	giDpadRight = 0x0200
	giLB        = 0x0400
	giRB        = 0x0800
)

type giGamepadState struct {
	buttons        uint32
	lt, rt         float32
	lx, ly, rx, ry float32
}

type gameInput struct{ obj uintptr }

func loadGameInput() *gameInput {
	dll := syscall.NewLazyDLL("GameInput.dll")
	if err := dll.Load(); err != nil {
		debugf("gameinput: not available (%v)", err)
		return nil
	}
	create := dll.NewProc("GameInputCreate")
	if create.Find() != nil {
		logf("gameinput: GameInputCreate not exported")
		return nil
	}
	var obj uintptr
	hr, _, _ := create.Call(uintptr(unsafe.Pointer(&obj)))
	if int32(hr) < 0 || obj == 0 {
		logf("gameinput: GameInputCreate failed hr=0x%08x", uint32(hr))
		return nil
	}
	debugf("gameinput: ready")
	return &gameInput{obj: obj}
}

func vtblFn(obj uintptr, idx int) uintptr {
	vtbl := *(*uintptr)(unsafe.Pointer(obj))
	return *(*uintptr)(unsafe.Pointer(vtbl + uintptr(idx)*unsafe.Sizeof(uintptr(0))))
}

// read returns the latest gamepad state as an XInput-style state.
func (g *gameInput) read() (xinputState, bool) {
	var reading uintptr
	hr, _, _ := syscall.SyscallN(vtblFn(g.obj, 4), g.obj, giKindGamepad, 0, uintptr(unsafe.Pointer(&reading)))
	if int32(hr) < 0 || reading == 0 {
		return xinputState{}, false
	}
	var st giGamepadState
	ok, _, _ := syscall.SyscallN(vtblFn(reading, 22), reading, uintptr(unsafe.Pointer(&st)))
	syscall.SyscallN(vtblFn(reading, 2), reading) // Release
	if ok&0xff == 0 {
		return xinputState{}, false
	}
	var x xinputState
	m := [][2]uint32{{giMenu, xStart}, {giView, xBack}, {giA, xA}, {giB, xB}, {giX, xX}, {giY, xY},
		{giDpadUp, xDpadUp}, {giDpadDown, xDpadDown}, {giDpadLeft, xDpadLeft}, {giDpadRight, xDpadRight},
		{giLB, xLB}, {giRB, xRB}}
	for _, p := range m {
		if st.buttons&p[0] != 0 {
			x.buttons |= uint16(p[1])
		}
	}
	x.lt = uint8(clampf(st.lt, 0, 1) * 255)
	x.rt = uint8(clampf(st.rt, 0, 1) * 255)
	x.lx = int16(clampf(st.lx, -1, 1) * 32767)
	x.ly = int16(clampf(st.ly, -1, 1) * 32767)
	return x, true
}

func clampf(v, lo, hi float32) float32 {
	if v != v { // NaN
		return 0
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
