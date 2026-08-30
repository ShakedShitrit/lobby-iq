package app

import (
	"go.uber.org/zap"

	"github.com/ShakedShitrit/lobby-iq/internal/config"
	"github.com/ShakedShitrit/lobby-iq/internal/presence"
	"github.com/ShakedShitrit/lobby-iq/internal/session"
)

// startPresence begins publishing Discord Rich Presence, or returns nil when
// it isn't configured. *presence.Presence is nil-safe, so callers can use the
// result unconditionally.
//
// The rank source comes from the shared backend session rather than being
// opened here, so that it and the lobby ratings cannot evict each other.
func startPresence(cfg *config.Config, sess *session.Tracker, b *backend) *presence.Presence {
	if !cfg.DiscordEnabled() {
		zap.L().Debug("discord rich presence is off (no discord_client_id set)")
		return nil
	}
	zap.L().Info("discord rich presence enabled")

	return presence.New(presence.Options{
		ClientID: cfg.DiscordClientID,
		Assets: presence.Assets{
			Logo:   cfg.DiscordAssets.Logo,
			Blue:   cfg.DiscordAssets.Blue,
			Orange: cfg.DiscordAssets.Orange,
		},
		Ranks:   b.ranks,
		Session: sess,
	})
}
