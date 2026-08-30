// Package session keeps a running win/loss tally per gamemode for the
// current run of LobbyIQ. It lives entirely in memory: quitting the app
// clears the session, which is the point - it answers "how am I doing right
// now", not "how am I doing all time" (that's internal/history).
package session

import (
	"fmt"
	"sort"
	"sync"

	"github.com/ShakedShitrit/lobby-iq/internal/rlstats"
)

// Stat is one gamemode's running tally.
type Stat struct {
	// Mode is the gamemode label, e.g. "2v2".
	Mode   string
	Wins   int
	Losses int
}

// Net is wins minus losses: 3 wins then 5 losses is -2.
func (s Stat) Net() int { return s.Wins - s.Losses }

// String renders the net tally with an explicit sign, e.g. "+3" or "-2".
func (s Stat) String() string {
	return fmt.Sprintf("%+d", s.Net())
}

// match is the in-progress match being accumulated, discarded once its
// result is recorded.
type match struct {
	guid string
	// teamSize is the largest roster seen on any single team this match, so
	// a leaver mid-game doesn't turn a 3v3 into a 2v2.
	teamSize int
	// targetTicks counts how many UpdateState ticks followed each team. Goal
	// replays briefly follow the scorer, who may be an opponent, so the team
	// followed *most* is taken as yours rather than the last one seen.
	targetTicks map[int]int
	recorded    bool
}

// Tracker accumulates per-gamemode results. It is safe for concurrent use.
type Tracker struct {
	mu    sync.Mutex
	modes map[string]*Stat
	cur   *match
}

func New() *Tracker {
	return &Tracker{modes: map[string]*Stat{}}
}

// Observe feeds a match state into the tracker, recording a win or loss the
// first time a match is seen to have finished. Safe to call on every tick.
func (t *Tracker) Observe(s rlstats.MatchState) {
	if s.MatchGuid == "" {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.cur == nil || t.cur.guid != s.MatchGuid {
		t.cur = &match{guid: s.MatchGuid, targetTicks: map[int]int{}}
	}
	if t.cur.recorded {
		return
	}

	if n := largestTeam(s.Players); n > t.cur.teamSize {
		t.cur.teamSize = n
	}
	if s.TargetTeamNum >= 0 {
		t.cur.targetTicks[s.TargetTeamNum]++
	}

	// Ended is the authoritative "it's over" signal, but a Winner appearing
	// in UpdateState is accepted too: MatchEnded is missed if the app is
	// started mid-match or the connection drops right at the whistle.
	if !s.Ended && s.Winner == "" {
		return
	}

	winner, ok := winningTeam(s)
	if !ok {
		return
	}
	mine, ok := myTeam(t.cur.targetTicks)
	if !ok {
		return
	}

	t.cur.recorded = true
	stat := t.stat(modeName(t.cur.teamSize))
	if winner == mine {
		stat.Wins++
	} else {
		stat.Losses++
	}
}

// Snapshot returns the per-gamemode tallies, ordered by mode name.
func (t *Tracker) Snapshot() []Stat {
	t.mu.Lock()
	defer t.mu.Unlock()

	out := make([]Stat, 0, len(t.modes))
	for _, s := range t.modes {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Mode < out[j].Mode })
	return out
}

// Total is the combined tally across every gamemode.
func (t *Tracker) Total() Stat {
	t.mu.Lock()
	defer t.mu.Unlock()

	total := Stat{Mode: "TOTAL"}
	for _, s := range t.modes {
		total.Wins += s.Wins
		total.Losses += s.Losses
	}
	return total
}

// Reset clears every tally, starting a fresh session without restarting the
// app.
func (t *Tracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.modes = map[string]*Stat{}
	t.cur = nil
}

func (t *Tracker) stat(mode string) *Stat {
	s, ok := t.modes[mode]
	if !ok {
		s = &Stat{Mode: mode}
		t.modes[mode] = s
	}
	return s
}

// modeName labels a gamemode by its per-team roster size. The Stats API
// exposes no playlist, so ranked and casual of the same size share a tally.
func modeName(teamSize int) string {
	if teamSize <= 0 {
		return "unknown"
	}
	return fmt.Sprintf("%dv%d", teamSize, teamSize)
}

func largestTeam(players []rlstats.Player) int {
	counts := map[int]int{}
	for _, p := range players {
		counts[p.TeamNum]++
	}
	largest := 0
	for _, n := range counts {
		if n > largest {
			largest = n
		}
	}
	return largest
}

// winningTeam resolves the winner to a TeamNum, preferring the reported
// winner name and falling back to the higher score.
func winningTeam(s rlstats.MatchState) (int, bool) {
	if s.Winner != "" {
		for num, name := range s.Teams {
			if name == s.Winner {
				return num, true
			}
		}
	}

	best, bestScore, tied := 0, 0, true
	for num, score := range s.Scores {
		switch {
		case score > bestScore:
			best, bestScore, tied = num, score, false
		case score == bestScore:
			tied = true
		}
	}
	if tied {
		return 0, false
	}
	return best, true
}

// myTeam picks the team followed for the most ticks this match.
func myTeam(targetTicks map[int]int) (int, bool) {
	best, bestTicks := 0, 0
	for num, ticks := range targetTicks {
		if ticks > bestTicks {
			best, bestTicks = num, ticks
		}
	}
	return best, bestTicks > 0
}
