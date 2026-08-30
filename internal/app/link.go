package app

import (
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	fyneapp "fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/ShakedShitrit/lobby-iq/internal/browser"
	"github.com/ShakedShitrit/lobby-iq/internal/config"
	"github.com/ShakedShitrit/lobby-iq/internal/liverank"
	"github.com/ShakedShitrit/lobby-iq/internal/selfid"
	"github.com/ShakedShitrit/rlmmr"
)

// rankSourceComment is written above discord_rank_source when linking has to
// add the setting to a config that predates it.
const rankSourceComment = `Where the card's rank badge comes from: "config" reads discord_ranks,
"live" reads your real rank from Rocket League. Set to live by "lobby-iq link".`

// LinkResult describes a completed sign-in.
type LinkResult struct {
	AccountID string
	// Ranks is one rendered line per gamemode, ready to display.
	Ranks []string
	// ConfigPath is the file discord_rank_source was written to, empty when
	// the config was left alone.
	ConfigPath string
	// ConfigErr is set when the sign-in worked but the config could not be
	// updated - a partial success the user needs to hear about, not a
	// failure worth sending them back through the sign-in.
	ConfigErr error
}

// PerformLink exchanges an Epic authorization code for a stored credential,
// verifies it by reading the ranks back, and points the config at the live
// source.
//
// The verification is deliberate: writing "use the live rank" into the config
// before knowing the live rank answers would leave the app configured for a
// source that does not work.
func PerformLink(cfg *config.Config, code string, writeConfig bool) (LinkResult, error) {
	var result LinkResult

	creds, err := rlmmr.LinkWithEpicAuthCode(cfg.RLMMRCredentials, code)
	if err != nil {
		return result, err
	}
	result.AccountID = creds.AccountID

	client, err := rlmmr.New(rlmmr.Options{CredentialsPath: cfg.RLMMRCredentials})
	if err != nil {
		return result, fmt.Errorf("signed in, but connecting failed: %w", err)
	}
	defer client.Close()

	// Verified against whoever is already named as "me", not against the
	// account just linked. When this link is a separate query account, the
	// ranks shown back are the ones the card will actually carry - which is
	// the thing worth confirming.
	source, err := liverank.New(client, cfg.RLMMRSelfID)
	if err != nil {
		return result, fmt.Errorf("signed in, but reading your rank failed: %w", err)
	}
	defer source.Close()

	for _, mode := range []string{"1v1", "2v2", "3v3"} {
		if _, label, ok := source.TierFor(mode); ok {
			result.Ranks = append(result.Ranks, fmt.Sprintf("%-4s %s", mode, label))
		} else {
			result.Ranks = append(result.Ranks, fmt.Sprintf("%-4s unranked", mode))
		}
	}

	if !writeConfig {
		return result, nil
	}

	// Deliberately not recording the linked account as "me". This sign-in may
	// be a separate query account, and writing it down as the player would put
	// its rank on the card - the exact mistake the two-account setup exists to
	// avoid. Who is playing is worked out from the game instead; see
	// internal/selfid.
	//
	// Any previously detected player is dropped, though: a new sign-in often
	// accompanies a new player, and a remembered one that is no longer at the
	// keyboard would go on being shown. Done here rather than in the GUI so
	// that "lobby-iq link" behaves the same way. A running GUI also clears
	// its in-memory copy, which this cannot reach.
	selfid.Open(selfid.FileName, cfg.RLMMRSelfID).Forget()

	path, err := config.SetValue(cfg.SourceFile, "discord_rank_source", "live", rankSourceComment)
	if err != nil {
		result.ConfigErr = err
		return result, nil
	}
	result.ConfigPath = path
	return result, nil
}

// RunLinkWindow drives the sign-in from a window of its own, for the "link"
// command.
//
// This exists because the binary is linked for the Windows GUI subsystem, so
// there is no console to prompt in: a shell does not wait for the process and
// never connects its stdin. A window is the only input that works in every way
// the app gets launched, including double-clicked from Explorer.
func RunLinkWindow(cfg *config.Config, writeConfig bool) error {
	a := fyneapp.New()
	// Left on Fyne's default theme, which follows the OS's light/dark setting,
	// so this window matches the main one rather than being pinned to light.
	w := buildLinkWindow(a, cfg, writeConfig, nil)
	w.ShowAndRun()
	return nil
}

