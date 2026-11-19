//go:build !windows

package inputview

const (
	syncHotkeyName  = ""
	pauseHotkeyName = ""
)

// listenHotkeys does nothing off Windows; the window's buttons still work.
func listenHotkeys(onSync, onPause func()) func() { return func() {} }
