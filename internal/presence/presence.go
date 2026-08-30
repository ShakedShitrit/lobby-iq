// Package presence turns the live match state into a Discord Rich Presence
// activity - the "Playing Rocket League" card on your Discord profile, with
// the score, your stat line, and this session's win/loss tally under it.
package presence

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/ShakedShitrit/lobby-iq/internal/discord"
	"github.com/ShakedShitrit/lobby-iq/internal/rlstats"
	"github.com/ShakedShitrit/lobby-iq/internal/session"
)

// Assets names the images the card shows. Each value is either the key of an
// art asset uploaded to the Discord application under Rich Presence > Art
// Assets, or an https:// URL that Discord fetches and proxies itself. A value
// naming nothing that exists is simply not rendered, so a half-configured
// application still shows its text. See DISCORD.md.
type Assets struct {
	Logo   string
	Blue   string
	Orange string
}

// DefaultAssets are the uploaded-asset keys DISCORD.md tells you to use.
func DefaultAssets() Assets {
	return Assets{Logo: "rocket_league", Blue: "team_blue", Orange: "team_orange"}
}

const (
	// updateInterval throttles publishes: Discord rate-limits SET_ACTIVITY
	// to 5 updates per 20 seconds and silently drops the excess.
	updateInterval = 5 * time.Second

	// tickInterval is how often the worker checks whether the activity has
	// changed. Match state arrives many times per second, far too fast to
	// publish directly, so Observe only records it and this does the work.
	tickInterval = time.Second

	// staleAfter is how long the last match update stays believable. Rocket
	// League emits UpdateState continuously during a match and nothing at
	// all in the menus, so silence this long means the match is over.
	staleAfter = 30 * time.Second

	// clockDriftTolerance is how far the game clock may diverge from our
	// anchor before it's re-derived. Without some tolerance the anchor would
	// shift by a second constantly, changing the payload and defeating the
	// unchanged-activity check below.
	clockDriftTolerance = 2 * time.Second
)

// Presence publishes match state to Discord. The zero value is not usable;
// call New.
type Presence struct {
	client *discord.Client
	sess   *session.Tracker
	assets Assets
	// ranks supplies the rank badge, from config or from the live backend.
	// Never nil - New substitutes a source that reports no rank.
	ranks    RankSource
	launched time.Time

	mu         sync.Mutex
	state      rlstats.MatchState
	observedAt time.Time

	// Per-match bookkeeping, reset when the MatchGuid changes.
	guid string
	// teamSize is the largest roster seen on a single team, so a leaver
	// mid-game doesn't turn a 3v3 into a 2v2.
	teamSize int
	// targetTicks counts how often each player name was the one the game
	// followed; the most-followed name is taken as yours.
	targetTicks map[string]int
	// startAnchor is when the current match began, derived from the game
	// clock so Discord's elapsed timer matches the one in-game.
	startAnchor time.Time
	// endedGuid is the match already reported finished, so a live rank source
	// is nudged once per match rather than on every tick after the whistle.
	endedGuid string

	lastPayload string
	lastSentAt  time.Time

	stop chan struct{}
	done chan struct{}
}

// Options configures a Presence.
type Options struct {
	// ClientID is the Discord application's client ID.
	ClientID string
	// Assets names the card's images; empty fields fall back to
	// DefaultAssets.
	Assets Assets
	// Ranks supplies the rank badge shown on the card. Use NewConfigRanks for
	// ranks written by hand, or a live source that reads them from the game's
	// backend. Nil means no rank badge, leaving the logo in place.
	//
	// Presence does not close it: whoever built it owns its lifetime, and a
	// live source holds a network connection that may outlive the card.
	Ranks RankSource
	// Session supplies the win/loss tally and may be nil.
	Session *session.Tracker
}

// New starts publishing to Discord in the background.
func New(opts Options) *Presence {
	assets := opts.Assets
	defaults := DefaultAssets()
	if assets.Logo == "" {
		assets.Logo = defaults.Logo
	}
	if assets.Blue == "" {
		assets.Blue = defaults.Blue
	}
	if assets.Orange == "" {
		assets.Orange = defaults.Orange
	}

	ranks := opts.Ranks
	if ranks == nil {
		ranks = noRanks{}
	}

	p := &Presence{
		client:      discord.New(opts.ClientID),
		sess:        opts.Session,
		assets:      assets,
		ranks:       ranks,
		launched:    time.Now(),
		targetTicks: map[string]int{},
		stop:        make(chan struct{}),
		done:        make(chan struct{}),
	}
	go p.run()
	return p
}

// noRanks is the stand-in when no rank source was supplied, so the rest of
// the code never has to check for nil.
type noRanks struct{}

func (noRanks) TierFor(string) (int, string, bool) { return 0, "", false }
func (noRanks) MatchEnded()                        {}
func (noRanks) Close() error                       { return nil }