// ShowLinkWindow opens the same sign-in over an already-running GUI, so that
// someone who has never used a command line can reach it from a button.
//
// onDone is called after a successful sign-in, on the UI goroutine.
func ShowLinkWindow(a fyne.App, cfg *config.Config, writeConfig bool, onDone func()) {
	buildLinkWindow(a, cfg, writeConfig, onDone).Show()
}

// buildLinkWindow lays out the sign-in. It is shared so the button and the
// command cannot drift into offering different instructions for the same task.
func buildLinkWindow(a fyne.App, cfg *config.Config, writeConfig bool, onDone func()) fyne.Window {
	authURL := rlmmr.EpicAuthURL()

	w := a.NewWindow("LobbyIQ - Epic sign-in")

	intro := widget.NewLabel(
		"Sign in to Epic once so the Discord card can show your real rank.\n\n" +
			"Your browser should have opened Epic's sign-in page. It ends on a\n" +
			"page of JSON containing \"authorizationCode\" - paste the whole page\n" +
			"below, or just the code.")
	intro.Wrapping = fyne.TextWrapWord

	entry := widget.NewMultiLineEntry()
	entry.SetPlaceHolder(`{"redirectUrl":"...","authorizationCode":"..."}`)
	entry.Wrapping = fyne.TextWrapWord

	status := widget.NewLabel("")
	status.Wrapping = fyne.TextWrapWord

	reopen := widget.NewButton("Open Epic sign-in again", func() {
		if err := browser.Open(authURL); err != nil {
			status.SetText("Could not open a browser. Copy this address instead:\n" + authURL)
		}
	})

	linkBtn := widget.NewButton("Link", nil)
	linkBtn.Importance = widget.HighImportance

	done := false
	linkBtn.OnTapped = func() {
		if done {
			w.Close()
			return
		}
		code := strings.TrimSpace(entry.Text)
		if code == "" {
			status.SetText("Paste the code (or the whole JSON page) first.")
			return
		}

		linkBtn.Disable()
		status.SetText("Signing in...")

		// Off the UI goroutine: the exchange and the rank read are network
		// calls, and blocking here would freeze the window.
		go func() {
			result, err := PerformLink(cfg, code, writeConfig)
			fyne.Do(func() {
				if err != nil {
					status.SetText("Sign-in failed:\n" + err.Error() +
						"\n\nCodes expire within minutes - fetch a fresh one and try again.")
					linkBtn.Enable()
					return
				}
				done = true
				status.SetText(linkSummary(result))
				entry.Disable()
				linkBtn.SetText("Close")
				linkBtn.Enable()
				if onDone != nil {
					onDone()
				}
			})
		}()
	}

	w.SetContent(container.NewBorder(
		container.NewVBox(intro, reopen),
		container.NewVBox(linkBtn, status),
		nil, nil,
		entry,
	))
	w.Resize(fyne.NewSize(560, 420))

	// Opened after the window is built so the browser does not steal focus
	// before there is anything to come back to.
	if err := browser.Open(authURL); err != nil {
		status.SetText("Could not open a browser. Copy this address instead:\n" + authURL)
	}

	return w
}

// linkSummary renders a completed sign-in for display.
func linkSummary(result LinkResult) string {
	var b strings.Builder
	b.WriteString("Signed in as " + result.AccountID + "\n\n")
	for _, line := range result.Ranks {
		b.WriteString("  " + line + "\n")
	}
	switch {
	case result.ConfigErr != nil:
		b.WriteString("\nSigned in, but the config could not be updated:\n")
		b.WriteString(result.ConfigErr.Error())
		b.WriteString("\nSet discord_rank_source: live by hand to finish.")
	case result.ConfigPath != "":
		b.WriteString("\nSet discord_rank_source: live in\n" + result.ConfigPath)
		b.WriteString("\nRestart LobbyIQ to see your real rank on the card.")
	default:
		b.WriteString("\nConfig left alone. Set discord_rank_source: live to use it.")
	}
	return b.String()
}
