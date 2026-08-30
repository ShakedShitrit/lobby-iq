package cmd

import (
	"os"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/ShakedShitrit/lobby-iq/internal/app"
	"github.com/ShakedShitrit/lobby-iq/internal/config"
	"github.com/ShakedShitrit/lobby-iq/internal/logging"
	"github.com/ShakedShitrit/lobby-iq/internal/startup"
)

var cfgFile string

// Execute runs the root command, returning any failure for main to report.
// It doesn't exit the process itself: with no console attached, a bare exit
// code is invisible and the caller needs the error to show it another way.
func Execute() error {
	return newRootCmd().Execute()
}

func newRootCmd() *cobra.Command {
	// Disable cobra's Windows "mousetrap". By default, when cobra sees it was
	// launched from explorer.exe it assumes a CLI was double-clicked by
	// mistake, prints "This is a command line tool. You need to open cmd.exe
	// and run it from there.", waits five seconds and exits - never reaching
	// RunE. LobbyIQ is a desktop app that happens to accept flags, so
	// double-clicking it is the normal way to start it, not a mistake.
	//
	// An empty string is cobra's documented way to turn this off.
	cobra.MousetrapHelpText = ""

	root := &cobra.Command{
		Use:          "lobby-iq",
		Short:        "Live Rocket League match viewer with tracker.network lookups",
		SilenceUsage: true,
		RunE:         runRoot,
	}

	root.PersistentFlags().StringVar(&cfgFile, "config", "", "path to config file (default ./config.yaml)")
	root.Flags().Int("port", 49123, "port RL's Stats API is listening on (see DefaultStatsAPI.ini)")
	root.Flags().String("log-level", "info", "log level: debug, info, warn, error")
	root.Flags().Bool("lightweight", false, "run the lightweight terminal UI instead of the desktop GUI")
	root.Flags().String("discord-client-id", "", "Discord application client ID, enabling Rich Presence (see DISCORD.md)")
	root.Flags().Bool("no-discord", false, "disable Discord Rich Presence even if a client ID is configured")

	root.AddCommand(newLinkCmd())

	return root
}

func runRoot(cmd *cobra.Command, _ []string) error {
	cfg, err := config.Load(cfgFile, cmd.Flags())
	if err != nil {
		return err
	}

	logger := logging.Init(cfg.LogLevel)
	defer logger.Sync()

	// First thing in the file every run: which binary this is, where it was
	// started from, and which config it actually picked up. Double-clicked
	// there is no console to answer those questions any other way.
	exe, _ := os.Executable()
	cwd, _ := os.Getwd()
	zap.L().Info("starting",
		zap.String("exe", exe),
		zap.String("cwd", cwd),
		zap.String("config", cfg.SourceFile),
		zap.Bool("gui", !cfg.Lightweight))

	if cfg.Lightweight {
		// Double-clicked into terminal mode there is no console to draw on,
		// so make one rather than rendering into the void.
		startup.Ensure()
		return app.RunConsole(cfg)
	}
	return app.RunGUI(cfg)
}
