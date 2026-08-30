package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The config file is mostly comments explaining what each setting does, and
// rewriting a value must not cost the user any of them.
func TestSetValuePreservesComments(t *testing.T) {
	original := `port: 49123
log_level: info

# Discord Rich Presence. Set this to your Discord application's client ID.
discord_client_id: "1541131405763940492"

# Where the card's rank badge comes from.
discord_rank_source: config

# Your rank per gamemode.
discord_ranks:
  "1v1": "Diamond II"
  "2v2": "gc3"
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := SetValue(path, "discord_rank_source", "live", ""); err != nil {
		t.Fatal(err)
	}

	got := readFile(t, path)
	if !strings.Contains(got, "discord_rank_source: live") {
		t.Errorf("setting not updated:\n%s", got)
	}
	if strings.Contains(got, "discord_rank_source: config") {
		t.Error("old value left behind")
	}
	for _, comment := range []string{
		"# Discord Rich Presence. Set this to your Discord application's client ID.",
		"# Where the card's rank badge comes from.",
		"# Your rank per gamemode.",
	} {
		if !strings.Contains(got, comment) {
			t.Errorf("lost comment %q", comment)
		}
	}
	// Everything else must survive untouched.
	for _, line := range []string{
		"port: 49123",
		`discord_client_id: "1541131405763940492"`,
		`  "1v1": "Diamond II"`,
		`  "2v2": "gc3"`,
	} {
		if !strings.Contains(got, line) {
			t.Errorf("lost line %q", line)
		}
	}
}

// An older config predates the setting, so it has to be appended rather than
// silently doing nothing.
func TestSetValueAppendsWhenAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("port: 49123\nlog_level: info\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := SetValue(path, "discord_rank_source", "live", "explains itself"); err != nil {
		t.Fatal(err)
	}

	got := readFile(t, path)
	if !strings.Contains(got, "discord_rank_source: live") {
		t.Errorf("setting not appended:\n%s", got)
	}
	if !strings.Contains(got, "# explains itself") {
		t.Errorf("comment not written:\n%s", got)
	}
	if !strings.Contains(got, "port: 49123") {
		t.Error("existing settings lost")
	}
}

// An indented key belongs to a block. Rewriting it would corrupt the block and
// change a completely unrelated setting.
func TestSetValueIgnoresIndentedKeys(t *testing.T) {
	original := `discord_assets:
  logo: "rocket_league"
logo: "top-level"
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := SetValue(path, "logo", "changed", ""); err != nil {
		t.Fatal(err)
	}

	got := readFile(t, path)
	if !strings.Contains(got, `  logo: "rocket_league"`) {
		t.Errorf("nested key was rewritten:\n%s", got)
	}
	if !strings.Contains(got, "logo: changed") {
		t.Errorf("top-level key not rewritten:\n%s", got)
	}
}

// A missing file means a fresh install; writing one is better than failing.
func TestSetValueCreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.yaml")
	written, err := SetValue(path, "discord_rank_source", "live", "")
	if err != nil {
		t.Fatal(err)
	}
	if written != path {
		t.Errorf("wrote %q, want %q", written, path)
	}
	if !strings.Contains(readFile(t, path), "discord_rank_source: live") {
		t.Error("new file lacks the setting")
	}
}

// Repeating the command must be a no-op, not append a second copy.
func TestSetValueIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	for range 3 {
		if _, err := SetValue(path, "discord_rank_source", "live", ""); err != nil {
			t.Fatal(err)
		}
	}
	if n := strings.Count(readFile(t, path), "discord_rank_source:"); n != 1 {
		t.Errorf("setting appears %d times, want 1", n)
	}
}

// No temporary file may be left behind for the user to trip over.
func TestSetValueLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if _, err := SetValue(path, "discord_rank_source", "live", ""); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("left temporary file %q", e.Name())
		}
	}
}

// LiveRankEnabled decides which source the app uses, so its spellings matter.
func TestLiveRankEnabled(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"live", true},
		{"LIVE", true},
		{" live ", true},
		{"rlmmr", true},
		{"config", false},
		{"", false},
		{"nonsense", false},
	} {
		cfg := Config{DiscordRankSource: tc.value}
		if got := cfg.LiveRankEnabled(); got != tc.want {
			t.Errorf("LiveRankEnabled(%q) = %v, want %v", tc.value, got, tc.want)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
