package presence

import (
	"strings"
	"testing"
	"time"

	"github.com/ShakedShitrit/lobby-iq/internal/rlstats"
	"github.com/ShakedShitrit/lobby-iq/internal/session"
)

var teamNames = map[int]string{0: "Blue", 1: "Orange"}

// newTestPresence builds a Presence without starting its publishing
// goroutine, so tests exercise the state-to-activity mapping without needing
// a Discord socket.
func newTestPresence(sess *session.Tracker) *Presence {
	return &Presence{
		sess:   sess,
		assets: DefaultAssets(),
		// New substitutes this when no rank source is configured; building a
		// Presence directly has to do the same, or every lookup panics on a
		// nil interface.
		ranks:       noRanks{},
		launched:    time.Now(),
		targetTicks: map[string]int{},
	}
}

// tick builds one in-progress UpdateState of a 2v2 where the camera follows
// "me" on Blue.
func tick(blue, orange int) rlstats.MatchState {
	return rlstats.MatchState{
		MatchGuid:     "GUID-1",
		Arena:         "Labs_4v4_Arena15_Blackout_P",
		Teams:         teamNames,
		Scores:        map[int]int{0: blue, 1: orange},
		TargetTeamNum: 0,
		TargetName:    "me",
		TimeSeconds:   90,
		Players: []rlstats.Player{
			{Name: "me", PrimaryId: "Epic|abc|0", TeamNum: 0, Score: 540, Goals: 2, Assists: 1, Saves: 3, Shots: 4},
			{Name: "mate", PrimaryId: "Epic|def|0", TeamNum: 0},
			{Name: "opp1", PrimaryId: "Epic|ghi|0", TeamNum: 1},
			{Name: "opp2", PrimaryId: "Epic|jkl|0", TeamNum: 1},
		},
	}
}

func build(p *Presence) (details, state string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	a := p.buildLocked()
	return a.Details, a.State
}

func TestIdleActivity(t *testing.T) {
	p := newTestPresence(session.New())

	details, state := build(p)
	if details != "In the menus" {
		t.Errorf("details = %q, want %q", details, "In the menus")
	}
	if !strings.Contains(state, "No finished matches yet") {
		t.Errorf("state = %q, want a no-matches note", state)
	}
}

func TestInMatchActivity(t *testing.T) {
	p := newTestPresence(session.New())
	p.Observe(tick(3, 1))

	details, state := build(p)
	if want := "2v2 · Blue 3 - 1 Orange"; details != want {
		t.Errorf("details = %q, want %q", details, want)
	}
	if want := "540 pts · 2G 1A 3S 4Sh"; state != want {
		t.Errorf("state = %q, want %q", state, want)
	}
}

// Assets are configurable so images can be swapped by URL without uploading
// anything to the Discord application.
func TestConfiguredAssetsAreUsed(t *testing.T) {
	p := newTestPresence(session.New())
	p.assets = Assets{
		Logo: "https://example.test/logo.png",
		Blue: "https://example.test/blue.png",
	}
	p.Observe(tick(0, 0))

	p.mu.Lock()
	a := p.buildLocked()
	p.mu.Unlock()

	if a.Assets.LargeImage != "https://example.test/logo.png" {
		t.Errorf("large image = %q, want the configured URL", a.Assets.LargeImage)
	}
	if a.Assets.SmallImage != "https://example.test/blue.png" {
		t.Errorf("small image = %q, want the configured URL", a.Assets.SmallImage)
	}
}

// An empty field falls back to the documented uploaded-asset key, so a config
// that sets only some of the images still works.
func TestNewFillsMissingAssets(t *testing.T) {
	p := New(Options{ClientID: "", Assets: Assets{Logo: "https://example.test/logo.png"}})
	defer p.Close()

	if p.assets.Logo != "https://example.test/logo.png" {
		t.Errorf("logo = %q, want the configured URL", p.assets.Logo)
	}
	if want := DefaultAssets().Orange; p.assets.Orange != want {
		t.Errorf("orange = %q, want the default %q", p.assets.Orange, want)
	}
}

