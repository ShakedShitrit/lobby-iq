//go:build windows

package inputrec

import (
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// xinputGetState is XInputGetState from whichever XInput the system has:
// 1.4 ships with Windows 8 and later, 9.1.0 with everything since Vista.
var xinputGetState = func() *windows.LazyProc {
	for _, name := range []string{"xinput1_4.dll", "xinput1_3.dll", "xinput9_1_0.dll"} {
		dll := windows.NewLazySystemDLL(name)
		if dll.Load() == nil {
			return dll.NewProc("XInputGetState")
		}
	}
	return nil
}()

// xinputState is XINPUT_STATE.
type xinputState struct {
	PacketNumber uint32
	Buttons      uint16
	LT, RT       uint8
	LX, LY       int16
	RX, RY       int16
}

// xinputRescan is how often empty slots are probed. Asking XInput about a
// slot with nothing in it is slow - it goes looking for the device - so doing
// it on every poll would eat into the polling rate for no reason.
const xinputRescan = time.Second

// xinputSource reads the first connected XInput pad: an Xbox controller, or
// any pad Steam Input or DS4Windows presents as one.
type xinputSource struct {
	slot     int // -1 while none is connected
	lastScan time.Time
}

func newXInputSource() Source {
	if xinputGetState == nil {
		return nil
	}
	return &xinputSource{slot: -1}
}

func (x *xinputSource) Kind() string { return KindXInput }

func (x *xinputSource) Name() string {
	if x.slot < 0 {
		return "XInput controller"
	}
	return fmt.Sprintf("XInput controller (slot %d)", x.slot+1)
}

func (x *xinputSource) Poll() (State, bool) {
	if x.slot >= 0 {
		if st, ok := xinputRead(x.slot); ok {
			return st, true
		}
		x.slot = -1
	}
	if time.Since(x.lastScan) < xinputRescan {
		return State{}, false
	}
	x.lastScan = time.Now()
	for slot := range 4 {
		if st, ok := xinputRead(slot); ok {
			x.slot = slot
			return st, true
		}
	}
	return State{}, false
}

func (x *xinputSource) Close() {}

func xinputRead(slot int) (State, bool) {
	var raw xinputState
	ret, _, _ := xinputGetState.Call(uintptr(slot), uintptr(unsafe.Pointer(&raw)))
	if ret != 0 {
		return State{}, false
	}
	return State{
		Buttons: uint32(raw.Buttons),
		LX:      raw.LX, LY: raw.LY,
		RX: raw.RX, RY: raw.RY,
		LT: raw.LT, RT: raw.RT,
	}, true
}
