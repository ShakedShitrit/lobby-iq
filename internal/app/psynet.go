package app

import (
	"errors"

	"go.uber.org/zap"

	"github.com/ShakedShitrit/lobby-iq/internal/config"
	"github.com/ShakedShitrit/lobby-iq/internal/liverank"
	"github.com/ShakedShitrit/lobby-iq/internal/lobby"
	"github.com/ShakedShitrit/lobby-iq/internal/presence"
	"github.com/ShakedShitrit/lobby-iq/internal/rlstats"
	"github.com/ShakedShitrit/lobby-iq/internal/selfid"
	"github.com/ShakedShitrit/rlmmr"
)

// backend holds everything that talks to Rocket League's own servers.
//
// It exists to keep the session count at exactly one. The backend allows a
// single live session per account and evicts the older one when a second
// appears, so a process that opened one session for the rank badge and another
// for lobby ratings would spend the whole match kicking itself off - and, if
// signed in as the account that is playing, kicking the game off too.
//
// Every field is safe to use when nil except client, which is checked before
// anything is built on it.
type backend struct {
	client *rlmmr.Client
	// ranks is the card's rank source. Never nil: it falls back to the
	// hand-written ranks when the live one is unavailable.
	ranks presence.RankSource
	// lobby is nil unless lobby_mmr is on and a session was obtained.
	lobby *lobby.Source
	// self works out which player in the lobby is you. Never nil - it is
	// useful even with no backend session, and its answer is what the rank
	// source is corrected to once known.
	self *selfid.Store
}

// Observe feeds a match state to everything that learns from one. It never
// blocks, so it is safe on the match-state path.
func (b *backend) Observe(s rlstats.MatchState) {
	b.self.Observe(s)
	if b.lobby != nil {
		b.lobby.Observe(s)
	}
}

// startBackend opens at most one backend session and builds whatever the
// config asked for on top of it.
//
// Nothing here is fatal. A sign-in that has lapsed, or a backend that is down,
// costs the rank badge and the lobby ratings - not the app, which is still
// useful without either.
func startBackend(cfg *config.Config) (*backend, func()) {
	b := &backend{
		ranks: presence.NewConfigRanks(cfg.DiscordRanks),
		self:  selfid.Open(selfid.FileName, cfg.RLMMRSelfID),
	}
	noop := func() {}

	if !cfg.PsyNetEnabled() {
		zap.L().Debug("psynet: not enabled; using ranks from config")
		return b, noop
	}

	client, err := rlmmr.New(rlmmr.Options{
		CredentialsPath: cfg.RLMMRCredentials,
		// DevicePrompt stays nil on purpose. LobbyIQ may be running with no
		// console attached, and a background app must never stall waiting for
		// someone to approve a browser sign-in.
	})
	if err != nil {
		logBackendFailure(err)
		return b, noop
	}
	b.client = client

	if cfg.LiveRankEnabled() {
		// Whoever is known now: the configured ID, or one detected in an
		// earlier run. Empty falls back to the signed-in account, which is
		// right for a single-account setup and is corrected below for a
		// two-account one as soon as a match identifies the player.
		known, _ := b.self.ID()
		live, err := liverank.New(client, known)
		if err != nil {
			zap.L().Warn("presence: live rank unavailable, falling back to discord_ranks",
				zap.Error(err))
		} else {
			zap.L().Info("presence: using live rank from Rocket League's backend")
			b.ranks = live
			b.self.OnResolve(live.SetPlayer)
			if known == "" {
				zap.L().Info("psynet: showing the signed-in account's rank until a " +
					"match identifies which player is you")
			}
		}
	}

	if cfg.LobbyMMR {
		b.lobby = lobby.New(client)
		zap.L().Info("lobby: reading every player's MMR at the start of a match")
	}

	return b, func() {
		// Ordered innermost first: both of these are built on the session, so
		// neither may outlive it.
		if b.lobby != nil {
			if err := b.lobby.Close(); err != nil {
				zap.L().Debug("lobby: closing", zap.Error(err))
			}
		}
		if err := b.ranks.Close(); err != nil {
			zap.L().Debug("presence: closing rank source", zap.Error(err))
		}
		if err := client.Close(); err != nil {
			zap.L().Debug("psynet: closing session", zap.Error(err))
		}
	}
}

func logBackendFailure(err error) {
	if errors.Is(err, rlmmr.ErrNoCredentials) || errors.Is(err, rlmmr.ErrCredentialsRejected) {
		zap.L().Warn("psynet: no usable Epic sign-in stored; " +
			"run 'lobby-iq link' once, falling back to discord_ranks for now")
		return
	}
	zap.L().Warn("psynet: could not reach Rocket League's backend, "+
		"falling back to discord_ranks", zap.Error(err))
}
