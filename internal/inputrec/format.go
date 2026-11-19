package inputrec

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// FormatVersion is bumped whenever a reader of an older version would
// misread a newer file.
const FormatVersion = 1

// Ext ends every recording's file name. Rotation only ever deletes files
// ending in it, so nothing else that happens to share the folder is at risk.
const Ext = ".lirec.json"

// Recording is one match's inputs. All times are milliseconds since the
// recording started, on one monotonic clock shared by samples and events.
type Recording struct {
	Version   int       `json:"version"`
	MatchGuid string    `json:"match_guid"`
	Arena     string    `json:"arena"`
	StartedAt time.Time `json:"started_at"`
	// Complete is false when the recording stopped for any reason other than
	// the match ending - leaving early, the game closing.
	Complete bool `json:"complete"`
	// ReplayFile and ReplayName identify the saved replay this recording
	// was kept for, once it has been.
	ReplayFile string      `json:"replay_file,omitempty"`
	ReplayName string      `json:"replay_name,omitempty"`
	PollHz     int         `json:"poll_hz"`
	DurationMs int64       `json:"duration_ms"`
	Devices    []Device    `json:"devices"`
	Segments   []Segment   `json:"segments"`
	Phases     []PhaseMark `json:"phases"`
	Events     []EventMark `json:"events"`
	Samples    []Sample    `json:"samples"`
}

// Segment is one stretch of continuous play, from a kickoff's "GO" until
// whatever stopped play. Within it, recording time and replay time advance
// together, which is what lets it be synced to a saved replay.
type Segment struct {
	Index    int   `json:"index"`
	AnchorMs int64 `json:"anchor_ms"`
	EndMs    int64 `json:"end_ms"`
	// Anchor says how AnchorMs was found, and so how far to trust it:
	// "round_started" is the game's own kickoff event; "countdown" is three
	// seconds after the countdown began; "clock" is a second before the clock
	// was first seen moving; "unpaused" is when a pause ended; "none" means
	// no kickoff was seen at all and the segment is the whole recording.
	Anchor        string `json:"anchor"`
	ClockAtAnchor int    `json:"clock_at_anchor"`
	Overtime      bool   `json:"overtime"`
	// EndedBy is "goal", "match_end", "paused", "countdown" (a new kickoff
	// began without the stoppage being reported) or "recording_end".
	EndedBy string `json:"ended_by"`
	// GoalTeam is the scoring team's TeamNum when EndedBy is "goal".
	GoalTeam *int `json:"goal_team,omitempty"`
}

// DurationMs is how long the segment's play lasted.
func (s Segment) DurationMs() int64 { return s.EndMs - s.AnchorMs }

// Phases of a match, as recorded in Recording.Phases.
const (
	PhasePregame    = "pregame"
	PhaseCountdown  = "countdown"
	PhaseLive       = "live"
	PhasePostGoal   = "post_goal"
	PhaseGoalReplay = "goal_replay"
	PhasePaused     = "paused"
	PhaseEnded      = "ended"
)

// PhaseMark records the match entering a phase.
type PhaseMark struct {
	AtMs  int64  `json:"at_ms"`
	Phase string `json:"phase"`
}

// EventMark is a Stats API event as it arrived, kept so a recording can be
// re-aligned later by whatever means turn out to work best - ball hits make
// good landmarks to check an alignment against.
type EventMark struct {
	AtMs int64           `json:"at_ms"`
	Name string          `json:"name"`
	Data json.RawMessage `json:"data,omitempty"`
}

// Sample is a device's state from AtMs until its next sample. Samples are
// only taken when the state changes, so a held input is one sample.
//
// Segment and TMs place it for replay sync: the segment it falls in (-1 for
// none - countdowns, goal replays) and its offset from that segment's anchor.
type Sample struct {
	AtMs    int64
	Segment int
	TMs     int64
	Device  int
	State   State
}

// sampleFields is the length of a sample row without the trailing key list.
const sampleFields = 11

// MarshalJSON writes a sample as a positional array,
// [at_ms, segment, t_ms, device, buttons, lx, ly, rx, ry, lt, rt, keys?] -
// a match has tens of thousands of these, and repeating field names in each
// would make the file several times larger. keys is present only when some
// are held.
func (s Sample) MarshalJSON() ([]byte, error) {
	row := []any{s.AtMs, s.Segment, s.TMs, s.Device,
		s.State.Buttons, s.State.LX, s.State.LY, s.State.RX, s.State.RY,
		s.State.LT, s.State.RT}
	if len(s.State.Keys) > 0 {
		row = append(row, s.State.Keys)
	}
	return json.Marshal(row)
}

// UnmarshalJSON reads the array MarshalJSON writes.
func (s *Sample) UnmarshalJSON(b []byte) error {
	var row []json.RawMessage
	if err := json.Unmarshal(b, &row); err != nil {
		return err
	}
	if len(row) < sampleFields {
		return fmt.Errorf("sample has %d fields, want at least %d", len(row), sampleFields)
	}
	fields := []any{&s.AtMs, &s.Segment, &s.TMs, &s.Device,
		&s.State.Buttons, &s.State.LX, &s.State.LY, &s.State.RX, &s.State.RY,
		&s.State.LT, &s.State.RT}
	for i, f := range fields {
		if err := json.Unmarshal(row[i], f); err != nil {
			return fmt.Errorf("sample field %d: %w", i, err)
		}
	}
	s.State.Keys = nil
	if len(row) > sampleFields {
		if err := json.Unmarshal(row[sampleFields], &s.State.Keys); err != nil {
			return fmt.Errorf("sample keys: %w", err)
		}
	}
	return nil
}

// Load reads a recording from path.
func Load(path string) (*Recording, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rec Recording
	if err := json.Unmarshal(b, &rec); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", filepath.Base(path), err)
	}
	if rec.Version > FormatVersion {
		return nil, fmt.Errorf("%s is format version %d; this build reads up to %d",
			filepath.Base(path), rec.Version, FormatVersion)
	}
	return &rec, nil
}

// save writes rec into dir, atomically, and returns the path written.
func save(dir string, rec *Recording) (string, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, FileName(rec))
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return path, nil
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// FileName is the name a recording is saved under:
// "<UTC yyyymmdd-hhmmss>_<arena>_<first 8 of guid>.lirec.json". Leading with
// the start time makes names sort oldest first, which is what rotation relies
// on.
func FileName(rec *Recording) string {
	arena := unsafeName.ReplaceAllString(rec.Arena, "")
	if arena == "" {
		arena = "unknown"
	}
	guid := unsafeName.ReplaceAllString(rec.MatchGuid, "")
	if len(guid) > 8 {
		guid = guid[:8]
	}
	if guid == "" {
		guid = "noguid"
	}
	return fmt.Sprintf("%s_%s_%s%s", rec.StartedAt.UTC().Format("20060102-150405"), arena, guid, Ext)
}

// rotate deletes all but the newest keep recordings in dir. Anything not
// named like a recording is left alone. keep <= 0 keeps everything.
func rotate(dir string, keep int) error {
	if keep <= 0 {
		return nil
	}
	paths, err := listDir(dir)
	if err != nil {
		return err
	}
	var errs []error
	for _, p := range paths[min(keep, len(paths)):] {
		if err := os.Remove(p); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