// Observe records the latest match state. It's cheap enough to call on every
// tick of the Stats API and never blocks. A nil *Presence - what New's
// callers hold when Rich Presence is switched off - ignores the call.
func (p *Presence) Observe(s rlstats.MatchState) {
	if p == nil {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	p.state = s
	p.observedAt = now

	if s.MatchGuid != p.guid {
		p.guid = s.MatchGuid
		p.teamSize = 0
		p.targetTicks = map[string]int{}
		p.startAnchor = time.Time{}
	}

	// The end of a ranked match is the only moment a rating can change, which
	// makes it a far better trigger than a timer - it fires exactly when the
	// value moved and never otherwise. Once per match: the game keeps sending
	// state after the whistle.
	if s.Ended && s.MatchGuid != "" && s.MatchGuid != p.endedGuid {
		p.endedGuid = s.MatchGuid
		p.ranks.MatchEnded()
	}

	if n := largestTeam(s.Players); n > p.teamSize {
		p.teamSize = n
	}
	// Replay ticks follow whoever scored, who may be an opponent, so they're
	// not evidence of which player is you.
	if name := strings.TrimSpace(s.TargetName); name != "" && !s.Replay {
		p.targetTicks[name]++
	}

	if s.TimeSeconds > 0 {
		anchor := now.Add(-time.Duration(s.TimeSeconds) * time.Second)
		if p.startAnchor.IsZero() || anchor.Sub(p.startAnchor).Abs() > clockDriftTolerance {
			p.startAnchor = anchor
		}
	}
}

// Close clears the presence and stops publishing. Safe on a nil *Presence.
func (p *Presence) Close() error {
	if p == nil {
		return nil
	}

	select {
	case <-p.stop:
		return nil // already closed
	default:
	}
	close(p.stop)
	<-p.done
	return p.client.Close()
}

func (p *Presence) run() {
	defer close(p.done)

	// Publish once up front so the menus card appears at launch rather than
	// a tick later.
	if !p.publish() {
		return
	}

	t := time.NewTicker(tickInterval)
	defer t.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-t.C:
			if !p.publish() {
				return
			}
		}
	}
}

// publish sends the current activity, skipping the send if nothing changed or
// if the last one was too recent. It reports whether publishing should carry
// on: a false means the Discord client is permanently done and the worker
// should stop rather than fail on a timer forever.
func (p *Presence) publish() bool {
	p.mu.Lock()

	activity := p.buildLocked()
	payload, err := json.Marshal(activity)
	if err != nil {
		p.mu.Unlock()
		zap.L().Warn("presence: marshaling activity", zap.Error(err))
		return true
	}

	if string(payload) == p.lastPayload || time.Since(p.lastSentAt) < updateInterval {
		p.mu.Unlock()
		return true
	}
	p.lastPayload = string(payload)
	p.lastSentAt = time.Now()
	p.mu.Unlock()

	err = p.client.SetActivity(activity)
	if err == nil {
		return true
	}

	// The activity never landed, so forget it: otherwise an unchanged
	// activity would never be retried once Discord comes back. The client's
	// own backoff keeps this from becoming a hot loop.
	p.mu.Lock()
	p.lastPayload = ""
	p.mu.Unlock()

	// A rejected client ID or a closed client won't recover - the client has
	// already logged why, so just stand down quietly.
	if errors.Is(err, discord.ErrInvalidClientID) || errors.Is(err, discord.ErrClosed) {
		return false
	}
	if !errors.Is(err, discord.ErrUnavailable) {
		zap.L().Warn("presence: setting activity", zap.Error(err))
	}
	return true
}

// buildLocked renders the current state as an activity.
func (p *Presence) buildLocked() discord.Activity {
	s := p.state
	inMatch := s.MatchGuid != "" && len(s.Players) > 0 &&
		!p.observedAt.IsZero() && time.Since(p.observedAt) < staleAfter
	if !inMatch {
		return discord.Activity{
			Details:    "In the menus",
			State:      p.sessionText(true),
			Timestamps: &discord.Timestamps{Start: p.launched.Unix()},
			Assets:     &discord.Assets{LargeImage: p.assets.Logo, LargeText: "Rocket League"},
		}
	}

	you, haveYou := p.youLocked()
	myTeam := s.TargetTeamNum
	if haveYou {
		myTeam = you.TeamNum
	}

	assets := &discord.Assets{
		LargeImage: p.assets.Logo,
		LargeText:  arenaText(s.Arena),
		SmallImage: p.teamAsset(s.Teams[myTeam]),
		SmallText:  teamText(s.Teams[myTeam], myTeam),
	}

	// A known rank for this gamemode takes over the large icon, since a rank
	// badge says more than the logo does. The arena moves into the tooltip
	// alongside it rather than being lost.
	if tier, label, ok := p.ranks.TierFor(modeName(p.teamSize)); ok {
		assets.LargeImage = rankIcon(tier)
		assets.LargeText = label + " · " + arenaText(s.Arena)
	}

	activity := discord.Activity{
		Details: p.detailsLocked(myTeam),
		State:   p.stateText(you, haveYou),
		Assets:  assets,
	}

	if !p.startAnchor.IsZero() {
		activity.Timestamps = &discord.Timestamps{Start: p.startAnchor.Unix()}
	}
	if p.teamSize > 0 {
		teams := len(s.Teams)
		if teams == 0 {
			teams = 2
		}
		activity.Party = &discord.Party{
			ID:   s.MatchGuid,
			Size: []int{len(s.Players), p.teamSize * teams},
		}
	}
	if haveYou {
		activity.Buttons = []discord.Button{{Label: "View on Tracker", URL: you.TrackerURL()}}
	}
	return activity
}

