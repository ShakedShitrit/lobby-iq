package app

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"go.uber.org/zap"

	"github.com/ShakedShitrit/lobby-iq/internal/config"
)

// newLinkButton builds the sign-in control for the main window.
//
// It exists so that signing in does not require knowing there is a command
// line, let alone a "link" subcommand on it. The command stays: a script has no
// use for a button, and someone who already works that way should not have to
// stop.
//
// Signing in from here does not take effect until the app is restarted. The
// backend session, the rank source, the lobby lookups and the table's columns
// are all decided at startup, and rebuilding that lot underneath a running
// window is a great deal of machinery for something that happens once. The
// button says so plainly instead.
func newLinkButton(a fyne.App, cfg *config.Config, b *backend) *fyne.Container {
	// Left unwrapped: this sits in a single row in the window's bottom corner,
	// where a wrapping label would grow the row rather than the text.
	status := widget.NewLabel("")

	var btn *widget.Button
	btn = widget.NewButton("", func() {
		ShowLinkWindow(a, cfg, true, func() {
			// A new sign-in may also be a new player. The detected identity is
			// worked out from the game and so survives a re-link on its own,
			// which would leave the card showing whoever used to be at the
			// keyboard; dropping it costs one match of re-detection.
			b.self.Forget()
			zap.L().Info("link: signed in again; the player will be detected afresh")

			btn.SetText("Signed in")
			btn.Disable()
			btn.Importance = widget.MediumImportance
			btn.Refresh()
			status.SetText("Restart LobbyIQ to use the new account.")
		})
	})

	switch {
	case b.client != nil:
		// A session exists, so the stored sign-in works. The only thing left
		// to offer is replacing it - which is a real need, since the account
		// doing the querying is meant to be swappable.
		btn.SetText("Switch Epic account")
		status.SetText("Signed in. Ranks are coming from Rocket League's backend.")

	case cfg.PsyNetEnabled():
		// Configured to use the backend but no session: either never linked,
		// or the stored credentials have lapsed. Both are fixed by signing in,
		// and they are indistinguishable to whoever is reading this.
		btn.SetText("Sign in to Epic")
		btn.Importance = widget.HighImportance
		status.SetText("Not signed in - ranks are coming from config.yaml.")

	default:
		btn.SetText("Sign in to Epic")
		status.SetText("Sign in once to show live ranks and lobby MMR.")
	}

	// The button goes last so that it lands in the window's corner, with its
	// explanation reading into it from the left.
	return container.NewHBox(status, btn)
}