func TestInMatchAssetsAndParty(t *testing.T) {
	p := newTestPresence(session.New())
	p.Observe(tick(0, 0))

	p.mu.Lock()
	a := p.buildLocked()
	p.mu.Unlock()

	if want := DefaultAssets().Blue; a.Assets.SmallImage != want {
		t.Errorf("small image = %q, want %q", a.Assets.SmallImage, want)
	}
	if a.Assets.LargeText != "Blackout" {
		t.Errorf("large text = %q, want %q", a.Assets.LargeText, "Blackout")
	}
	if a.Party == nil || len(a.Party.Size) != 2 || a.Party.Size[0] != 4 || a.Party.Size[1] != 4 {
		t.Errorf("party = %+v, want size [4 4]", a.Party)
	}
	// The clock is elapsed seconds, so the timer counts up from match start.
	if a.Timestamps == nil || a.Timestamps.End != 0 || a.Timestamps.Start == 0 {
		t.Errorf("timestamps = %+v, want a start and no end", a.Timestamps)
	}
	if len(a.Buttons) != 1 || !strings.Contains(a.Buttons[0].URL, "rocketleague.tracker.network") {
		t.Errorf("buttons = %+v, want a tracker link", a.Buttons)
	}
}

// A goal replay follows whoever scored, so those ticks must not be taken as
// evidence of which player you are.
func TestReplayTicksDoNotChangeWhoYouAre(t *testing.T) {
	p := newTestPresence(session.New())
	p.Observe(tick(0, 1))

	replay := tick(0, 1)
	replay.Replay = true
	replay.TargetName = "opp1"
	replay.TargetTeamNum = 1
	for i := 0; i < 20; i++ {
		p.Observe(replay)
	}

	details, state := build(p)
	if !strings.HasPrefix(details, "2v2 · Blue") {
		t.Errorf("details = %q, want the score from Blue's point of view", details)
	}
	if !strings.HasPrefix(state, "540 pts") {
		t.Errorf("state = %q, want your own stat line", state)
	}
}

func TestFinishedMatchShowsResultAndSession(t *testing.T) {
	sess := session.New()
	p := newTestPresence(sess)

	final := tick(4, 2)
	p.Observe(final)
	sess.Observe(final)

	final.Winner = "Blue"
	final.Ended = true
	p.Observe(final)
	sess.Observe(final)

	details, state := build(p)
	if want := "2v2 · Won 4 - 2"; details != want {
		t.Errorf("details = %q, want %q", details, want)
	}
	if !strings.HasSuffix(state, "Session +1") {
		t.Errorf("state = %q, want it to end with the session tally", state)
	}
}

func TestStaleStateFallsBackToMenus(t *testing.T) {
	p := newTestPresence(session.New())
	p.Observe(tick(1, 0))

	p.mu.Lock()
	p.observedAt = time.Now().Add(-2 * staleAfter)
	p.mu.Unlock()

	details, _ := build(p)
	if details != "In the menus" {
		t.Errorf("details = %q, want the app to fall back to the menus card", details)
	}
}

// The clock anchor must hold still between ticks: a start timestamp that
// drifted every second would make every payload look new and burn through
// Discord's rate limit.
func TestClockAnchorIsStable(t *testing.T) {
	p := newTestPresence(session.New())
	p.Observe(tick(0, 0))

	p.mu.Lock()
	first := p.startAnchor
	p.mu.Unlock()

	p.Observe(tick(0, 0))

	p.mu.Lock()
	second := p.startAnchor
	p.mu.Unlock()

	if !first.Equal(second) {
		t.Errorf("anchor moved from %v to %v within tolerance", first, second)
	}
}

func TestNilPresenceIsSafe(t *testing.T) {
	var p *Presence
	p.Observe(tick(0, 0))
	if err := p.Close(); err != nil {
		t.Errorf("Close on nil = %v, want nil", err)
	}
}
