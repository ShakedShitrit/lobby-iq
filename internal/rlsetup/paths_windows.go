//go:build windows

package rlsetup

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

// gameExeRelative is where the executable sits under an install root, on both
// stores. Its presence is what makes a candidate directory an actual
// installation rather than a leftover folder.
var gameExeRelative = filepath.Join("Binaries", "Win64", "RocketLeague.exe")

// gameProcessName is matched when checking whether the game is running.
const gameProcessName = "RocketLeague.exe"

// configRelative is where Rocket League keeps its per-user config, under the
// user's Documents folder.
var configRelative = filepath.Join("My Games", "Rocket League", "TAGame", "Config")

var (
	shell32                  = syscall.NewLazyDLL("shell32.dll")
	procSHGetKnownFolderPath = shell32.NewProc("SHGetKnownFolderPath")

	ole32             = syscall.NewLazyDLL("ole32.dll")
	procCoTaskMemFree = ole32.NewProc("CoTaskMemFree")

	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	procCreateToolhelp32Snapshot = kernel32.NewProc("CreateToolhelp32Snapshot")
	procProcess32FirstW          = kernel32.NewProc("Process32FirstW")
	procProcess32NextW           = kernel32.NewProc("Process32NextW")
)

// guid is a Windows GUID, laid out as the API expects rather than as a string.
type guid struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

// folderIDDocuments is FOLDERID_Documents, {FDD39AD0-238F-46AF-ADB4-6C85480369C7}.
var folderIDDocuments = guid{
	0xFDD39AD0, 0x238F, 0x46AF,
	[8]byte{0xAD, 0xB4, 0x6C, 0x85, 0x48, 0x03, 0x69, 0xC7},
}

// DocumentsDir returns the user's Documents folder.
//
// Asked of Windows rather than built from %USERPROFILE%, because Documents is
// routinely redirected - OneDrive moves it to %USERPROFILE%\OneDrive\Documents
// on a great many machines, and enterprise policy can put it on a network
// share. Guessing the path finds nothing there and would leave the caller
// writing a config Rocket League never reads.
func DocumentsDir() (string, error) {
	// Held as *uint16 rather than uintptr: a uintptr is just a number as far
	// as the garbage collector is concerned, and converting one back to a
	// pointer to read through is exactly what go vet's unsafe.Pointer check
	// exists to catch.
	var pathPtr *uint16
	ret, _, _ := procSHGetKnownFolderPath.Call(
		uintptr(unsafe.Pointer(&folderIDDocuments)),
		0, // no special behaviour: the current, possibly redirected, path
		0, // current user
		uintptr(unsafe.Pointer(&pathPtr)),
	)
	if ret != 0 || pathPtr == nil {
		return "", errors.New("could not locate your Documents folder")
	}
	// The shell allocated this and expects it back regardless of what we do
	// with the contents.
	defer procCoTaskMemFree.Call(uintptr(unsafe.Pointer(pathPtr)))

	return syscall.UTF16ToString(utf16Slice(pathPtr)), nil
}

// utf16Slice views a NUL-terminated wide string as a slice, without copying.
func utf16Slice(p *uint16) []uint16 {
	// Bounded only by the terminator, which is the contract of the API that
	// produced it; the length is not reported separately. 32768 is the longest
	// path Windows will hand back.
	const maxChars = 32768
	out := unsafe.Slice(p, maxChars)
	for i, c := range out {
		if c == 0 {
			return out[:i]
		}
	}
	return out
}

// ConfigDir returns the directory holding Rocket League's per-user config.
// It may not exist: the game creates it on first run.
func ConfigDir() (string, error) {
	docs, err := DocumentsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(docs, configRelative), nil
}

// FindRocketLeague locates an installation, or reports false if there is none.
//
// Only advisory: the file this package edits lives under Documents and can be
// written whether or not the game is found. A failed search means "warn the
// user", not "give up" - someone may be installing LobbyIQ before the game, or
// running a store neither branch below knows about.
func FindRocketLeague() (Install, bool) {
	if in, ok := findEpic(); ok {
		return in, true
	}
	return findSteam()
}

// epicManifest is the part of an Epic .item manifest worth reading.
type epicManifest struct {
	InstallLocation string
	DisplayName     string
}

// findEpic reads the Epic launcher's manifests.
//
// Matched on the executable rather than on DisplayName: the name is localised,
// carries a registered-trademark sign, and arrives mojibaked often enough that
// matching it is a losing game.
func findEpic() (Install, bool) {
	programData := os.Getenv("ProgramData")
	if programData == "" {
		programData = `C:\ProgramData`
	}
	dir := filepath.Join(programData, "Epic", "EpicGamesLauncher", "Data", "Manifests")

	entries, err := os.ReadDir(dir)
	if err != nil {
		return Install{}, false
	}
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".item") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var m epicManifest
		if json.Unmarshal(b, &m) != nil || m.InstallLocation == "" {
			continue
		}
		if hasGameExe(m.InstallLocation) {
			return Install{Path: m.InstallLocation, Store: "Epic"}, true
		}
	}
	return Install{}, false
}

