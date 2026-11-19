package inputrec

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/ShakedShitrit/lobby-iq/internal/rlstats"
)

const guid = "5002996C11F184800B8C78A4C92835D5"

// script drives a Recorder with events at fixed offsets from a start time,
// so segment boundaries can be checked to the millisecond.
type script struct {
	t     *testing.T
	r     *Recorder
	start time.Time
	saved []string
}

func newScript(t *testing.T) *script {
	s := &script{t: t, start: time.Now()}
	s.r = New(Options{Dir: t.TempDir(), Keep: 5})
	// No real devices: samples are fed in directly with at().
	s.r.open = func([]string) []Source { return nil }
	s.r.OnSaved(func(p string) { s.saved = append(s.saved, p) })
	return s
}

func (s *script) event(ms int64, name string, data string) {
	if data == "" {
		data = `{"MatchGuid":"` + guid + `"}`
	}
	s.r.OnEvent(rlstats.Event{Name: name, Data: json.RawMessage(data), At: s.start.Add(time.Duration(ms) * time.Millisecond)})
}

func (s *script) input(ms int64, st State) {
	s.r.mu.Lock()
	t := s.r.cur
	s.r.mu.Unlock()
	if t == nil {
		s.t.Fatalf("input at %dms with nothing recording", ms)
	}
	s.r.sample(t, 0, s.start.Add(time.Duration(ms)*time.Millisecond), st)
}

func (s *script) load() *Recording {
	if len(s.saved) != 1 {
		s.t.Fatalf("saved %d recordings, want 1", len(s.saved))
	}
	rec, err := Load(s.saved[0])
	if err != nil {
		s.t.Fatal(err)
	}
	return rec
}

func TestRecorderSegmentsByKickoff(t *testing.T) {
	s := newScript(t)
	jump := State{Buttons: ButtonA}

	s.event(0, "MatchCreated", "")
	s.event(1000, "CountdownBegin", "")
	s.input(2000, jump) // during the countdown: in no segment
	s.event(4000, "RoundStarted", "")
	s.input(4500, State{RT: 255})
	s.event(10000, "GoalScored", `{"MatchGuid":"`+guid+`","Scorer":{"Name":"x","TeamNum":1}}`)
	s.event(11000, "GoalReplayStart", "")
	s.input(12000, jump) // during the goal replay: in no segment
	s.event(16000, "GoalReplayEnd", "")
	s.event(17000, "CountdownBegin", "")
	s.event(20000, "RoundStarted", "")
	s.input(20250, State{LX: 100})
	if !s.r.Recording() {
		t.Fatal("not recording mid-match")
	}
	s.event(30000, "MatchEnded", "")

	if s.r.Recording() {
		t.Fatal("still recording after MatchEnded")
	}
	rec := s.load()
	if !rec.Complete {
		t.Error("recording of a finished match not marked complete")
	}

	one := 1
	want := []Segment{
		{Index: 0, AnchorMs: 4000, EndMs: 10000, Anchor: "round_started", EndedBy: "goal", GoalTeam: &one},
		{Index: 1, AnchorMs: 20000, EndMs: 30000, Anchor: "round_started", EndedBy: "match_end"},
	}
	if !reflect.DeepEqual(rec.Segments, want) {
		t.Errorf("segments =\n%+v\nwant\n%+v", rec.Segments, want)
	}

	type placed struct {
		seg int
		t   int64
	}
	var got []placed
	for _, smp := range rec.Samples {
		got = append(got, placed{smp.Segment, smp.TMs})
	}
	wantPlaced := []placed{{-1, 0}, {0, 500}, {-1, 0}, {1, 250}}
	if !reflect.DeepEqual(got, wantPlaced) {
		t.Errorf("samples placed at %v, want %v", got, wantPlaced)
	}

	var phases []string
	for _, p := range rec.Phases {
		phases = append(phases, p.Phase)
	}
	wantPhases := []string{PhasePregame, PhaseCountdown, PhaseLive, PhasePostGoal, PhaseGoalReplay,
		PhasePostGoal, PhaseCountdown, PhaseLive, PhaseEnded}
	if !reflect.DeepEqual(phases, wantPhases) {
		t.Errorf("phases = %v, want %v", phases, wantPhases)
	}
}

