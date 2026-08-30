package session

import (
	"testing"

	"github.com/ShakedShitrit/lobby-iq/internal/rlstats"
)

var teamNames = map[int]string{0: "Blue", 1: "Orange"}

// playing builds an in-progress tick of a teamSize-per-side match where the
// camera follows myTeam.
func playing(guid string, teamSize, myTeam int) rlstats.MatchState {
	var players []rlstats.Player
	for team := 0; team < 2; team++ {
		for i := 0; i < teamSize; i++ {
			players = append(players, rlstats.Player{TeamNum: team})
		}
	}
	return rlstats.MatchState{
		MatchGuid:     guid,
		Teams:         teamNames,
		Scores:        map[int]int{0: 0, 1: 0},
		TargetTeamNum: myTeam,
		Players:       players,
	}
}

// finish plays out a match to completion, won by winner.
func finish(t *testing.T, tr *Tracker, guid string, teamSize, myTeam, winner int) {
	t.Helper()
	tick := playing(guid, teamSize, myTeam)
	tr.Observe(tick)

	tick.Scores = map[int]int{winner: 1, 1 - winner: 0}
	tick.Winner = teamNames[winner]
	tick.Ended = true
	tr.Observe(tick)
}

func TestNetTallyPerGamemode(t *testing.T) {
	tr := New()

	// 3 wins, then 5 losses, all 2v2 -> net -2.
	for i := 0; i < 3; i++ {
		finish(t, tr, "w"+string(rune('a'+i)), 2, 0, 0)
	}
	for i := 0; i < 5; i++ {
		finish(t, tr, "l"+string(rune('a'+i)), 2, 0, 1)
	}
	// A 3v3 win keeps its own tally.
	finish(t, tr, "3v3-win", 3, 1, 1)

	stats := tr.Snapshot()
	if len(stats) != 2 {
		t.Fatalf("got %d modes, want 2: %+v", len(stats), stats)
	}
	if stats[0].Mode != "2v2" || stats[0].Net() != -2 {
		t.Errorf("got %s %s, want 2v2 -2", stats[0].Mode, stats[0])
	}
	if stats[1].Mode != "3v3" || stats[1].Net() != 1 {
		t.Errorf("got %s %s, want 3v3 +1", stats[1].Mode, stats[1])
	}
	if got := tr.Total(); got.Net() != -1 {
		t.Errorf("total net = %s, want -1", got)
	}
}

func TestMatchCountedOnce(t *testing.T) {
	tr := New()
	tick := playing("g1", 2, 0)
	tr.Observe(tick)

	tick.Scores = map[int]int{0: 3, 1: 1}
	tick.Winner = "Blue"
	tick.Ended = true
	for i := 0; i < 5; i++ {
		tr.Observe(tick)
	}

	if got := tr.Total(); got.Wins != 1 || got.Losses != 0 {
		t.Errorf("got %d-%d, want 1-0", got.Wins, got.Losses)
	}
}

// A goal replay briefly follows the scorer, who may be an opponent. The team
// followed for most of the match is the one that counts.
func TestReplayCameraDoesNotFlipTeam(t *testing.T) {
	tr := New()
	for i := 0; i < 10; i++ {
		tr.Observe(playing("g1", 3, 0))
	}
	tr.Observe(playing("g1", 3, 1)) // replay follows an Orange scorer

	end := playing("g1", 3, 1)
	end.Scores = map[int]int{0: 4, 1: 2}
	end.Winner = "Blue"
	end.Ended = true
	tr.Observe(end)

	if got := tr.Total(); got.Wins != 1 {
		t.Errorf("got %d-%d, want 1-0 (Blue win from Blue's camera)", got.Wins, got.Losses)
	}
}

// A leaver shrinking a 3v3 to 2v3 must not relabel the match as 2v2.
func TestLeaverKeepsGamemode(t *testing.T) {
	tr := New()
	tr.Observe(playing("g1", 3, 0))

	end := rlstats.MatchState{
		MatchGuid:     "g1",
		Teams:         teamNames,
		Scores:        map[int]int{0: 5, 1: 1},
		Winner:        "Blue",
		Ended:         true,
		TargetTeamNum: 0,
		Players: []rlstats.Player{
			{TeamNum: 0}, {TeamNum: 0}, {TeamNum: 0},
			{TeamNum: 1}, {TeamNum: 1},
		},
	}
	tr.Observe(end)

	stats := tr.Snapshot()
	if len(stats) != 1 || stats[0].Mode != "3v3" {
		t.Fatalf("got %+v, want a single 3v3 tally", stats)
	}
}

func TestUnfinishedAndUnknownMatchesIgnored(t *testing.T) {
	tr := New()

	tr.Observe(rlstats.MatchState{TargetTeamNum: -1}) // disconnect
	tr.Observe(playing("g1", 2, 0))                   // still in progress
	tr.Observe(playing("g2", 2, 0))                   // abandoned

	// Ended, but 0-0 with no reported winner: nothing to record.
	tie := playing("g3", 2, 0)
	tie.Ended = true
	tr.Observe(tie)

	// Ended, but the game never reported a target, so "my team" is unknown.
	noTarget := playing("g4", 2, -1)
	noTarget.Scores = map[int]int{0: 2, 1: 0}
	noTarget.Winner = "Blue"
	noTarget.Ended = true
	tr.Observe(noTarget)

	if got := tr.Total(); got.Wins != 0 || got.Losses != 0 {
		t.Errorf("got %d-%d, want 0-0", got.Wins, got.Losses)
	}
}

func TestReset(t *testing.T) {
	tr := New()
	finish(t, tr, "g1", 2, 0, 0)
	tr.Reset()

	if got := tr.Snapshot(); len(got) != 0 {
		t.Errorf("got %+v, want empty after reset", got)
	}
	if got := tr.Total(); got.Net() != 0 {
		t.Errorf("total = %s, want 0 after reset", got)
	}
}

func TestStatStringSigned(t *testing.T) {
	for _, tc := range []struct {
		wins, losses int
		want         string
	}{
		{3, 0, "+3"},
		{3, 5, "-2"},
		{2, 2, "+0"},
	} {
		got := Stat{Wins: tc.wins, Losses: tc.losses}.String()
		if got != tc.want {
			t.Errorf("Stat{%d,%d}.String() = %s, want %s", tc.wins, tc.losses, got, tc.want)
		}
	}
}
