// Package rlversion reads the version Rocket League was built as out of the
// installed game, so LobbyIQ can present itself to PsyNet as the same version.
//
// PsyNet rejects a sign-in whose version does not match the live client with
// "VersionMismatch", and Rocket League is patched every few weeks. The version
// compiled into dank/rlapi is therefore right only until the next patch, which
// would mean shipping a new LobbyIQ build for every one of them - or asking
// people to type a version string into their config, which they have no way of
// knowing. The game itself knows, and it is already on the machine.
//
// Nothing here touches the running game: it reads bytes out of an executable
// file on disk, which is no different from any other file read and is nothing
// anti-cheat has an opinion about.
package rlversion

import (
	"fmt"
	"os"
	"sync"

	"github.com/ShakedShitrit/lobby-iq/internal/rlsetup"
)

// Version is how a Rocket League client identifies itself to PsyNet.
type Version struct {
	// Game is the build string, e.g. "260825.79374.526531". It changes with
	// every patch.
	Game string
	// FeatureSet is the protocol level, e.g. "PrimeUpdate59_1". It changes only
	// with a numbered update. Empty means it could not be read, and the caller
	// should keep whatever it was already using.
	FeatureSet string
}

func (v Version) String() string {
	if v.FeatureSet == "" {
		return v.Game
	}
	return fmt.Sprintf("%s (%s)", v.Game, v.FeatureSet)
}

// stamp identifies the file a cached result came from. Rocket League can be
// patched while LobbyIQ is running - the launcher updates it whenever the game
// is closed - and the next reconnect after that needs the new version, so a
// result is only reused while the file it was read from is unchanged.
type stamp struct {
	path string
	size int64
	mod  int64
}

var (
	mu     sync.Mutex
	cached Version
	from   stamp
)

// Detect returns the version of the installed game. The boolean reports
// whether it could be read at all; false means the caller should carry on with
// whatever version it already has, which is the best guess available.
//
// The result is cached, so this is cheap enough to call on every connection.
// Only the file's size and timestamp are checked on a repeat call - a patched
// game changes both.
func Detect() (Version, bool) {
	install, ok := rlsetup.FindRocketLeague()
	if !ok {
		return Version{}, false
	}
	path := install.ExePath()

	info, err := os.Stat(path)
	if err != nil {
		return Version{}, false
	}
	now := stamp{path: path, size: info.Size(), mod: info.ModTime().UnixNano()}

	mu.Lock()
	defer mu.Unlock()
	if now == from && cached.Game != "" {
		return cached, true
	}

	v, found, err := scanFile(path)
	if err != nil || !found {
		return Version{}, false
	}
	cached, from = v, now
	return v, true
}

func scanFile(path string) (Version, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return Version{}, false, err
	}
	defer f.Close()
	return scan(f)
}
