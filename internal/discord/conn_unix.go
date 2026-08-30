//go:build !windows

package discord

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
)

// ipcSlots is how many sockets Discord may listen on. It uses the first free
// one, so several clients (stable, PTB, Canary) can coexist.
const ipcSlots = 10

// sandboxDirs are the extra subdirectories Flatpak and Snap installs of
// Discord place their socket in, relative to the runtime dir.
var sandboxDirs = []string{"", "app/com.discordapp.Discord", "snap.discord"}

// dialIPC connects to Discord's unix domain socket. Not the platform
// LobbyIQ is really aimed at - Rocket League's Stats API is Windows-only -
// but keeping the package buildable everywhere keeps `go vet ./...` and CI on
// other machines honest.
func dialIPC() (io.ReadWriteCloser, error) {
	var lastErr error = errors.New("no runtime directory found")
	for _, base := range runtimeDirs() {
		for _, sub := range sandboxDirs {
			for i := 0; i < ipcSlots; i++ {
				path := filepath.Join(base, sub, fmt.Sprintf("discord-ipc-%d", i))
				conn, err := net.Dial("unix", path)
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
		}
	}
	return nil, fmt.Errorf("no discord ipc socket found (is discord running?): %w", lastErr)
}

func runtimeDirs() []string {
	var dirs []string
	for _, env := range []string{"XDG_RUNTIME_DIR", "TMPDIR", "TMP", "TEMP"} {
		if v := os.Getenv(env); v != "" {
			dirs = append(dirs, v)
		}
	}
	return append(dirs, "/tmp")
}
