package selfid

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ShakedShitrit/lobby-iq/internal/rlstats"
)

const (
	me  = "Epic|aaa|0"
	you = "Epic|bbb|0"
)

// tick builds a state for a 1v1 where the camera is following target.
func tick(target string, replay bool) rlstats.MatchState {
	return rlstats.MatchState{
		MatchGuid:  "match-1",
		TargetName: target,
		Replay:     replay,
		Players: []rlstats.Player{
			{Name: "me", PrimaryId: me, TeamNum: 0},
			{Name: "you", PrimaryId: you, TeamNum: 1},
		},
	}
}

func newStore(t *testing.T) *Store {
	t.Helper()
	return Open(filepath.Join(t.TempDir(), FileName), "")
}

func feed(s *Store, target string, replay bool, n int) {
	for i := 0; i < n; i++ {
		s.Observe(tick(target, replay))
	}
}

func TestResolvesTheMostFollowedPlayer(t *testing.T) {
	s := newStore(t)
	feed(s, "me", false, minTicks)

	got, ok := s.ID()
	if !ok || got != me {
		t.Fatalf("ID() = %q, %v; want %q, true", got, ok, me)
	}
}

func TestWaitsForEnoughEvidence(t *testing.T) {
	s := newStore(t)
	feed(s, "me", false, minTicks-1)

	if _, ok := s.ID(); ok {
		t.Error("resolved before seeing minTicks ticks")
	}
}

// A goal replay follows the scorer. Those ticks must not count at all, or a
// player who concedes a lot of goals ends up identified as the opponent.
func TestReplayTicksAreIgnored(t *testing.T) {
	s := newStore(t)
	feed(s, "you", true, minTicks*3)

	if id, ok := s.ID(); ok {
		t.Fatalf("resolved to %q from replay ticks alone", id)
	}

	feed(s, "me", false, minTicks)
	if got, _ := s.ID(); got != me {
		t.Errorf("ID() = %q; want %q", got, me)
	}
}

// Spectating, or any match where the camera was split between players, is not
// evidence of who is playing.
func TestAmbiguousFocusDoesNotResolve(t *testing.T) {
	s := newStore(t)
	for i := 0; i < minTicks; i++ {
		s.Observe(tick("me", false))
		s.Observe(tick("you", false))
	}

	if id, ok := s.ID(); ok {
		t.Errorf("resolved to %q on a 50/50 split", id)
	}
}

// Two players under one display name make the name useless as an identity -
// showing the wrong person's rank is worse than showing none.
func TestDuplicateNamesDoNotResolve(t *testing.T) {
	s := newStore(t)
	state := rlstats.MatchState{
		MatchGuid:  "match-1",
		TargetName: "me",
		Players: []rlstats.Player{
			{Name: "me", PrimaryId: me, TeamNum: 0},
			{Name: "me", PrimaryId: you, TeamNum: 1},
		},
	}
	for i := 0; i < minTicks; i++ {
		s.Observe(state)
	}

	if id, ok := s.ID(); ok {
		t.Errorf("resolved to %q despite two players named \"me\"", id)
	}
}

// Evidence is per match: a new match must not inherit the previous one's
// counts, or a short match could be decided by the one before it.
func TestCountsResetBetweenMatches(t *testing.T) {
	s := newStore(t)
	feed(s, "me", false, minTicks-1)

	next := tick("you", false)
	next.MatchGuid = "match-2"
	s.Observe(next)

	if id, ok := s.ID(); ok {
		t.Errorf("resolved to %q across a match boundary", id)
	}
}

func TestResolvedIdentityIsRemembered(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)

	first := Open(path, "")
	feed(first, "me", false, minTicks)
	if _, ok := first.ID(); !ok {
		t.Fatal("did not resolve")
	}

	// A later run must know it without watching another match.
	second := Open(path, "")
	got, ok := second.ID()
	if !ok || got != me {
		t.Errorf("reopened ID() = %q, %v; want %q, true", got, ok, me)
	}
}

// A configured ID is the user stating a fact, and observation must not revise
// it - nor write it to the state file, since it already lives in the config.
func TestConfiguredIDWins(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)

	s := Open(path, you)
	feed(s, "me", false, minTicks*2)

	if got, _ := s.ID(); got != you {
		t.Errorf("ID() = %q; want the configured %q", got, you)
	}
	if fileExists(path) {
		t.Error("wrote a state file for a configured ID")
	}
}

func TestOnResolveFiresOnce(t *testing.T) {
	s := newStore(t)
	calls := 0
	s.OnResolve(func(string) { calls++ })

	feed(s, "me", false, minTicks*3)

	if calls != 1 {
		t.Errorf("OnResolve fired %d times, want 1", calls)
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestForgetMakesItDetectAgain(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)

	s := Open(path, "")
	feed(s, "me", false, minTicks)
	if _, ok := s.ID(); !ok {
		t.Fatal("did not resolve")
	}

	s.Forget()

	if id, ok := s.ID(); ok {
		t.Errorf("still knows %q after Forget", id)
	}
	if fileExists(path) {
		t.Error("Forget left the stored identity on disk")
	}

	// A later run must not recall the forgotten identity either.
	if _, ok := Open(path, "").ID(); ok {
		t.Error("a new store recalled a forgotten identity")
	}

	// And the next match resolves afresh - to whoever is playing now.
	feed(s, "you", false, minTicks)
	if got, ok := s.ID(); !ok || got != you {
		t.Errorf("ID() = %q, %v; want %q, true", got, ok, you)
	}
}

// Forget must not silently discard a configured identity: that value came from
// the user, not from observation.
func TestForgetLeavesAConfiguredIDAlone(t *testing.T) {
	s := Open(filepath.Join(t.TempDir(), FileName), you)
	s.Forget()

	if got, ok := s.ID(); !ok || got != you {
		t.Errorf("ID() = %q, %v; want the configured %q", got, ok, you)
	}
}

// Forgetting when nothing was ever detected is a normal case - the first
// sign-in on a fresh install - and must not error or leave state behind.
func TestForgetWithNothingStored(t *testing.T) {
	s := Open(filepath.Join(t.TempDir(), FileName), "")
	s.Forget()
	if _, ok := s.ID(); ok {
		t.Error("resolved after forgetting an empty store")
	}
}

// The callback exists so a rank source can be retargeted without a restart;
// after a switch of account that has to happen again.
func TestOnResolveFiresAgainAfterForget(t *testing.T) {
	s := newStore(t)
	var got []string
	s.OnResolve(func(id string) { got = append(got, id) })

	feed(s, "me", false, minTicks)
	s.Forget()
	feed(s, "you", false, minTicks)

	if len(got) != 2 || got[0] != me || got[1] != you {
		t.Errorf("OnResolve saw %v; want [%s %s]", got, me, you)
	}
}
