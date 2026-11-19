package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/ShakedShitrit/lobby-iq/internal/app"
	"github.com/ShakedShitrit/lobby-iq/internal/config"
	"github.com/ShakedShitrit/lobby-iq/internal/inputrec"
	"github.com/ShakedShitrit/lobby-iq/internal/inputview"
	"github.com/ShakedShitrit/lobby-iq/internal/logging"
)

func newInputsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inputs",
		Short: "Review the controller inputs recorded during your matches",
		Long: `With input_recording enabled in config.yaml, LobbyIQ records what you do on
your controller or keyboard during every match. Recordings are temporary -
only the newest few are kept - unless you save the match's replay in Rocket
League, which keeps its recording for good. These commands list the
recordings and play them back alongside the replay. See
docs/input-recording.md.`,
	}
	cmd.AddCommand(newInputsViewCmd(), newInputsListCmd())
	return cmd
}

func newInputsViewCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "view [file]",
		Short:        "Play a recording back as an animated controller (default: the newest)",
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgFile, nil)
			if err != nil {
				return err
			}
			logger := logging.Init(cfg.LogLevel)
			defer logger.Sync()

			store := app.Store(cfg)
			// Catch up on replays saved while LobbyIQ wasn't running.
			store.Promote()
			path := ""
			if len(args) == 1 {
				path = args[0]
			}
			// Its own connection to the game, to follow a replay as it
			// plays. With the main LobbyIQ window open, its Inputs button is
			// the better way in: it shares that window's connection.
			inputview.Run(store, path, fmt.Sprintf("127.0.0.1:%d", cfg.Port))
			return nil
		},
	}
}

func newInputsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "list",
		Short:        "List the recordings, newest first; ★ marks those kept for good",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(cfgFile, nil)
			if err != nil {
				return err
			}
			store := app.Store(cfg)
			store.Promote()
			entries, err := store.List()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(entries) == 0 {
				abs, _ := filepath.Abs(store.Dir())
				fmt.Fprintf(out, "No recordings in %s\n", abs)
				return nil
			}
			for _, e := range entries {
				mark := " "
				if e.Kept {
					mark = "★"
				}
				rec, err := inputrec.Load(e.Path)
				if err != nil {
					fmt.Fprintf(out, "%s %s  (unreadable: %v)\n", mark, e.Path, err)
					continue
				}
				extra := ""
				if !rec.Complete {
					extra += "  incomplete"
				}
				if rec.ReplayName != "" {
					extra += fmt.Sprintf("  replay %q", rec.ReplayName)
				}
				fmt.Fprintf(out, "%s %s  %d kickoffs  %d changes  %ds%s\n",
					mark, e.Path, len(rec.Segments), len(rec.Samples), rec.DurationMs/1000, extra)
			}
			return nil
		},
	}
}