// detailsLocked is the activity's first line: the gamemode and the score from
// your team's point of view, e.g. "2v2 · Blue 3 - 1 Orange".
func (p *Presence) detailsLocked(myTeam int) string {
	s := p.state
	mine, theirs := s.Scores[myTeam], 0
	theirName := ""
	for num, score := range s.Scores {
		if num == myTeam {
			continue
		}
		if score >= theirs {
			theirs, theirName = score, s.Teams[num]
		}
	}

	mode := modeName(p.teamSize)

	// A reported winner is the match's final word, and outlasts the last
	// score tick either way.
	if s.Ended || s.Winner != "" {
		switch {
		case mine > theirs:
			return fmt.Sprintf("%s · Won %d - %d", mode, mine, theirs)
		case mine < theirs:
			return fmt.Sprintf("%s · Lost %d - %d", mode, mine, theirs)
		default:
			return fmt.Sprintf("%s · Drew %d - %d", mode, mine, theirs)
		}
	}

	myName := teamText(s.Teams[myTeam], myTeam)
	if theirName == "" {
		theirName = "Opponents"
	}

	line := fmt.Sprintf("%s · %s %d - %d %s", mode, myName, mine, theirs, theirName)
	if s.Overtime {
		line = fmt.Sprintf("%s · OT · %s %d - %d %s", mode, myName, mine, theirs, theirName)
	}
	return line
}

// stateText is the activity's second line: your stat line for this match plus
// the running session tally.
func (p *Presence) stateText(you rlstats.Player, haveYou bool) string {
	tally := p.sessionText(false)
	if !haveYou {
		return tally
	}

	stats := fmt.Sprintf("%d pts · %dG %dA %dS", you.Score, you.Goals, you.Assists, you.Saves)
	if you.Shots > 0 {
		stats += fmt.Sprintf(" %dSh", you.Shots)
	}
	if you.Demos > 0 {
		stats += fmt.Sprintf(" %dD", you.Demos)
	}
	if tally == "" {
		return stats
	}
	return stats + " · " + tally
}

// sessionText renders this run's win/loss tally. verbose spells out the
// record for the roomier menus card.
func (p *Presence) sessionText(verbose bool) string {
	if p.sess == nil {
		return ""
	}
	total := p.sess.Total()
	if total.Wins+total.Losses == 0 {
		if verbose {
			return "No finished matches yet this session"
		}
		return ""
	}
	if verbose {
		return fmt.Sprintf("Session %s · %dW %dL", total, total.Wins, total.Losses)
	}
	return fmt.Sprintf("Session %s", total)
}

// youLocked picks out the local player: the one the game followed for the
// most ticks this match, matched back to the roster by name.
func (p *Presence) youLocked() (rlstats.Player, bool) {
	best, bestTicks := "", 0
	for name, ticks := range p.targetTicks {
		if ticks > bestTicks {
			best, bestTicks = name, ticks
		}
	}
	if best == "" {
		return rlstats.Player{}, false
	}
	for _, candidate := range p.state.Players {
		if strings.TrimSpace(candidate.Name) == best {
			return candidate, true
		}
	}
	return rlstats.Player{}, false
}

func arenaText(arena string) string {
	if pretty := rlstats.PrettyArena(arena); pretty != "" {
		return pretty
	}
	return "Rocket League"
}

// teamText names a team, falling back to its number when the game hasn't
// reported a name yet.
func teamText(name string, teamNum int) string {
	if name = strings.TrimSpace(name); name != "" {
		return name
	}
	if teamNum < 0 {
		return "Spectating"
	}
	return fmt.Sprintf("Team %d", teamNum)
}

func (p *Presence) teamAsset(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "blue":
		return p.assets.Blue
	case "orange":
		return p.assets.Orange
	}
	return ""
}

// modeName labels a gamemode by its per-team roster size. The Stats API
// exposes no playlist, so ranked and casual of the same size look identical.
func modeName(teamSize int) string {
	if teamSize <= 0 {
		return "Rocket League"
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
