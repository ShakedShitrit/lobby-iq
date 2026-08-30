//go:build windows

package discord

import (
	"fmt"
	"io"
	"os"
)

// ipcSlots is how many sockets Discord may listen on. It uses the first free
// one, so several clients (stable, PTB, Canary) can coexist.
const ipcSlots = 10

// dialIPC opens Discord's named pipe. A named pipe is openable as an ordinary
// file on Windows, so this needs no winio dependency - but the handle is a
// synchronous one, and Go guards reads and writes to those with a single lock
// per file. A read and a write can therefore never overlap: a goroutine parked
// in a blocking read would deadlock every subsequent write. That is why the
// client is strictly request/response, and why aborting a stuck read means
// closing the handle (see readTimeout) rather than setting a deadline.
func dialIPC() (io.ReadWriteCloser, error) {
	var lastErr error
	for i := 0; i < ipcSlots; i++ {
		path := fmt.Sprintf(`\\.\pipe\discord-ipc-%d`, i)
		f, err := os.OpenFile(path, os.O_RDWR, 0)
		if err == nil {
			return f, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("no discord ipc pipe found (is discord running?): %w", lastErr)
}
