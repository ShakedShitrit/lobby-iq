package rlsetup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// StatsAPIFile is the per-user file this package edits. The install-side
// DefaultStatsAPI.ini is the game's own, and is only ever read from.
const StatsAPIFile = "TAStatsAPI.ini"

// backupSuffix marks the copy taken before the first edit. Rocket League's
// config is not ours, and a user who wants the original back should not have
// to verify the game files to get it.
const backupSuffix = ".lobbyiq-backup"

// Install is a located Rocket League installation.
type Install struct {
	// Path is the install root, the directory holding Binaries and TAGame.
	Path string
	// Store is where it was found: "Epic" or "Steam".
	Store string
}

// Report describes what Apply found and did. Every field is filled in whether
// or not anything changed, so a caller can explain the outcome either way.
type Report struct {
	// ConfigPath is the file that was inspected.
	ConfigPath string
	// Install is the game installation found, valid only when Found.
	Install Install
	Found   bool
	// Running is true when the game was running during the edit, which means
	// the change will be overwritten when it closes.
	Running bool
	// Created is true when there was no config file until now.
	Created bool
	// BackupPath is the copy taken before the first edit, empty when none was
	// needed.
	BackupPath string
	// Changes is empty when the config was already correct.
	Changes []Change
}

// Wrote reports whether the file needed touching at all.
func (r Report) Wrote() bool { return r.Created || len(r.Changes) > 0 }

// OK reports whether Rocket League will actually talk to LobbyIQ after this.
//
// A running game only matters when something was written: Rocket League
// rewrites this file from memory when it closes, so a change made underneath
// it is lost. A file that was already correct has nothing to lose, and
// warning about it would make the ordinary case - installing while playing -
// look like a failure.
func (r Report) OK() bool { return !r.Running || !r.Wrote() }

// Summary renders the report as the few lines a person needs.
func (r Report) Summary() string {
	var b []byte
	add := func(format string, args ...any) {
		b = append(b, fmt.Sprintf(format+"\n", args...)...)
	}

	if r.Found {
		add("Rocket League (%s): %s", r.Install.Store, r.Install.Path)
	} else {
		add("Rocket League: not found - settings were still written, and will")
		add("  apply once the game is installed and has run at least once.")
	}

	add("Config: %s", r.ConfigPath)
	switch {
	case r.Created:
		add("Created it and enabled the Stats API.")
	case len(r.Changes) == 0:
		add("Already correct - nothing to change.")
	default:
		add("Updated:")
		for _, c := range r.Changes {
			add("  %s", c)
		}
	}
	if r.BackupPath != "" {
		add("Original saved as %s", filepath.Base(r.BackupPath))
	}
	if !r.OK() {
		add("")
		add("WARNING: Rocket League is running. It rewrites this file when it")
		add("closes, which will undo the change. Quit the game and run this")
		add("again, or the Stats API will still be off next time you play.")
	} else if r.Running {
		add("Rocket League is running, but the file already said the right")
		add("thing, so there is nothing for it to undo.")
	}
	return string(b)
}

// Plan reports what Apply would do, without writing anything.
//
// Separate from Apply so the two cannot disagree: a dry run that reasoned
// about the file independently would be describing a different edit from the
// one that eventually happens.
func Plan(port int) (Report, error) {
	report, _, _, err := inspect(port)
	return report, err
}

// inspect works out the change, returning the report along with the file's
// current and intended contents.
func inspect(port int) (report Report, existing []byte, updated string, err error) {
	if port <= 0 || port > 65535 {
		return Report{}, nil, "", fmt.Errorf("port %d is not a usable port", port)
	}

	dir, err := ConfigDir()
	if err != nil {
		return Report{}, nil, "", err
	}
	path := filepath.Join(dir, StatsAPIFile)

	report = Report{ConfigPath: path}
	report.Install, report.Found = FindRocketLeague()
	report.Running = GameRunning()

	existing, err = os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		report.Created = true
	case err != nil:
		return report, nil, "", fmt.Errorf("reading %s: %w", path, err)
	}

	updated, report.Changes = patchStatsAPI(string(existing), port)
	return report, existing, updated, nil
}

// Apply makes Rocket League export match data on the given port.
//
// The game does not have to be installed, or ever have been run: the config
// directory is created if missing, and Rocket League reads what it finds there
// on next launch. That ordering matters for an installer, which cannot assume
// the user set the game up first.
func Apply(port int) (Report, error) {
	report, existing, updated, err := inspect(port)
	if err != nil {
		return report, err
	}
	dir := filepath.Dir(report.ConfigPath)
	path := report.ConfigPath
	changes := report.Changes

	// Nothing to write is the common case once this has run once, and not
	// touching the file keeps its timestamp - which is one of the few signals
	// available when working out whether the game rewrote it.
	if len(changes) == 0 && !report.Created {
		return report, nil
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return report, fmt.Errorf("creating %s: %w", dir, err)
	}

	if len(existing) > 0 {
		backup := path + backupSuffix
		// Only the first time: running this again after a game update would
		// otherwise overwrite the pristine original with an already-patched
		// one, making the backup worthless exactly when it is wanted.
		if _, err := os.Stat(backup); errors.Is(err, os.ErrNotExist) {
			if err := os.WriteFile(backup, existing, 0o644); err == nil {
				report.BackupPath = backup
			}
			// A failed backup is not worth refusing the edit over; the game
			// can regenerate this file from its own defaults.
		}
	}

	if err := writeAtomic(path, []byte(updated)); err != nil {
		return report, fmt.Errorf("writing %s: %w", path, err)
	}
	return report, nil
}

// writeAtomic writes via a temporary file in the same directory, so a failure
// partway through leaves the original config intact rather than truncated.
// Rocket League refuses to start cleanly on a malformed config, so a torn
// write here would break the game, not just LobbyIQ.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // no-op once the rename has happened

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// Windows will not rename onto an existing file, so the old one goes
	// first. The backup above is what covers the gap.
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(name, path)
}
