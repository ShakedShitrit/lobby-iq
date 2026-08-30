package lobby

import (
	"testing"
	"time"

	"github.com/ShakedShitrit/lobby-iq/internal/rlstats"
	"github.com/dank/rlapi"
)

func state(teams ...int) rlstats.MatchState {
	var s rlstats.MatchState
	for team, n := range teams {
		for i := 0; i < n; i++ {
			s.Players = append(s.Players, rlstats.Player{TeamNum: team})
		}
	}
	return s
}

func TestPlaylistForUsesLargestTeam(t *testing.T) {
	tests := []struct {
		name  string
		state rlstats.MatchState
		want  int
		ok    bool
	}{
		{"duel", state(1, 1), 10, true},
		{"doubles", state(2, 2), 11, true},
		{"standard", state(3, 3), 13, true},
		// A leaver must not turn a 3v3 into a 2v2 partway through and send the
		// whole roster's ratings to the wrong playlist.
		{"leaver keeps the larger playlist", state(3, 2), 13, true},
		{"no players", state(), 0, false},
		{"unsupported size", state(4, 4), 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := playlistFor(tt.state)
			if ok != tt.ok || got != tt.want {
				t.Errorf("playlistFor() = %d, %v; want %d, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

// newIdle builds a Source with no lookup goroutine, so Observe can be tested
// without a backend session behind it.
func newIdle() *Source {
	return &Source{
		queue:  make(chan struct{}, 1),
		byKey:  map[key]Entry{},
		known:  map[rlapi.PlayerID]bool{},
		failed: map[rlapi.PlayerID]time.Time{},
	}
}

func TestObserveQueuesUnknownPlayers(t *testing.T) {
	s := newIdle()
	st := rlstats.MatchState{Players: []rlstats.Player{
		{PrimaryId: "Epic|aaa|0", TeamNum: 0},
		{PrimaryId: "Epic|bbb|0", TeamNum: 1},
		// The game reports players with no ID briefly as a match forms. There
		// is nothing to look up for them, and querying an empty ID would just
		// spend a round trip to be told so.
		{PrimaryId: "", TeamNum: 1},
	}}

	s.Observe(st)

	if got := len(s.want); got != 2 {
		t.Fatalf("queued %d players, want 2: %v", got, s.want)
	}
	// A player whose ID has not arrived yet still occupies a slot, so the
	// roster above is a 2v2 forming - not a 1v1. Sizing off the slots rather
	// than off the identified players is what stops the playlist flipping as
	// the lobby fills.
	if got, ok := s.PlaylistFor(st); !ok || got != 11 {
		t.Errorf("PlaylistFor() = %d, %v; want 11 (2v2), true", got, ok)
	}
	select {
	case <-s.queue:
	default:
		t.Error("Observe did not signal the lookup goroutine")
	}
}

func TestObserveSkipsPlayersAlreadyKnown(t *testing.T) {
	s := newIdle()
	st := rlstats.MatchState{Players: []rlstats.Player{
		{PrimaryId: "Epic|aaa|0", TeamNum: 0},
		{PrimaryId: "Epic|bbb|0", TeamNum: 1},
	}}
	s.known["Epic|aaa|0"] = true

	s.Observe(st)

	if len(s.want) != 1 || s.want[0] != "Epic|bbb|0" {
		t.Errorf("queued %v, want only Epic|bbb|0", s.want)
	}
}

// Observe runs on every roster tick, so a whole lobby already read must queue
// nothing at all - otherwise a match generates a lookup pass per tick.
func TestObserveQueuesNothingWhenAllKnown(t *testing.T) {
	s := newIdle()
	st := rlstats.MatchState{Players: []rlstats.Player{
		{PrimaryId: "Epic|aaa|0", TeamNum: 0},
		{PrimaryId: "Epic|bbb|0", TeamNum: 1},
	}}
	s.known["Epic|aaa|0"] = true
	s.known["Epic|bbb|0"] = true

	s.Observe(st)

	select {
	case <-s.queue:
		t.Error("Observe signalled a lookup with nothing to look up")
	default:
	}
}

// A player the backend refused is retried, but not on the very next tick.
func TestObserveHoldsOffRetryingFailures(t *testing.T) {
	s := newIdle()
	st := rlstats.MatchState{Players: []rlstats.Player{
		{PrimaryId: "Epic|aaa|0", TeamNum: 0},
		{PrimaryId: "Epic|bbb|0", TeamNum: 1},
	}}
	s.failed["Epic|aaa|0"] = time.Now()

	s.Observe(st)
	if len(s.want) != 1 || s.want[0] != "Epic|bbb|0" {
		t.Errorf("queued %v, want only Epic|bbb|0", s.want)
	}

	s.failed["Epic|aaa|0"] = time.Now().Add(-retryAfter - time.Second)
	s.Observe(st)
	if len(s.want) != 2 {
		t.Errorf("queued %v after the retry window, want both", s.want)
	}
}

// Selecting a playlist reports that one regardless of what is being played -
// the point being to sit in a 3v3 and look at everyone's 2v2.
func TestSelectedPlaylistOverridesTheMatch(t *testing.T) {
	s := newIdle()
	st := state(3, 3) // a 3v3, which follows to playlist 13

	if got, _ := s.PlaylistFor(st); got != 13 {
		t.Fatalf("PlaylistFor() = %d, want 13 by default", got)
	}

	s.SetPlaylist(11)
	if got, ok := s.PlaylistFor(st); !ok || got != 11 {
		t.Errorf("PlaylistFor() = %d, %v; want 11, true", got, ok)
	}
	if s.Playlist() != 11 {
		t.Errorf("Playlist() = %d, want 11", s.Playlist())
	}

	s.SetPlaylist(FollowMatch)
	if got, _ := s.PlaylistFor(st); got != 13 {
		t.Errorf("PlaylistFor() = %d, want 13 after following again", got)
	}
}

// An explicit playlist is answerable even for a roster size that has no ranked
// playlist to follow, which is the case while a lobby is still filling.
func TestSelectedPlaylistWorksWithNoFollowablePlaylist(t *testing.T) {
	s := newIdle()
	empty := state()

	if _, ok := s.PlaylistFor(empty); ok {
		t.Error("PlaylistFor() resolved with no roster while following the match")
	}
	s.SetPlaylist(11)
	if got, ok := s.PlaylistFor(empty); !ok || got != 11 {
		t.Errorf("PlaylistFor() = %d, %v; want 11, true", got, ok)
	}
}

// One lookup fills every playlist, so a player read while playing 3v3 answers
// for 2v2 too - without another query.
func TestOnePlayerAnswersForEveryPlaylist(t *testing.T) {
	s := newIdle()
	st := state(3, 3)
	st.Players[0].PrimaryId = "Epic|aaa|0"

	s.known["Epic|aaa|0"] = true
	s.byKey[key{"Epic|aaa|0", 13}] = Entry{MMR: 1131, Tier: 16, Ranked: true}
	s.byKey[key{"Epic|aaa|0", 11}] = Entry{MMR: 900, Tier: 13, Ranked: true}

	if e, ok := s.For(st, "Epic|aaa|0"); !ok || e.MMR != 1131 {
		t.Errorf("3v3: For() = %+v, %v; want MMR 1131", e, ok)
	}
	s.SetPlaylist(11)
	if e, ok := s.For(st, "Epic|aaa|0"); !ok || e.MMR != 900 {
		t.Errorf("2v2: For() = %+v, %v; want MMR 900", e, ok)
	}
}

// A player who has been read but has no rating in the selected playlist is
// unranked there, which is an answer - and must not read as "still loading".
func TestKnownPlayerUnrankedInPlaylist(t *testing.T) {
	s := newIdle()
	st := state(3, 3)
	st.Players[0].PrimaryId = "Epic|aaa|0"
	s.known["Epic|aaa|0"] = true

	e, ok := s.For(st, "Epic|aaa|0")
	if !ok {
		t.Fatal("For() reported the player as not looked up")
	}
	if e.Ranked {
		t.Errorf("For() = %+v; want unranked", e)
	}
}

func TestUnknownPlayerIsNotAnAnswer(t *testing.T) {
	s := newIdle()
	st := state(3, 3)
	st.Players[0].PrimaryId = "Epic|aaa|0"

	if _, ok := s.For(st, "Epic|aaa|0"); ok {
		t.Error("For() answered for a player never looked up")
	}
}

// Every menu entry must be a playlist the code can actually report, and
// "Current mode" must be first since it is the default.
func TestPlaylistsMenu(t *testing.T) {
	list := Playlists()
	if len(list) < 2 {
		t.Fatal("menu is empty")
	}
	if list[0].ID != FollowMatch {
		t.Errorf("first entry is %+v, want FollowMatch", list[0])
	}
	seen := map[int]bool{}
	for _, p := range list {
		if p.Name == "" {
			t.Errorf("playlist %d has no name", p.ID)
		}
		if seen[p.ID] {
			t.Errorf("playlist %d listed twice", p.ID)
		}
		seen[p.ID] = true
	}
	for _, want := range []int{10, 11, 13} {
		if !seen[want] {
			t.Errorf("menu is missing playlist %d", want)
		}
	}
}
