package presence

import (
	"strings"

	"go.uber.org/zap"
)

// RankSource supplies the rank badge shown on the card.
//
// It exists because there are two reasonable ways to know your rank and the
// card should not care which is in use: written by hand in config.yaml, or
// read live from Rocket League's own backend.
type RankSource interface {
	// TierFor returns the rank for a gamemode ("2v2"). The tier is Rocket
	// League's own tier number, used to pick the badge art; label is what
	// goes in the tooltip. ok is false when there is no rank to show, which
	// is normal - an unconfigured mode, or one you are unranked in.
	TierFor(mode string) (tier int, label string, ok bool)

	// MatchEnded tells the source a match just finished, so a live source can
	// re-read a rating that may have changed. It must not block: it is called
	// from the same path that records match state.
	MatchEnded()

	// Close releases anything held.
	Close() error
}

// configRanks serves ranks written by hand in config.yaml.
type configRanks struct {
	byMode map[string]int
}

// NewConfigRanks builds a RankSource from configured "mode: rank" entries.
//
// Names are resolved to tier numbers once, here, so a typo is reported when
// the app starts rather than silently doing nothing every match.
func NewConfigRanks(in map[string]string) RankSource {
	out := map[string]int{}
	for mode, rank := range in {
		mode = strings.ToLower(strings.TrimSpace(mode))
		if mode == "" || strings.TrimSpace(rank) == "" {
			continue
		}
		tier, name, ok := parseRank(rank)
		if !ok {
			zap.L().Warn("presence: ignoring unrecognised rank",
				zap.String("mode", mode), zap.String("rank", rank))
			continue
		}
		zap.L().Debug("presence: rank configured",
			zap.String("mode", mode), zap.String("rank", name))
		out[mode] = tier
	}
	return &configRanks{byMode: out}
}

func (c *configRanks) TierFor(mode string) (int, string, bool) {
	tier, ok := c.byMode[strings.ToLower(strings.TrimSpace(mode))]
	if !ok || tier <= 0 || tier >= len(rankNames) {
		return 0, "", false
	}
	return tier, rankNames[tier], true
}

// MatchEnded does nothing: a hand-written rank cannot change on its own.
func (c *configRanks) MatchEnded() {}

func (c *configRanks) Close() error { return nil }
