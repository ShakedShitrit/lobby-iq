//go:build !windows

package startup

import "os"

// HasConsole reports whether this process has a console attached.
func HasConsole() bool { return true }

// AttachParent is a no-op outside Windows.
func AttachParent() bool { return true }

// Ensure is a no-op outside Windows.
func Ensure() bool { return true }

// RedirectStdio is a no-op outside Windows, where a process keeps the
// terminal's streams.
func RedirectStdio(_ *os.File) {}

// ReportFatal writes a startup failure to stderr.
func ReportFatal(_, message string) {
	os.Stderr.WriteString(message + "\n")
}