// The ticks that follow MatchEnded - podium, scoreboard - carry the same
// match ID and must not start a second recording.
func TestRecorderDoesNotRestartFinishedMatch(t *testing.T) {
	s := newScript(t)
	s.r.Observe(rlstats.MatchState{MatchGuid: guid, Arena: "Stadium_P"})
	s.event(0, "RoundStarted", "")
	s.event(6000, "MatchEnded", "")
	s.r.Observe(rlstats.MatchState{MatchGuid: guid, Arena: "Stadium_P"})
	if s.r.Recording() {
		t.Fatal("restarted recording for a match that already ended")
	}
	if rec := s.load(); rec.Arena != "Stadium_P" {
		t.Errorf("arena = %q", rec.Arena)
	}
}

// Losing the game mid-match still saves what was recorded, flagged as such.
func TestRecorderSavesIncompleteOnDisconnect(t *testing.T) {
	s := newScript(t)
	s.r.Observe(rlstats.MatchState{MatchGuid: guid})
	s.event(0, "RoundStarted", "")
	// Observe stamps with the real clock, so wait out the too-short cut-off
	// by backdating the recording's start instead of sleeping.
	s.r.mu.Lock()
	s.r.cur.start = s.r.cur.start.Add(-time.Minute)
	s.r.mu.Unlock()
	s.r.Observe(rlstats.MatchState{TargetTeamNum: -1})

	rec := s.load()
	if rec.Complete {
		t.Error("disconnected recording marked complete")
	}
	if n := len(rec.Segments); n != 1 || rec.Segments[0].EndedBy != "recording_end" {
		t.Errorf("segments = %+v", rec.Segments)
	}
}

// Without RoundStarted, the kickoff is placed four seconds after the
// countdown began, once the clock is seen running.
func TestRecorderFallsBackToCountdownAnchor(t *testing.T) {
	s := newScript(t)
	s.event(0, "CountdownBegin", "")
	s.event(4100, "ClockUpdatedSeconds", `{"MatchGuid":"`+guid+`","TimeSeconds":299,"bOvertime":false}`)
	s.event(9000, "MatchEnded", "")

	rec := s.load()
	if len(rec.Segments) != 1 {
		t.Fatalf("segments = %+v", rec.Segments)
	}
	if seg := rec.Segments[0]; seg.AnchorMs != 4000 || seg.Anchor != "countdown" {
		t.Errorf("segment = %+v, want anchored at 4000 by countdown", seg)
	}
}

func TestRecorderNilIsSafe(t *testing.T) {
	var r *Recorder
	r.Observe(rlstats.MatchState{MatchGuid: guid})
	r.OnEvent(rlstats.Event{Name: "RoundStarted"})
	r.OnChange(func(bool) {})
	if r.Recording() {
		t.Error("nil recorder recording")
	}
}

func TestRotateKeepsNewest(t *testing.T) {
	dir := t.TempDir()
	names := []string{
		"20260101-000000_a_1" + Ext,
		"20260102-000000_a_2" + Ext,
		"20260103-000000_a_3" + Ext,
		"20260104-000000_a_4" + Ext,
		"20260105-000000_a_5" + Ext,
		"notes.txt",
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), nil, 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := rotate(dir, 3); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	var left []string
	for _, e := range entries {
		left = append(left, e.Name())
	}
	want := []string{names[2], names[3], names[4], "notes.txt"}
	if !reflect.DeepEqual(left, want) {
		t.Errorf("left %v, want %v", left, want)
	}
}

func TestSampleRoundTrip(t *testing.T) {
	in := []Sample{
		{AtMs: 12, Segment: -1, Device: 1, State: State{Buttons: ButtonA | LeftShoulder, LX: -32768, RY: 32767, LT: 3, RT: 255}},
		{AtMs: 99, Segment: 2, TMs: 40, Device: 0, State: State{Keys: []uint16{0x20, 0x57}}},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out []Sample
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("round trip:\n%+v\n%s\n%+v", in, b, out)
	}
}
