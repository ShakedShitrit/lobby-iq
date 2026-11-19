//go:build windows

package inputrec

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

var (
	user32               = windows.NewLazySystemDLL("user32.dll")
	procGetAsyncKeyState = user32.NewProc("GetAsyncKeyState")
)

// gameProcessName is the executable whose window has to be in front for
// keys to be recorded.
const gameProcessName = "RocketLeague.exe"

// Virtual-key codes that only ever repeat a more specific one: Windows
// reports VK_SHIFT alongside VK_LSHIFT or VK_RSHIFT, and so on.
const (
	vkShift   = 0x10
	vkControl = 0x11
	vkMenu    = 0x12
)

// kbmSource reads the keyboard and mouse buttons - only while Rocket League
// is the window in front. Anything typed into another window, a chat or a
// password box, is never seen: the moment focus leaves the game every key
// reads as released.
//
// Mouse movement isn't recorded. The game reads it as raw input, which
// polling the cursor position can't see.
type kbmSource struct {
	pid    uint32
	inGame bool
}

func newKBMSource() Source {
	if user32.Load() != nil {
		return nil
	}
	return &kbmSource{}
}

func (k *kbmSource) Kind() string { return KindKBM }
func (k *kbmSource) Name() string { return "Keyboard and mouse" }
func (k *kbmSource) Close()       {}

func (k *kbmSource) Poll() (State, bool) {
	var pid uint32
	if hwnd := windows.GetForegroundWindow(); hwnd != 0 {
		_, _ = windows.GetWindowThreadProcessId(hwnd, &pid)
	}
	// The process name is only looked up when the foreground process
	// changes; asking on every poll would mean opening a process handle
	// hundreds of times a second.
	if pid != k.pid {
		k.pid = pid
		k.inGame = isGameProcess(pid)
	}
	if !k.inGame {
		return State{}, true
	}

	var st State
	for vk := uintptr(0x01); vk <= 0xFE; vk++ {
		if vk == vkShift || vk == vkControl || vk == vkMenu {
			continue
		}
		if ret, _, _ := procGetAsyncKeyState.Call(vk); ret&0x8000 != 0 {
			st.Keys = append(st.Keys, uint16(vk))
		}
	}
	return st, true
}

func isGameProcess(pid uint32) bool {
	if pid == 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)

	buf := make([]uint16, windows.MAX_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return false
	}
	return strings.EqualFold(filepath.Base(windows.UTF16ToString(buf[:size])), gameProcessName)
}
