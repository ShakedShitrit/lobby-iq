package app

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/ShakedShitrit/lobby-iq/internal/lobby"
)

// newPlaylistPicker builds the control that chooses which playlist the RANK
// and MMR columns report.
//
// It returns nil when there are no ratings to pick a playlist for, and callers
// must tolerate that: a nil *fyne.Container placed in a layout would panic, so
// the caller leaves the control out entirely instead.
//
// Switching costs nothing. A lookup fetches every playlist a player has
// played, so this only changes which cached rating is read - which is what
// makes it reasonable to sit in a 3v3 and look at everyone's 2v2.
func newPlaylistPicker(b *backend, table *widget.Table) *fyne.Container {
	if b.lobby == nil {
		return nil
	}

	playlists := lobby.Playlists()
	names := make([]string, 0, len(playlists))
	byName := make(map[string]int, len(playlists))
	for _, p := range playlists {
		names = append(names, p.Name)
		byName[p.Name] = p.ID
	}

	sel := widget.NewSelect(names, func(name string) {
		id, ok := byName[name]
		if !ok {
			return
		}
		b.lobby.SetPlaylist(id)
		table.Refresh()
	})
	// The first entry is "Current mode", which is the default the source
	// starts in - set without firing OnChanged, which would be a no-op that
	// redraws an empty table at startup.
	sel.Selected = names[0]

	return container.NewHBox(
		widget.NewLabelWithStyle("Ratings for", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		sel,
	)
}
