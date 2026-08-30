package presence

import (
	"sync"
	"testing"
)

// countingRanks records how often a match end was reported.
type countingRanks struct {
	mu     sync.Mutex
	ended  int
	closed bool
}

func (c *countingRanks) TierFor(string) (int, string, bool) { return 0, "", false }

func (c *countingRanks) MatchEnded() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ended++
}

func (c *countingRanks) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

func (c *countingRanks) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ended
}

// The game keeps sending state after the final whistle, so the live source
// must be nudged once per match rather than on every tick - otherwise one
// finished match would trigger a burst of backend reads.
func TestMatchEndReportedOncePerMatch(t *testing.T) {
	ranks := &countingRanks{}
	p := newTestPresence(nil)
	p.ranks = ranks

	ended := tick(3, 1)
	ended.Ended = true
	for range 5 {
		p.Observe(ended)
	}
	if got := ranks.count(); got != 1 {
		t.Errorf("MatchEnded called %d times for one match, want 1", got)
	}

	// A second match must be reported in its own right.
	next := tick(1, 0)
	next.MatchGuid = "GUID-2"
	next.Ended = true
	p.Observe(next)
	if got := ranks.count(); got != 2 {
		t.Errorf("MatchEnded called %d times across two matches, want 2", got)
	}
}

// Ticks during play must not trigger a read: the rating cannot have changed
// yet, and a mid-match refresh would just show the pre-match number.
func TestMatchEndNotReportedDuringPlay(t *testing.T) {
	ranks := &countingRanks{}
	p := newTestPresence(nil)
	p.ranks = ranks

	for i := range 10 {
		p.Observe(tick(i, 0))
	}
	if got := ranks.count(); got != 0 {
		t.Errorf("MatchEnded called %d times mid-match, want 0", got)
	}
}

// A match with no GUID is not a match, and must not be reported as ending.
func TestMatchEndIgnoresEmptyGuid(t *testing.T) {
	ranks := &countingRanks{}
	p := newTestPresence(nil)
	p.ranks = ranks

	s := tick(1, 0)
	s.MatchGuid = ""
	s.Ended = true
	p.Observe(s)

	if got := ranks.count(); got != 0 {
		t.Errorf("MatchEnded called %d times for a guid-less state, want 0", got)
	}
}

// A source reporting no rank must leave the logo alone rather than rendering
// a bogus tier-0 badge.
func TestNoRankKeepsLogo(t *testing.T) {
	p := newTestPresence(nil)
	p.ranks = &countingRanks{} // always reports "no rank"
	p.Observe(tick(1, 0))

	p.mu.Lock()
	a := p.buildLocked()
	p.mu.Unlock()

	if a.Assets.LargeImage != DefaultAssets().Logo {
		t.Errorf("large image = %q, want the default logo", a.Assets.LargeImage)
	}
}
