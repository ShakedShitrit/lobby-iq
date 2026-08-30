// Package lobby reads every player's rating at the start of a match.
//
// The Stats API already reports the roster, and reports it with each player's
// PrimaryId - "Epic|<hex>|0", which is exactly the identifier Rocket League's
// backend takes. So the lobby can be looked up without touching the game
// process: take the roster the game already told us about, and ask the backend
// what those players are rated.
//
// Skill is public data there. Reading someone else's rating needs no
// relationship to them and no permission from them, which is what makes this
// possible from a session signed in as any account at all - including one that
// is not the account playing. That distinction is the whole point; see
// config.RLMMRSelfID.
//
// One query returns a player's rating in every playlist, so all of them are
// kept. Showing a different playlist than the one being played is then a
// change of mind rather than a round trip.
package lobby

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/ShakedShitrit/lobby-iq/internal/rlstats"
	"github.com/ShakedShitrit/rlmmr"
	"github.com/dank/rlapi"
)

// lookupTimeout bounds one player's query. Six of these run back to back at
// kickoff, so it is short enough that a stalled backend cannot leave the whole
// roster blank for a noticeable part of the match.
const lookupTimeout = 15 * time.Second

// retryAfter is how long a failed lookup is left alone before being tried
// again. Without it a player the backend will not answer for is re-queried on
// every roster tick for the rest of the match.
const retryAfter = 2 * time.Minute

// FollowMatch is the playlist selection meaning "whatever is being played",
// which is inferred from the roster's team size.
const FollowMatch = 0

// Playlist is a playlist the UI can show ratings for.
type Playlist struct {
	// ID is PsyNet's playlist number, or FollowMatch.
	ID int
	// Name is what to put in a menu.
	Name string
}

// Playlists lists the selectable playlists in menu order.
//
// Only the ranked ones are here: casual playlists carry no rating worth
// showing, so offering them would be offering a column of blanks.
func Playlists() []Playlist {
	return []Playlist{
		{FollowMatch, "Current mode"},
		{10, "Duel (1v1)"},
		{11, "Doubles (2v2)"},
		{13, "Standard (3v3)"},
		{12, "Solo Standard"},
		{27, "Hoops"},
		{28, "Rumble"},
		{29, "Dropshot"},
		{30, "Snow Day"},
		{34, "Tournaments"},
	}
}

// Entry is what is known about one player's rating in one playlist.
type Entry struct {
	// MMR and Rank describe the rating. Ranked is false when the player has no
	// rating in this playlist, which is not an error - most people are
	// unranked in most playlists.
	MMR    int
	Rank   string
	Ranked bool

	// Tier is Rocket League's own tier number, 0 unranked to 22 Supersonic
	// Legend. It is what selects the badge art, and is kept alongside the
	// rendered name so a UI can show either without parsing the other.
	Tier int

	// Fetched is when this was read.
	Fetched time.Time
}

// Source looks up lobby ratings and caches them.
//
// Ratings are cached for the lifetime of the process. Facing the same opponent
// twice in an evening is common, and a rating does not move enough between two
// matches to be worth re-reading.
type Source struct {
	client *rlmmr.Client

	// queue is depth-1: Observe is called from the match-state path on every
	// tick and must never block. A roster that changes while a lookup is in
	// flight is picked up by the next pass, which re-reads current state
	// rather than whatever was queued.
	queue chan struct{}
	stop  chan struct{}
	done  chan struct{}

	mu sync.RWMutex
	// byKey holds a rating per player and playlist. One lookup fills every
	// playlist that player has played.
	byKey map[key]Entry
	// known marks players whose lookup has completed. It is separate from
	// byKey because a player with no entry for a playlist is unranked there,
	// which is an answer - and is only distinguishable from "not looked up
	// yet" by having been recorded here.
	known map[rlapi.PlayerID]bool
	// failed records when a lookup last failed, so retries are spaced out.
	failed map[rlapi.PlayerID]time.Time
	// want is the roster to look up, replaced wholesale on each tick.
	want []rlapi.PlayerID
	// selected is the playlist to report, or FollowMatch to infer it.
	selected int

	// onUpdate is called after a lookup pass changes what For would answer.
	// A UI redraws on match state, which arrives on its own schedule and not
	// when a lookup lands, so without this a rating can sit unshown until the
	// next tick happens to come along.
	onUpdate func()
}

type key struct {
	id       rlapi.PlayerID
	playlist int
}

// New starts a Source on an existing backend session.
//
// The client is shared rather than owned: a second session on the same account
// would evict the first, so everything that talks to the backend has to go
// through one. Close does not close it.
func New(client *rlmmr.Client) *Source {
	s := &Source{
		client: client,
		queue:  make(chan struct{}, 1),
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
		byKey:  map[key]Entry{},
		known:  map[rlapi.PlayerID]bool{},
		failed: map[rlapi.PlayerID]time.Time{},
	}
	go s.run()
	return s
}

// OnUpdate registers a callback fired after ratings change. It is called from
// the lookup goroutine, so a UI callback must marshal to its own thread.
func (s *Source) OnUpdate(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onUpdate = fn
}

