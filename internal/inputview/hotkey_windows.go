//go:build windows

package inputview

import (
	"runtime"
	"unsafe"

	"go.uber.org/zap"
	"golang.org/x/sys/windows"
)

var (
	user32               = windows.NewLazySystemDLL("user32.dll")
	procRegisterHotKey   = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey = user32.NewProc("UnregisterHotKey")
	procGetMessageW      = user32.NewProc("GetMessageW")
	procPostThreadMsgW   = user32.NewProc("PostThreadMessageW")
)

const (
	wmHotkey     = 0x0312
	wmQuit       = 0x0012
	modNoRepeat  = 0x4000
	hotkeySyncID = 1
	hotkeyPlayID = 2
)

// hotkeyNames are shown in the window, so they have to match what is
// registered below.
const (
	syncHotkeyName  = "F8"
	pauseHotkeyName = "F9"
	vkF8            = 0x77
	vkF9            = 0x78
)

type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      struct{ x, y int32 }
}

// listenHotkeys registers system-wide hotkeys - they have to be, because the
// moment to press them is while watching the replay, with the game in front.
// onSync and onPause are called from a background goroutine. The returned
// func unregisters them.
func listenHotkeys(onSync, onPause func()) func() {
	threadID := make(chan uint32, 1)
	go func() {
		// Hotkey messages go to the thread that registered them, so the
		// registering and the message loop must stay on one OS thread.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		threadID <- windows.GetCurrentThreadId()

		for id, vk := range map[uintptr]uintptr{hotkeySyncID: vkF8, hotkeyPlayID: vkF9} {
			if ok, _, err := procRegisterHotKey.Call(0, id, modNoRepeat, vk); ok == 0 {
				zap.L().Warn("registering viewer hotkey", zap.Uintptr("vk", vk), zap.Error(err))
			} else {
				defer procUnregisterHotKey.Call(0, id)
			}
		}

		var m msg
		for {
			ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
			if int32(ret) <= 0 {
				return
			}
			if m.message != wmHotkey {
				continue
			}
			switch m.wParam {
			case hotkeySyncID:
				onSync()
			case hotkeyPlayID:
				onPause()
			}
		}
	}()

	tid := <-threadID
	return func() { procPostThreadMsgW.Call(uintptr(tid), wmQuit, 0, 0) }
}