// findSteam walks Steam's library folders looking for the game.
func findSteam() (Install, bool) {
	steam, ok := steamPath()
	if !ok {
		return Install{}, false
	}
	for _, lib := range steamLibraries(steam) {
		root := filepath.Join(lib, "steamapps", "common", "rocketleague")
		if hasGameExe(root) {
			return Install{Path: root, Store: "Steam"}, true
		}
	}
	return Install{}, false
}

// steamPath reads Steam's own install location from the registry.
func steamPath() (string, bool) {
	for _, k := range []struct {
		root syscall.Handle
		path string
		name string
	}{
		{syscall.HKEY_CURRENT_USER, `Software\Valve\Steam`, "SteamPath"},
		{syscall.HKEY_LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Valve\Steam`, "InstallPath"},
	} {
		if v, ok := regString(k.root, k.path, k.name); ok && v != "" {
			return filepath.FromSlash(v), true
		}
	}
	return "", false
}

// steamLibraries returns every library folder Steam knows about, the main one
// included. Games are routinely on a different drive from Steam itself.
func steamLibraries(steam string) []string {
	libs := []string{steam}

	b, err := os.ReadFile(filepath.Join(steam, "steamapps", "libraryfolders.vdf"))
	if err != nil {
		return libs
	}
	// Read with a targeted scan rather than a VDF parser: the one field needed
	// is "path", and pulling in a parser for a handful of quoted strings would
	// cost more than it explains.
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, `"path"`) {
			continue
		}
		parts := strings.Split(line, `"`)
		if len(parts) < 4 {
			continue
		}
		// VDF escapes backslashes, so the value arrives doubled.
		p := strings.ReplaceAll(parts[3], `\\`, `\`)
		if p != "" && !strings.EqualFold(p, steam) {
			libs = append(libs, p)
		}
	}
	return libs
}

// hasGameExe reports whether root looks like a Rocket League installation.
func hasGameExe(root string) bool {
	st, err := os.Stat(filepath.Join(root, gameExeRelative))
	return err == nil && !st.IsDir()
}

// regString reads one string value, returning false rather than an error for
// the ordinary case of it not being there.
func regString(root syscall.Handle, path, name string) (string, bool) {
	var h syscall.Handle
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return "", false
	}
	if syscall.RegOpenKeyEx(root, p, 0, syscall.KEY_READ, &h) != nil {
		return "", false
	}
	defer syscall.RegCloseKey(h)

	n, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return "", false
	}
	var typ, size uint32
	if syscall.RegQueryValueEx(h, n, nil, &typ, nil, &size) != nil || typ != syscall.REG_SZ {
		return "", false
	}
	buf := make([]uint16, size/2+1)
	if syscall.RegQueryValueEx(h, n, nil, &typ, (*byte)(unsafe.Pointer(&buf[0])), &size) != nil {
		return "", false
	}
	return syscall.UTF16ToString(buf), true
}

// processEntry is PROCESSENTRY32W. Only the fields up to the name are used,
// but the whole struct has to be declared: dwSize is checked by the API and
// the layout is fixed.
type processEntry struct {
	Size            uint32
	Usage           uint32
	ProcessID       uint32
	DefaultHeapID   uintptr
	ModuleID        uint32
	Threads         uint32
	ParentProcessID uint32
	PriClassBase    int32
	Flags           uint32
	ExeFile         [260]uint16
}

// GameRunning reports whether Rocket League is running.
//
// It matters because the game holds its config in memory and writes it back on
// exit, so an edit made while it is running is overwritten the moment it
// closes - leaving a user who followed every instruction with nothing working
// and no idea why.
func GameRunning() bool {
	const th32csSnapProcess = 0x00000002
	const invalidHandle = ^uintptr(0)

	snap, _, _ := procCreateToolhelp32Snapshot.Call(th32csSnapProcess, 0)
	if snap == invalidHandle || snap == 0 {
		// Unknowable rather than false, but the caller's only use is a
		// warning, and warning about something we cannot confirm is worse
		// than staying quiet.
		return false
	}
	defer syscall.CloseHandle(syscall.Handle(snap))

	var e processEntry
	e.Size = uint32(unsafe.Sizeof(e))

	ok, _, _ := procProcess32FirstW.Call(snap, uintptr(unsafe.Pointer(&e)))
	for ok != 0 {
		if strings.EqualFold(syscall.UTF16ToString(e.ExeFile[:]), gameProcessName) {
			return true
		}
		ok, _, _ = procProcess32NextW.Call(snap, uintptr(unsafe.Pointer(&e)))
	}
	return false
}
