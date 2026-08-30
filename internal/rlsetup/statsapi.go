// Package rlsetup makes Rocket League talk to LobbyIQ: it finds the game's
// per-user config and makes sure the Stats API is switched on and pointed at
// the port LobbyIQ listens on.
//
// It exists as a package rather than as installer scripting because the same
// work is needed more than once. Rocket League regenerates TAStatsAPI.ini from
// the game's own DefaultStatsAPI.ini when the version stamp in [IniVersion]
// changes, so a game update can silently undo an install-time edit; running
// this again puts it back.
package rlsetup

import (
	"fmt"
	"strconv"
	"strings"
)

// StatsAPISection is the ini section Rocket League reads exporter settings
// from. The name is the game's own class name and is not ours to choose.
const StatsAPISection = "TAGame.MatchStatsExporter_TA"

const (
	keyPort           = "Port"
	keyPacketSendRate = "PacketSendRate"
)

// defaultPacketSendRate is what the game itself ships in DefaultStatsAPI.ini.
// Used when the setting is missing or disabled; an existing rate above zero is
// left alone, since anyone who raised it did so deliberately.
const defaultPacketSendRate = 2

// Change describes one setting this package would write.
type Change struct {
	Key  string
	From string // empty when the key was absent
	To   string
}

func (c Change) String() string {
	if c.From == "" {
		return fmt.Sprintf("%s=%s (was unset)", c.Key, c.To)
	}
	return fmt.Sprintf("%s: %s -> %s", c.Key, c.From, c.To)
}

// patchStatsAPI returns content with the Stats API settings corrected for the
// given port, along with the changes it made.
//
// The whole file is preserved apart from the specific values changed:
// TAStatsAPI.ini is the game's file, not ours, and it carries settings and an
// [IniVersion] stamp that mean something to Rocket League. Rewriting it from a
// template would throw those away.
func patchStatsAPI(content string, port int) (string, []Change) {
	lines, ending := splitLines(content)

	var (
		changes    []Change
		inSection  bool
		sectionEnd = -1 // index after the last line of our section
		seenPort   bool
		seenRate   bool
	)

	for i, line := range lines {
		if name, ok := sectionHeader(line); ok {
			// A new header closes whatever came before it, so our section
			// ends here if we were in it.
			if inSection {
				sectionEnd = i
			}
			inSection = strings.EqualFold(name, StatsAPISection)
			continue
		}
		if !inSection {
			continue
		}

		key, value, ok := keyValue(line)
		if !ok {
			continue
		}

		switch {
		case strings.EqualFold(key, keyPort):
			// A duplicate key would leave the game reading one value while we
			// checked another, so every occurrence is rewritten.
			seenPort = true
			want := strconv.Itoa(port)
			if value != want {
				changes = append(changes, Change{Key: keyPort, From: value, To: want})
				lines[i] = keyPort + "=" + want
			}

		case strings.EqualFold(key, keyPacketSendRate):
			seenRate = true
			// Zero disables the update stream entirely, which is the one way
			// this file can be "configured" and still tell LobbyIQ nothing.
			// Any positive rate is the user's own choice.
			if n, err := strconv.Atoi(strings.TrimSpace(value)); err != nil || n <= 0 {
				want := strconv.Itoa(defaultPacketSendRate)
				changes = append(changes, Change{Key: keyPacketSendRate, From: value, To: want})
				lines[i] = keyPacketSendRate + "=" + want
			}
		}
	}
	if inSection {
		sectionEnd = len(lines)
	}

	// Anything missing is appended to the section, or the section itself is
	// added when the file has none - which is the case for a file the game has
	// never written.
	var missing []string
	if !seenPort {
		want := strconv.Itoa(port)
		changes = append(changes, Change{Key: keyPort, To: want})
		missing = append(missing, keyPort+"="+want)
	}
	if !seenRate {
		want := strconv.Itoa(defaultPacketSendRate)
		changes = append(changes, Change{Key: keyPacketSendRate, To: want})
		missing = append(missing, keyPacketSendRate+"="+want)
	}

	if len(missing) > 0 {
		if sectionEnd < 0 {
			lines = append(lines, "["+StatsAPISection+"]")
			lines = append(lines, missing...)
		} else {
			// Inserted at the end of the existing section rather than at the
			// end of the file, where it would land inside whichever section
			// happens to be last.
			rest := append([]string{}, lines[sectionEnd:]...)
			lines = append(lines[:sectionEnd], append(missing, rest...)...)
		}
	}

	return strings.Join(lines, ending), changes
}

// splitLines splits content into lines, reporting the line ending to rejoin
// with. Rocket League writes CRLF; preserving whatever is already there keeps
// the file from turning into one big diff.
func splitLines(content string) ([]string, string) {
	ending := "\r\n"
	if !strings.Contains(content, "\r\n") && strings.Contains(content, "\n") {
		ending = "\n"
	}
	if content == "" {
		return nil, ending
	}
	normalised := strings.ReplaceAll(content, "\r\n", "\n")
	return strings.Split(normalised, "\n"), ending
}

// sectionHeader returns the name inside a [Section] line.
func sectionHeader(line string) (string, bool) {
	t := strings.TrimSpace(line)
	if len(t) >= 2 && t[0] == '[' && t[len(t)-1] == ']' {
		return t[1 : len(t)-1], true
	}
	return "", false
}

// keyValue splits a "Key=Value" line, skipping comments. Rocket League's own
// files comment with ';'.
func keyValue(line string) (key, value string, ok bool) {
	t := strings.TrimSpace(line)
	if t == "" || strings.HasPrefix(t, ";") || strings.HasPrefix(t, "#") {
		return "", "", false
	}
	i := strings.Index(t, "=")
	if i < 0 {
		return "", "", false
	}
	return strings.TrimSpace(t[:i]), strings.TrimSpace(t[i+1:]), true
}
