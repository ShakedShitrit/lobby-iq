package inputrec

import (
	"os"
	"testing"

	"github.com/ShakedShitrit/lobby-iq/internal/rlstats"
)

// recordOne plays a short match through s, so its directory holds a
// recording of guid.
func recordOne(t *testing.T, s *script) string {
	t.Helper()
	s.event(0, "RoundStarted", "")
	s.event(6000, "MatchEnded", "")
	return s.load().MatchGuid
}

// Watching a recorded match's replay must neither record it again (which
// would also rotate older recordings away) nor be confused with the podium
// of the match just played.
func TestReplayOfRecordedMatchIsNotRecorded(t *testing.T) {
	s := newScript(t)
	recordOne(t, s)

	var announced []string
	s.r.OnReplay(func(g, path string) { announced = append(announced, path) })

	// The podium of the match just played: same ID, but not a replay.
	s.r.Observe(rlstats.MatchState{MatchGuid: guid})
	if len(announced) != 0 || s.r.Recording() {
		t.Fatalf("podium treated as a replay (announced %v) or recorded", announced)
	}

	// Later, the replay is opened.
	s.r.Observe(rlstats.MatchState{MatchGuid: "OTHERMATCH0000000000000000000000"}) // a match in between
	s.r.Observe(rlstats.MatchState{MatchGuid: "", TargetTeamNum: -1})
	s.event(0, "RoundStarted", "")
	s.r.Observe(rlstats.MatchState{MatchGuid: guid, HistoryReplay: true})
	s.event(4000, "RoundStarted", "")

	if s.r.Recording() {
		t.Error("recording during a replay")
	}
	if len(announced) != 1 || announced[0] != s.saved[0] {
		t.Errorf("announced %v, want the one recording %v", announced, s.saved[:1])
	}
	entries, _ := os.ReadDir(s.r.Store().tmpDir())
	if len(entries) != 1 {
		t.Errorf("%d files after watching the replay, want 1", len(entries))
	}
}

// Even without the HistoryReplay flag, a match ID that already has a
// recording is a replay.
func TestKnownGuidIsTreatedAsReplay(t *testing.T) {
	s := newScript(t)
	recordOne(t, s)
	s.r.Observe(rlstats.MatchState{MatchGuid: "OTHERMATCH0000000000000000000000"})
	s.r.Observe(rlstats.MatchState{TargetTeamNum: -1})

	announced := 0
	s.r.OnReplay(func(string, string) { announced++ })
	s.r.Observe(rlstats.MatchState{MatchGuid: guid})
	if s.r.Recording() || announced != 1 {
		t.Errorf("recording = %v, announced %d times", s.r.Recording(), announced)
	}
}

// A replay of a match with no recording starts out looking like a match;
// once it's known to be a replay, what was recorded is thrown away.
func TestReplayCreatedDiscardsTake(t *testing.T) {
	s := newScript(t)
	s.event(0, "MatchCreated", "")
	if !s.r.Recording() {
		t.Fatal("not recording after MatchCreated")
	}
	s.event(100, "ReplayCreated", "")
	s.event(9000, "RoundStarted", "")
	if s.r.Recording() {
		t.Error("still recording after ReplayCreated")
	}
	if len(s.saved) != 0 {
		t.Errorf("saved %v from a replay", s.saved)
	}
}

// The repeat GoalScored sent as a goal replay ends mustn't disturb phases.
func TestRepeatGoalScoredIgnored(t *testing.T) {
	s := newScript(t)
	s.event(0, "RoundStarted", "")
	s.event(5000, "GoalScored", `{"MatchGuid":"`+guid+`","GoalSpeed":60,"Scorer":{"TeamNum":0}}`)
	s.event(8000, "GoalReplayStart", "")
	s.event(9000, "GoalScored", `{"MatchGuid":"`+guid+`","GoalSpeed":0,"Scorer":{"TeamNum":0}}`)
	s.event(9001, "GoalReplayEnd", "")
	s.event(9001, "CountdownBegin", "")
	s.event(13000, "RoundStarted", "")
	s.event(20000, "MatchEnded", "")

	rec := s.load()
	var phases []string
	for _, p := range rec.Phases {
		phases = append(phases, p.Phase)
	}
	want := []string{PhasePregame, PhaseLive, PhasePostGoal, PhaseGoalReplay, PhasePostGoal, PhaseCountdown, PhaseLive, PhaseEnded}
	if len(phases) != len(want) {
		t.Fatalf("phases = %v, want %v", phases, want)
	}
	for i := range want {
		if phases[i] != want[i] {
			t.Fatalf("phases = %v, want %v", phases, want)
		}
	}
	if len(rec.Segments) != 2 {
		t.Errorf("segments = %+v", rec.Segments)
	}
}
