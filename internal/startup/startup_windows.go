//go:build windows

package startup

import (
	"os"
	"syscall"
	"unsafe"
)

// attachParentProcess is ATTACH_PARENT_PROCESS, i.e. (DWORD)-1.
const attachParentProcess = uintptr(0xFFFFFFFF)

const (
	mbOK        = 0x00000000
	mbIconError = 0x00000010
)

var (
	kernel32             = syscall.NewLazyDLL("kernel32.dll")
	procAttachConsole    = kernel32.NewProc("AttachConsole")
	procAllocConsole     = kernel32.NewProc("AllocConsole")
	procGetConsoleWindow = kernel32.NewProc("GetConsoleWindow")
	procSetStdHandle     = kernel32.NewProc("SetStdHandle")

	user32          = syscall.NewLazyDLL("user32.dll")
	procMessageBoxW = user32.NewProc("MessageBoxW")
)

// HasConsole reports whether this process has a console attached.
func HasConsole() bool {
	hwnd, _, _ := procGetConsoleWindow.Call()
	return hwnd != 0
}

// AttachParent hooks the process up to the console it was launched from, if
// there is one.
//
// The binary is linked for the GUI subsystem so that double-clicking it does
// not flash up a console window. The side effect is that a run from a terminal
// gets no console either, and every write to stdout vanishes - which would
// silently break --help and the --lightweight terminal UI. Attaching to the
// parent's console restores both.
func AttachParent() bool {
	if HasConsole() {
		return true
	}
	if ok, _, _ := procAttachConsole.Call(attachParentProcess); ok == 0 {
		return false
	}
	bindStdio()
	return true
}

// Ensure guarantees a console exists, creating a new window if the process was
// started without one. It's for the terminal UI, which has nowhere to draw
// otherwise.
func Ensure() bool {
	if AttachParent() {
		return true
	}
	if ok, _, _ := procAllocConsole.Call(); ok == 0 {
		return false
	}
	bindStdio()
	return true
}

// bindStdio points Go's standard streams, and the Win32 handles underneath
// them, at the console this process just attached to. Without this the streams
// still refer to the handles the process started with, which are invalid.
//
// A stream that already works is left alone. That matters for redirection:
// `lobby-iq --help > out.txt` hands us a perfectly good file handle, and
// repointing it at the console would send the output to the screen instead of
// the file the user asked for.
func bindStdio() {
	// stdout and stderr get separate handles so that closing one - which Go
	// may do on exit - can't take the other down with it.
	for _, s := range []struct {
		which  int
		device string
		stream **os.File
	}{
		{syscall.STD_INPUT_HANDLE, "CONIN$", &os.Stdin},
		{syscall.STD_OUTPUT_HANDLE, "CONOUT$", &os.Stdout},
		{syscall.STD_ERROR_HANDLE, "CONOUT$", &os.Stderr},
	} {
		if stdHandleUsable(s.which) {
			continue
		}
		file, err := openConsole(s.device)
		if err != nil {
			continue
		}
		*s.stream = file
		setStdHandle(s.which, file.Fd())
	}
}

// RedirectStdio points stdout and stderr at f, for when there is no console to
// attach to. Without it a double-clicked process writes into null handles, so
// anything printed - a library's warning, a Go runtime crash report - is lost
// and the app appears to fail for no reason.
func RedirectStdio(f *os.File) {
	if f == nil {
		return
	}
	if !stdHandleUsable(syscall.STD_OUTPUT_HANDLE) {
		os.Stdout = f
		setStdHandle(syscall.STD_OUTPUT_HANDLE, f.Fd())
	}
	if !stdHandleUsable(syscall.STD_ERROR_HANDLE) {
		os.Stderr = f
		setStdHandle(syscall.STD_ERROR_HANDLE, f.Fd())
	}
}

// stdHandleUsable reports whether a standard handle already refers to
// something - a console, a file, or a pipe - rather than being the null handle
// a GUI-subsystem process is given when launched from Explorer.
func stdHandleUsable(which int) bool {
	h, err := syscall.GetStdHandle(which)
	if err != nil || h == 0 || h == syscall.InvalidHandle {
		return false
	}
	kind, err := syscall.GetFileType(h)
	return err == nil && kind != syscall.FILE_TYPE_UNKNOWN
}

func openConsole(name string) (*os.File, error) {
	p, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	h, err := syscall.CreateFile(p,
		syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE,
		nil, syscall.OPEN_EXISTING, 0, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(h), name), nil
}

func setStdHandle(which int, handle uintptr) {
	// Best effort: Go's own writes go through os.Stdout regardless, and this
	// only matters for code reaching for the raw Win32 handle.
	_, _, _ = procSetStdHandle.Call(uintptr(uint32(which)), handle)
}

// ReportFatal surfaces a startup failure. With a console it goes to stderr as
// usual; without one - the double-clicked case - it becomes a message box,
// because the alternative is the app appearing to do nothing at all.
func ReportFatal(title, message string) {
	if HasConsole() {
		os.Stderr.WriteString(message + "\n")
		return
	}

	titlePtr, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return
	}
	msgPtr, err := syscall.UTF16PtrFromString(message)
	if err != nil {
		return
	}
	_, _, _ = procMessageBoxW.Call(0,
		uintptr(unsafe.Pointer(msgPtr)),
		uintptr(unsafe.Pointer(titlePtr)),
		mbOK|mbIconError)
}
