// Package liverank reads your real rank from Rocket League's own backend,
// as an alternative to the ranks written by hand in config.yaml.
//
// It implements presence.RankSource, so the Discord card does not know or care
// which source is in use.
//
// Nothing here touches the game process, so it is unaffected by anti-cheat: it
// authenticates as your Epic account and talks to PsyNet over HTTPS, the same
// way the game does. Setting it up is a one-time sign-in; see the rlmmr
// README.
package liverank

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/ShakedShitrit/rlmmr"
	"github.com/dank/rlapi"
)

// ErrNotLinked means no Epic credentials are stored, so the live rank cannot
// be read until the user completes the one-time sign-in.
var ErrNotLinked = errors.New("no Epic credentials stored; run 'rlmmr link' once")

// modePlaylists maps the gamemode the Stats API implies to a PsyNet playlist.
//
// Only the ranked playlists are listed, because a hand-written rank meant the
// ranked one too and the card should not change meaning with the source.
//
// This is inherently approximate. The Stats API reports no playlist, only how
// many players are on a team, so a 2v2 could be Doubles or Hoops and a 3v3
// could be Standard, Rumble, Dropshot or Snow Day. The main ranked playlist
// is the useful guess. The hand-written ranks have exactly the same blind
// spot.
var modePlaylists = map[string]int{
	"1v1": 10, // Ranked Duel
	"2v2": 11, // Ranked Doubles
	"3v3": 13, // Ranked Standard
}

// refreshTimeout bounds a single read so a stalled connection cannot wedge
// the refresh goroutine forever.
const refreshTimeout = 30 * time.Second

// settleDelay is how long to wait after a match before reading the rating.
// PsyNet does not always have the new value the instant the game says the
// match ended, and a read that lands too early shows the old number until the
// next match finishes.
const settleDelay = 5 * time.Second

// Source serves live ranks and refreshes them when a match ends.
type Source struct {
	provider *rlmmr.LiveProvider

	// refresh is a depth-1 queue rather than a mutex-guarded flag: MatchEnded
	// is called from the match-state path and must never block, and a second
	// trigger while one is in flight can be dropped safely because the
	// refresh already pending will read the same value.
	refresh chan struct{}
	stop    chan struct{}
	done    chan struct{}

	mu       sync.RWMutex
	lastWarn time.Time
}

// New reads the current ranks over an existing PsyNet session.
//
// selfID names whose rank to read, as "Epic|<hex>|0". Empty means the
// signed-in account, which is only right when that account is yours. When the
// session belongs to a separate query account - the arrangement that stops
// this from evicting the running game - selfID is what still makes the card
// show your rank rather than the query account's.
//
// It returns ErrNotLinked when the user has not signed in, which the caller
// should treat as "not set up yet" and fall back from, rather than as a
// failure worth aborting over.
func New(client *rlmmr.Client, selfID string) (*Source, error) {
	provider, err := rlmmr.NewLive(rlmmr.LiveOptions{
		Client:   client,
		PlayerID: rlapi.PlayerID(strings.TrimSpace(selfID)),
	})
	if err != nil {
		if errors.Is(err, rlmmr.ErrNoCredentials) || errors.Is(err, rlmmr.ErrCredentialsRejected) {
			return nil, fmt.Errorf("%w: %v", ErrNotLinked, err)
		}
		return nil, err
	}

	s := &Source{
		provider: provider,
		refresh:  make(chan struct{}, 1),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	go s.run()

	for _, rank := range provider.All() {
		if rank.Ranked() {
			zap.L().Debug("liverank: rank read",
				zap.String("playlist", rank.PlaylistName),
				zap.String("rank", rank.Name),
				zap.Int("mmr", rank.MMR))
		}
	}
	return s, nil
}

// TierFor returns the live rank for a gamemode.
func (s *Source) TierFor(mode string) (int, string, bool) {
	playlist, ok := modePlaylists[normalise(mode)]
	if !ok {
		return 0, "", false
	}

	rank, ranked, err := s.provider.Rank(context.Background(), playlist)
	if err != nil {
		s.warnOccasionally("liverank: reading rank", err)
		return 0, "", false
	}
	if !ranked {
		return 0, "", false
	}
	// The MMR is the reason to prefer this over a configured rank, so put it
	// in the tooltip: "Champion I div2 - 1131".
	return rank.Tier, fmt.Sprintf("%s - %d", rank.Name, rank.MMR), true
}

// SetPlayer points the source at a different player and re-reads their rank.
//
// Called when the local player is worked out from the game, which for a
// separate query account cannot happen until a match has been watched. It runs
// the read on its own goroutine so it never blocks the match-state path.
func (s *Source) SetPlayer(id string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), refreshTimeout)
		defer cancel()
		if err := s.provider.SetPlayerID(ctx, rlapi.PlayerID(strings.TrimSpace(id))); err != nil {
			s.warnOccasionally("liverank: reading the rank of the detected player", err)
			return
		}
		zap.L().Info("liverank: now showing the rank of the player detected in game",
			zap.String("player", id))
	}()
}

// MatchEnded queues a refresh. It never blocks.
func (s *Source) MatchEnded() {
	select {
	case s.refresh <- struct{}{}:
	default:
		// A refresh is already queued; it will pick up the same value.
	}
}

// run performs refreshes off the caller's goroutine.
func (s *Source) run() {
	defer close(s.done)
	for {
		select {
		case <-s.stop:
			return
		case <-s.refresh:
			// Let the rating settle before reading it.
			select {
			case <-s.stop:
				return
			case <-time.After(settleDelay):
			}

			ctx, cancel := context.WithTimeout(context.Background(), refreshTimeout)
			err := s.provider.Refresh(ctx, true)
			cancel()
			if err != nil {
				s.warnOccasionally("liverank: refreshing after match", err)
				continue
			}
			zap.L().Debug("liverank: refreshed after match")
		}
	}
}

// warnOccasionally logs at most once a minute. A backend that is down would
// otherwise fill the log with the same line on every card update.
func (s *Source) warnOccasionally(msg string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Since(s.lastWarn) < time.Minute {
		return
	}
	s.lastWarn = time.Now()
	zap.L().Warn(msg, zap.Error(err))
}

// Close stops refreshing and releases the PsyNet connection.
func (s *Source) Close() error {
	select {
	case <-s.stop:
		return nil // already closed
	default:
	}
	close(s.stop)
	<-s.done
	return s.provider.Close()
}

func normalise(mode string) string {
	out := make([]rune, 0, len(mode))
	for _, r := range mode {
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		if r != ' ' && r != '\t' {
			out = append(out, r)
		}
	}
	return string(out)
}