// SetPlaylist chooses which playlist For reports. FollowMatch infers it from
// the match being played.
//
// This never queries anything: a lookup already fetched every playlist, so
// switching only changes which cached rating is read.
func (s *Source) SetPlaylist(playlist int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.selected = playlist
}

// Playlist returns the current selection.
func (s *Source) Playlist() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.selected
}

// PlaylistFor returns the playlist For will report for a match, resolving
// FollowMatch against the roster. ok is false when following the match and the
// roster is not a size with a ranked playlist.
func (s *Source) PlaylistFor(state rlstats.MatchState) (int, bool) {
	s.mu.RLock()
	selected := s.selected
	s.mu.RUnlock()

	if selected != FollowMatch {
		return selected, true
	}
	return playlistFor(state)
}

// Observe takes a match state and queues lookups for anyone not yet known.
// It never blocks, so it is safe to call on every tick.
//
// Queuing does not depend on the selected playlist, because one lookup covers
// them all.
func (s *Source) Observe(state rlstats.MatchState) {
	var missing []rlapi.PlayerID
	s.mu.Lock()
	now := time.Now()
	for _, p := range state.Players {
		id := rlapi.PlayerID(p.PrimaryId)
		if id == "" || s.known[id] {
			continue
		}
		if last, bad := s.failed[id]; bad && now.Sub(last) < retryAfter {
			continue
		}
		missing = append(missing, id)
	}
	if len(missing) == 0 {
		s.mu.Unlock()
		return
	}
	s.want = missing
	s.mu.Unlock()

	select {
	case s.queue <- struct{}{}:
	default:
		// A pass is already pending and will read s.want as it stands then,
		// which is fresher than what this call would have enqueued.
	}
}

// For returns what is known about one player in the selected playlist.
//
// ok is false while the lookup is outstanding or if the player could not be
// read, so a caller should render a blank rather than a zero. A player who has
// been read but has no rating in this playlist comes back ok with Ranked
// false, which is a different thing and worth showing differently.
func (s *Source) For(state rlstats.MatchState, primaryID string) (Entry, bool) {
	playlist, ok := s.PlaylistFor(state)
	if !ok {
		return Entry{}, false
	}

	id := rlapi.PlayerID(primaryID)
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.known[id] {
		return Entry{}, false
	}
	return s.byKey[key{id, playlist}], true
}

// run performs lookups off the caller's goroutine.
func (s *Source) run() {
	defer close(s.done)
	for {
		select {
		case <-s.stop:
			return
		case <-s.queue:
			s.mu.RLock()
			ids := s.want
			s.mu.RUnlock()
			if len(ids) == 0 {
				continue
			}
			s.lookup(ids)
		}
	}
}

func (s *Source) lookup(ids []rlapi.PlayerID) {
	ctx, cancel := context.WithTimeout(context.Background(), lookupTimeout*time.Duration(len(ids)))
	defer cancel()

	found, fails := s.client.SkillsFor(ctx, ids)

	now := time.Now()
	s.mu.Lock()

	for id, skills := range found {
		// Every playlist is stored, not just the one being played, because the
		// response carried them all and re-asking later would be a round trip
		// for data already in hand.
		for _, sk := range skills {
			s.byKey[key{id, sk.Playlist}] = Entry{
				MMR:     sk.DisplayMMR(),
				Rank:    sk.RankName(),
				Tier:    sk.Tier,
				Ranked:  sk.Tier > 0,
				Fetched: now,
			}
		}
		s.known[id] = true
		delete(s.failed, id)
	}

	for id, err := range fails {
		s.failed[id] = now
		zap.L().Debug("lobby: could not read rating",
			zap.String("player", string(id)), zap.Error(err))
	}

	fn := s.onUpdate
	changed := len(found) > 0
	s.mu.Unlock()

	// Outside the lock: the callback redraws a UI, which must not be able to
	// block the next lookup pass behind it.
	if changed && fn != nil {
		fn()
	}
}

// Close stops the lookup goroutine. The shared backend session is left open.
func (s *Source) Close() error {
	select {
	case <-s.stop:
		return nil // already closed
	default:
	}
	close(s.stop)
	<-s.done
	return nil
}

// playlistFor guesses the ranked playlist from the roster, the same way
// liverank does and with the same blind spot: the Stats API reports team sizes
// and no playlist, so a 3v3 could be Standard, Rumble, Dropshot or Snow Day.
// The main ranked playlist for the size is the useful guess - and picking the
// playlist by hand is exactly what SetPlaylist is for when the guess is wrong.
func playlistFor(state rlstats.MatchState) (int, bool) {
	counts := map[int]int{}
	for _, p := range state.Players {
		counts[p.TeamNum]++
	}
	largest := 0
	for _, n := range counts {
		if n > largest {
			largest = n
		}
	}
	return rankedPlaylistBySize(largest)
}

func rankedPlaylistBySize(size int) (int, bool) {
	switch size {
	case 1:
		return 10, true // Ranked Duel
	case 2:
		return 11, true // Ranked Doubles
	case 3:
		return 13, true // Ranked Standard
	}
	return 0, false
}
