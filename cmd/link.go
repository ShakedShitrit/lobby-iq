package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ShakedShitrit/lobby-iq/internal/app"
	"github.com/ShakedShitrit/lobby-iq/internal/config"
	"github.com/ShakedShitrit/rlmmr"
)

func newLinkCmd() *cobra.Command {
	var (
		code    string
		keepCfg bool
	)

	cmd := &cobra.Command{
		Use:   "link",
		Short: "Sign in to Epic once so the card can show your real rank",
		Long: `Signs in to Epic so Rich Presence can show your live rank and MMR
instead of the one written by hand in config.yaml.

Opens a small window with Epic's sign-in page in your browser. You are signed
in on Epic's own site - no password is ever typed here - and paste back the
one-time code it gives you. The resulting token is long-lived and refreshes
itself, so this is a one-time step.

On success, discord_rank_source is set to "live" in your config.

Pass --code to skip the window entirely, which is what a script wants.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runLink(code, !keepCfg)
		},
	}

	cmd.Flags().StringVar(&code, "code", "",
		"authorization code, if you already have one (skips the window)")
	cmd.Flags().BoolVar(&keepCfg, "keep-config", false,
		"link only; do not set discord_rank_source to live")
	return cmd
}

// runLink picks how to obtain the authorization code.
//
// The binary is linked for the Windows GUI subsystem, so there is normally no
// console: a shell does not wait for the process and never connects its stdin,
// which makes an interactive prompt impossible. A window is therefore the
// default. The other two paths exist so the command stays usable from a script
// or a pipe, where a window would be worse than useless.
func runLink(code string, writeConfig bool) error {
	switch {
	case code != "":
		return linkHeadless(code, writeConfig)
	case stdinIsPiped():
		return linkFromStdin(writeConfig)
	default:
		cfg, err := config.Load(cfgFile, nil)
		if err != nil {
			return err
		}
		return app.RunLinkWindow(cfg, writeConfig)
	}
}

func linkHeadless(code string, writeConfig bool) error {
	cfg, err := config.Load(cfgFile, nil)
	if err != nil {
		return err
	}
	result, err := app.PerformLink(cfg, code, writeConfig)
	if err != nil {
		return err
	}
	reportLink(result)
	return nil
}

func linkFromStdin(writeConfig bool) error {
	cfg, err := config.Load(cfgFile, nil)
	if err != nil {
		return err
	}

	fmt.Println("Paste the Epic authorization code (or the whole JSON page):")
	fmt.Printf("  %s\n", rlmmr.EpicAuthURL())

	input, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if strings.TrimSpace(input) == "" {
		if err != nil {
			return fmt.Errorf("read authorization code: %w", err)
		}
		return fmt.Errorf("no authorization code supplied")
	}

	result, err := app.PerformLink(cfg, input, writeConfig)
	if err != nil {
		return err
	}
	reportLink(result)
	return nil
}

func reportLink(result app.LinkResult) {
	fmt.Println()
	fmt.Printf("Signed in as %s\n", result.AccountID)
	for _, line := range result.Ranks {
		fmt.Printf("  %s\n", line)
	}
	fmt.Println()
	switch {
	case result.ConfigErr != nil:
		fmt.Printf("Signed in, but could not update the config: %v\n", result.ConfigErr)
		fmt.Println("Set discord_rank_source: live by hand to finish.")
	case result.ConfigPath != "":
		fmt.Printf("Set discord_rank_source: live in %s\n", result.ConfigPath)
		fmt.Println("Restart LobbyIQ and the card will show your real rank.")
	default:
		fmt.Println("Config left alone. Set discord_rank_source: live to use it.")
	}
}

// stdinIsPiped reports whether stdin carries data rather than being a console
// or nothing at all. Only then is reading from it worth attempting.
func stdinIsPiped() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice == 0 && info.Size() >= 0
}
