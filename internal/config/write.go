package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// setting matches a top-level "key: value" line, ignoring anything indented -
// an indented key belongs to a block like discord_ranks and must not be
// mistaken for the setting of the same name.
var setting = regexp.MustCompile(`(?m)^([a-z_]+):[^\n]*$`)

// SetValue rewrites one top-level setting in a config file, in place.
//
// It edits the text rather than re-serialising through viper, because viper
// writes back only the values it parsed: every comment in config.yaml would be
// lost, and that file is mostly comments explaining what the settings do.
//
// A key that is not present is appended with the comment supplied. An empty
// path writes a new config beside the executable, which is where Load looks
// when none was found.
func SetValue(path, key, value, comment string) (string, error) {
	if key == "" {
		return "", fmt.Errorf("no setting named")
	}

	if path == "" {
		var err error
		path, err = defaultWritePath()
		if err != nil {
			return "", err
		}
	}

	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	text := string(existing)

	line := fmt.Sprintf("%s: %s", key, value)
	replaced := false
	updated := setting.ReplaceAllStringFunc(text, func(match string) string {
		if replaced {
			return match
		}
		name, _, _ := strings.Cut(match, ":")
		if strings.TrimSpace(name) != key {
			return match
		}
		replaced = true
		return line
	})

	if !replaced {
		var b strings.Builder
		b.WriteString(updated)
		if updated != "" && !strings.HasSuffix(updated, "\n") {
			b.WriteString("\n")
		}
		if updated != "" {
			b.WriteString("\n")
		}
		if comment != "" {
			for _, c := range strings.Split(comment, "\n") {
				b.WriteString("# " + c + "\n")
			}
		}
		b.WriteString(line + "\n")
		updated = b.String()
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create config directory: %w", err)
	}
	// Through a temporary file so an interrupted write cannot leave the user
	// with a truncated config and no way to start the app.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(updated), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("replace %s: %w", path, err)
	}
	return path, nil
}

// defaultWritePath is beside the executable, matching where Load looks when
// no config file was found - so a config written here is the one read next
// time, however the app is launched.
func defaultWritePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "config.yaml", nil
	}
	return filepath.Join(filepath.Dir(exe), "config.yaml"), nil
}
