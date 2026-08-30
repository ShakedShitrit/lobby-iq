package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ShakedShitrit/lobby-iq/internal/config"
	"github.com/ShakedShitrit/lobby-iq/internal/rlsetup"
)

func newSetupCmd() *cobra.Command {
	var (
		dryRun bool
		quiet  bool
		port   int
	)

	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Switch on Rocket League's Stats API so LobbyIQ can read your matches",
		Long: `Points Rocket League's Stats API at LobbyIQ.

The game exports live match data over a local socket, but only if TAStatsAPI.ini
in your Rocket League config says so. This finds that file - wherever your
Documents folder actually is, which on many machines is inside OneDrive - and
makes sure the port matches the one LobbyIQ listens on and that the export is
not switched off.

The installer runs this for you. It is worth running again if matches stop
appearing: a Rocket League update can regenerate that file from the game's own
defaults and undo the change.

Nothing else in the file is touched, and the original is kept alongside it.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSetup(port, dryRun, quiet)
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"report what would change without writing anything")
	cmd.Flags().BoolVar(&quiet, "quiet", false,
		"say nothing unless something went wrong (used by the installer)")
	cmd.Flags().IntVar(&port, "port", 0,
		"port to configure (default: the one in config.yaml)")
	return cmd
}

func runSetup(port int, dryRun, quiet bool) error {
	// The configured port rather than a constant, so someone who moved it in
	// config.yaml does not end up with the game exporting to one port while
	// the app listens on another.
	if port == 0 {
		cfg, err := config.Load(cfgFile, nil)
		if err != nil {
			return fmt.Errorf("reading config: %w", err)
		}
		port = cfg.Port
	}

	if dryRun {
		report, err := rlsetup.Plan(port)
		if err != nil {
			return err
		}
		fmt.Print(report.Summary())
		if len(report.Changes) > 0 || report.Created {
			fmt.Println("\n(dry run - nothing was written)")
		}
		return nil
	}

	report, err := rlsetup.Apply(port)
	if err != nil {
		return err
	}

	if !quiet {
		fmt.Print(report.Summary())
	}

	// The game running is the one outcome that looks like success and is not:
	// the file is correct now and will be overwritten when Rocket League
	// closes. Reported through the exit code so the installer can say so.
	if !report.OK() {
		if quiet {
			fmt.Fprint(os.Stderr, report.Summary())
		}
		return errRocketLeagueRunning
	}
	return nil
}

// errRocketLeagueRunning is a distinct error so callers can tell it from a
// genuine failure to write.
var errRocketLeagueRunning = fmt.Errorf(
	"rocket league is running, so the change will be undone when it closes")
