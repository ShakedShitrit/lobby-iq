//go:build windows

package startup

// Link this program for the Windows GUI subsystem.
//
// A console-subsystem binary makes Windows open a terminal for the process
// whether it wants one or not, so double-clicking the exe put a black console
// window behind the UI. The subsystem is fixed by the linker, not by anything
// the program can decide at runtime.
//
// The usual fix is `go build -ldflags "-H=windowsgui"`, but that only helps
// whoever remembers to type it. Passing the flag to the external linker from
// here instead means a plain `go build` produces the right binary - there is
// nothing to remember and no build script to keep in sync.
//
// This relies on cgo, which is already required on Windows: fyne's OpenGL
// driver needs it, so CGO_ENABLED=0 could never build this program anyway.
//
// Consequence worth knowing: the process starts with no console at all, so
// stdout goes nowhere when run from a terminal. AttachParent in this package
// puts that back - see startup_windows.go.

/*
#cgo LDFLAGS: -Wl,--subsystem,windows
*/
import "C"
