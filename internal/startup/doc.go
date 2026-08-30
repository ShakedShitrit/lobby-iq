// Package startup handles the process-level setup that differs between a
// double-clicked desktop app and a run from a terminal.
//
// Only Windows needs any of it: elsewhere a process keeps the terminal it was
// started from, so everything here is a no-op.
package startup
