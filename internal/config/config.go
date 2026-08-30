// Package config loads LobbyIQ's configuration from a config file, env
// vars (LOBBYIQ_*), and CLI flags, in ascending priority.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

type Config struct {
	Port        int    `mapstructure:"port"`
	LogLevel    string `mapstructure:"log_level"`
	Lightweight bool   `mapstructure:"lightweight"`
	// DiscordClientID is the client ID of the Discord application whose name
	// and art assets the Rich Presence card uses. Rich Presence is off unless
	// this is set; see DISCORD.md.
	DiscordClientID string `mapstructure:"discord_client_id"`
	// DiscordDisabled turns Rich Presence off even when a client ID is
	// configured, so the ID can stay in config.yaml between runs.
	DiscordDisabled bool `mapstructure:"discord_disabled"`
	// DiscordAssets names the images the card uses.
	DiscordAssets DiscordAssets `mapstructure:"discord_assets"`
	// DiscordRanks maps a gamemode ("2v2") to your rank in it ("Champion I").
	// Rocket League's Stats API doesn't report rank, so this is entered by
	// hand; a mode listed here shows that rank's badge on the card. Used when
	// DiscordRankSource is "config".
	DiscordRanks map[string]string `mapstructure:"discord_ranks"`
	// DiscordRankSource chooses where the card's rank badge comes from:
	// "config" uses DiscordRanks, "live" reads your real rank from Rocket
	// League's backend. Live needs a one-time sign-in; without it the run
	// falls back to config rather than failing.
	DiscordRankSource string `mapstructure:"discord_rank_source"`
	// RLMMRCredentials is the path to the Epic credentials file, not the
	// credentials themselves - those are never stored in the config, which
	// gets shared in bug reports and committed to repositories. Empty uses
	// the per-user default that "lobby-iq link" writes.
	RLMMRCredentials string `mapstructure:"rlmmr_credentials"`
	// RLMMRSelfID is your own player ID, "Epic|<hex>|0", as the Stats API
	// spells it in PrimaryId.
	//
	// It exists so the credentials can belong to a *different* Epic account
	// than the one playing. Rocket League's backend allows one live session per
	// account and evicts the older one, so signing in here as the account in
	// the game disconnects the game after every match. A separate account
	// avoids that, but then "my rank" can no longer mean "the signed-in
	// account" - hence naming yourself explicitly.
	//
	// Empty means the signed-in account, which is correct when the credentials
	// are your own and you accept the disconnects.
	RLMMRSelfID string `mapstructure:"rlmmr_self_id"`
	// LobbyMMR turns on reading every player's MMR at the start of a match.
	// Needs the same sign-in as live rank, and shares its session.
	LobbyMMR bool `mapstructure:"lobby_mmr"`

	// SourceFile is the config file these values came from, or empty if none
	// was found. Set by Load; not itself a setting.
	SourceFile string `mapstructure:"-"`
}

// DiscordAssets names the three images the presence card can show. Each value
// is either the key of an art asset uploaded to the Discord application, or an
// https:// URL that Discord fetches directly - the latter is what lets the
// images be changed from config alone, with nothing uploaded anywhere.
type DiscordAssets struct {
	// Logo is the large icon.
	Logo string `mapstructure:"logo"`
	// Blue and Orange are the small badge, chosen by your team.
	Blue   string `mapstructure:"blue"`
	Orange string `mapstructure:"orange"`
}

// DiscordEnabled reports whether Rich Presence should be published.
func (c *Config) DiscordEnabled() bool {
	return c.DiscordClientID != "" && !c.DiscordDisabled
}

// LiveRankEnabled reports whether the card should read the real rank from
// Rocket League's backend rather than from discord_ranks.
func (c *Config) LiveRankEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(c.DiscordRankSource)) {
	case "live", "rlmmr", "psynet":
		return true
	default:
		return false
	}
}

// PsyNetEnabled reports whether anything needs a Rocket League backend
// session. Both features share one, so this is what decides to open it.
func (c *Config) PsyNetEnabled() bool {
	return c.LiveRankEnabled() || c.LobbyMMR
}

// Load reads configuration from cfgFile (or ./config.yaml if cfgFile is
// empty), then overlays the LOBBYIQ_* environment variables and any bound
// flags on top.
func Load(cfgFile string, flags *pflag.FlagSet) (*Config, error) {
	v := viper.New()

	v.SetDefault("port", 49123)
	v.SetDefault("log_level", "info")
	v.SetDefault("lightweight", false)
	v.SetDefault("discord_client_id", "")
	v.SetDefault("discord_disabled", false)
	// A URL rather than an uploaded-asset key, so the large icon works without
	// anything having been uploaded to whichever Discord application is in
	// use. Discord fetches it once and hosts its own copy.
	v.SetDefault("discord_assets.logo",
		"https://raw.githubusercontent.com/ShakedShitrit/lobby-iq/main/assets/brand/logo.png")
	v.SetDefault("discord_assets.blue", "team_blue")
	v.SetDefault("discord_assets.orange", "team_orange")
	// "config" preserves existing behaviour for anyone upgrading, who has not
	// signed in and would otherwise lose their rank badge.
	v.SetDefault("discord_rank_source", "config")
	v.SetDefault("rlmmr_credentials", "")
	v.SetDefault("rlmmr_self_id", "")
	v.SetDefault("lobby_mmr", false)

	v.SetEnvPrefix("LOBBYIQ")
	v.AutomaticEnv()

	if flags != nil {
		if err := v.BindPFlag("port", flags.Lookup("port")); err != nil {
			return nil, err
		}
		if err := v.BindPFlag("log_level", flags.Lookup("log-level")); err != nil {
			return nil, err
		}
		if err := v.BindPFlag("lightweight", flags.Lookup("lightweight")); err != nil {
			return nil, err
		}
		if err := v.BindPFlag("discord_client_id", flags.Lookup("discord-client-id")); err != nil {
			return nil, err
		}
		if err := v.BindPFlag("discord_disabled", flags.Lookup("no-discord")); err != nil {
			return nil, err
		}
	}

	if cfgFile != "" {
		v.SetConfigFile(cfgFile)
	} else {
		v.SetConfigName("config")
		v.SetConfigType("yaml")
		v.AddConfigPath(".")
		// Double-clicked from Explorer the working directory is the exe's
		// folder, but a shortcut can point it anywhere - so look beside the
		// binary too, or the config silently wouldn't be found.
		if exe, err := os.Executable(); err == nil {
			v.AddConfigPath(filepath.Dir(exe))
		}
	}

	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) {
			return nil, fmt.Errorf("reading config: %w", err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	// Empty when no file was found and defaults are in force, which is a
	// useful thing to be able to see in the log.
	cfg.SourceFile = v.ConfigFileUsed()

	return &cfg, nil
}
