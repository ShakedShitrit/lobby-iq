package app

import (
	"fmt"
	"image/color"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	fyneapp "fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/ShakedShitrit/lobby-iq/internal/browser"
	"github.com/ShakedShitrit/lobby-iq/internal/config"
	"github.com/ShakedShitrit/lobby-iq/internal/history"
	"github.com/ShakedShitrit/lobby-iq/internal/lobby"
	"github.com/ShakedShitrit/lobby-iq/internal/rlstats"
	"github.com/ShakedShitrit/lobby-iq/internal/session"
)

// Column names. Cells are filled by matching on these rather than on a column
// index, so that adding or hiding a column cannot silently shift what every
// later cell renders.
const (
	colTeam     = "TEAM"
	colName     = "NAME"
	colPlatform = "PLATFORM"
	colRank     = "RANK"
	colMMR      = "MMR"
	colGoals    = "GOALS"
	colAssists  = "ASSISTS"
	colSaves    = "SAVES"
	colGames    = "GAMES"
)

// guiColumnsFor lists the table's columns. Rank and MMR appear only when they
// can be filled in, so a run without a backend sign-in shows a table with no
// permanently empty columns in it.
func guiColumnsFor(withRatings bool) []string {
	cols := []string{colTeam, colName, colPlatform}
	if withRatings {
		cols = append(cols, colRank, colMMR)
	}
	return append(cols, colGoals, colAssists, colSaves, colGames)
}

// The team column is rendered as a color swatch rather than text.
const teamColumn = 0

// pal is the colour set the window draws with, chosen from the OS's light or
// dark setting and swapped when that changes.
//
// It is package state because the colours are read from inside table cell
// callbacks, which Fyne calls without any context of ours. Every read and
// every write happens on the app goroutine - cells are drawn there, and
// Settings.AddListener is documented to call back there too - so it needs no
// lock.
var pal = lightPalette

func guiColumnWidth(name string) float32 {
	switch name {
	case colTeam:
		return 44
	case colName:
		return 220
	case colPlatform:
		return 150
	case colRank:
		// Sized for the badge, plus room for the "unranked" and "..." text
		// shown before one is available.
		return 90
	default:
		return 90
	}
}

// teamImportance maps a team to a widget.Importance so its TEAM cell renders
// as a colored dot via Fyne's built-in label coloring, matching Rocket
// League's usual Blue/Orange team colors. A composite cell (icon + label)
// was tried instead but breaks text rendering inside widget.Table in this
// environment, so this sticks to a single plain *widget.Label per cell.
func teamImportance(team string) widget.Importance {
	switch strings.ToLower(team) {
	case "blue":
		return widget.HighImportance
	case "orange":
		return widget.WarningImportance
	default:
		return widget.MediumImportance
	}
}

// gamesImportance highlights players you've played multiple matches with, so
// familiar faces stand out from first-time teammates/opponents.
func gamesImportance(games int) widget.Importance {
	if games > 1 {
		return widget.WarningImportance
	}
	return widget.MediumImportance
}

// lobbyEntry looks up a player's rating, reporting false when ratings are off
// or the lookup has not landed.
func lobbyEntry(b *backend, state rlstats.MatchState, p rlstats.Player) (lobby.Entry, bool) {
	if b.lobby == nil {
		return lobby.Entry{}, false
	}
	return b.lobby.For(state, p.PrimaryId)
}

// ratingText renders a player's rank or MMR for the table.
//
// pending is reported separately from the text so the caller can mute a cell
// that is not an answer yet. The three states are worth telling apart while a
// match is starting: still being read, read and unranked, and read with a
// rating - "..." and "unranked" mean quite different things at kickoff.
func ratingText(b *backend, state rlstats.MatchState, p rlstats.Player, mmr bool) (text string, pending bool) {
	entry, ok := lobbyEntry(b, state, p)
	switch {
	case !ok:
		return "...", true
	case !entry.Ranked:
		if mmr {
			return "", true
		}
		return "unranked", true
	case mmr:
		return fmt.Sprintf("%d", entry.MMR), false
	default:
		return entry.Rank, false
	}
}

// ratingImportance mutes a cell that does not carry a rating yet, so a lobby
// still being read is visibly distinct from one that has been.
func ratingImportance(pending bool) widget.Importance {
	if pending {
		return widget.LowImportance
	}
	return widget.MediumImportance
}

// netColor tints a session tally green when up, red when down, and neutral
// at even.
func netColor(net int) color.Color {
	switch {
	case net > 0:
		return pal.Ok
	case net < 0:
		return pal.Danger
	}
	return pal.Muted
}

func boldText(s string, c color.Color) *canvas.Text {
	t := canvas.NewText(s, c)
	t.TextStyle = fyne.TextStyle{Bold: true}
	return t
}

// watchThemeVariant keeps pal in step with the OS's light/dark setting.
//
// Fyne repaints its own widgets on a theme change, but the colours named in
// palette are ours and it knows nothing about them; without this, switching
// the OS to dark would leave near-black text on a near-black background.
//
// onChange is called on the UI goroutine, and should refresh whatever draws
// with explicit colours.
func watchThemeVariant(a fyne.App, onChange func()) {
	pal = paletteFor(a.Settings().ThemeVariant())

	// AddListener rather than the channel-based AddChangeListener: it is
	// documented to invoke the callback on the app goroutine, which is exactly
	// where pal may be written and where onChange has to run.
	a.Settings().AddListener(func(s fyne.Settings) {
		pal = paletteFor(s.ThemeVariant())
		onChange()
	})
}

// RunGUI connects to the Stats API and runs the desktop GUI until the user
// closes the window.
func RunGUI(cfg *config.Config) error {
	addr := fmt.Sprintf("127.0.0.1:%d", cfg.Port)
	hist := history.Open(history.FileName)
	sess := session.New()

	back, closeBackend := startBackend(cfg)
	rich := startPresence(cfg, sess, back)
	defer rich.Close()
	// After the card, since the card reads from the rank source.
	defer closeBackend()

	a := fyneapp.New()
	// The theme is left as Fyne's default, which follows the OS's light/dark
	// setting. pal has to be read from it here, before any widget is built:
	// the colours below are captured at construction, so starting in dark mode
	// with the light palette still loaded would draw dark text on dark until
	// something happened to change the theme.
	pal = paletteFor(a.Settings().ThemeVariant())

	w := a.NewWindow("LobbyIQ")

	var mu sync.Mutex
	var state rlstats.MatchState

	title := canvas.NewText("LobbyIQ", pal.Text)
	title.TextStyle = fyne.TextStyle{Bold: true}
	title.TextSize = 20

	arenaLabel := canvas.NewText("Waiting for match data...", pal.Muted)

	status := canvas.NewText("Select a player to open their tracker.network profile", pal.Muted)

	// sessionBox is rebuilt wholesale on each refresh: the set of gamemodes
	// grows as you play, so there's no fixed set of labels to update in
	// place.
	sessionBox := container.NewHBox()
	refreshSession := func() {
		objs := []fyne.CanvasObject{boldText("SESSION", pal.Text)}

		stats := sess.Snapshot()
		if len(stats) == 0 {
			objs = append(objs, canvas.NewText("no finished matches yet", pal.Muted))
		} else {
			for _, s := range stats {
				objs = append(objs,
					canvas.NewText(s.Mode, pal.Muted),
					boldText(s.String(), netColor(s.Net())),
				)
			}
			total := sess.Total()
			objs = append(objs,
				canvas.NewText("TOTAL", pal.Muted),
				boldText(total.String(), netColor(total.Net())),
			)
		}

		sessionBox.Objects = objs
		sessionBox.Refresh()
	}
	refreshSession()

	resetSession := widget.NewButton("Reset", func() {
		sess.Reset()
		refreshSession()
	})

	cols := guiColumnsFor(back.lobby != nil)

	// Only built when there is a rank column to draw into.
	var icons *guiIcons
	if back.lobby != nil {
		icons = newGUIIcons()
	}

	table := widget.NewTable(
		func() (int, int) {
			mu.Lock()
			defer mu.Unlock()
			return len(state.Players) + 1, len(cols)
		},
		func() fyne.CanvasObject {
			// Every cell is a label with a badge stacked on it, and exactly
			// one of the two is ever visible. The existing note about
			// composite cells concerns an icon and text sharing a cell; these
			// never render together, which keeps each cell a single visible
			// widget as before.
			bg := canvas.NewRectangle(color.Transparent)

			img := canvas.NewImageFromResource(nil)
			img.FillMode = canvas.ImageFillContain
			img.SetMinSize(fyne.NewSize(rankIconSize, rankIconSize))
			img.Hide()

			// A separate text object for the MMR, because widget.Label takes
			// its colour from Importance and Importance has no entry for
			// "champion" - tinting by rank needs an arbitrary colour, which
			// only canvas.Text offers. Centred so it sits on the same line as
			// the labels beside it.
			mmr := canvas.NewText("", pal.Text)
			mmr.TextStyle = fyne.TextStyle{Bold: true}
			mmrBox := container.NewCenter(mmr)
			mmrBox.Hide()

			return container.NewStack(bg, widget.NewLabel(""), mmrBox, img)
		},
		func(id widget.TableCellID, obj fyne.CanvasObject) {
			cell := obj.(*fyne.Container)
			bg := cell.Objects[0].(*canvas.Rectangle)
			label := cell.Objects[1].(*widget.Label)
			mmrBox := cell.Objects[2].(*fyne.Container)
			mmrText := mmrBox.Objects[0].(*canvas.Text)
			icon := cell.Objects[3].(*canvas.Image)

			// Reset everything every time: cells are recycled as the table
			// scrolls, so anything left set carries into an unrelated row.
			icon.Hide()
			mmrBox.Hide()
			label.Show()
			label.Alignment = fyne.TextAlignLeading
			label.Importance = widget.MediumImportance
			bg.FillColor = color.Transparent
			bg.Refresh()

			if id.Row == 0 {
				label.TextStyle = fyne.TextStyle{Bold: true}
				label.SetText(cols[id.Col])
				return
			}
			mu.Lock()
			defer mu.Unlock()
			idx := id.Row - 1
			if idx >= len(state.Players) {
				label.TextStyle = fyne.TextStyle{}
				label.SetText("")
				return
			}
			p := state.Players[idx]
			team := state.Teams[p.TeamNum]
			if team == "" {
				team = fmt.Sprintf("Team %d", p.TeamNum)
			}

			// Teams read as blocks, rather than as six rows distinguished only
			// by a coloured dot.
			bg.FillColor = pal.RowTint(team)
			bg.Refresh()

			// Your own row is bold throughout. That reads at a glance and
			// spends no colour, which the teams have already claimed. selfid
			// may not know who you are yet, in which case no row is marked -
			// better than marking the wrong one.
			self, known := back.self.ID()
			label.TextStyle = fyne.TextStyle{Bold: known && self == p.PrimaryId}

			switch cols[id.Col] {
			case colTeam:
				label.Alignment = fyne.TextAlignCenter
				label.Importance = teamImportance(team)
				label.SetText("●")
			case colName:
				label.SetText(strings.TrimSpace(p.Name))
			case colPlatform:
				label.SetText(p.Platform())
			case colRank:
				entry, ok := lobbyEntry(back, state, p)
				// The badge is the point of this column, so the label is only
				// used to say why there is no badge yet.
				if ok && entry.Ranked {
					if res, have := icons.Get(entry.Tier); have {
						label.Hide()
						// Rocket League draws these badges for a dark in-game
						// background; on a light window they need one.
						bg.FillColor = pal.BadgeBackdrop
						bg.Refresh()
						icon.Resource = res
						icon.Refresh()
						icon.Show()
						break
					}
				}
				text, pending := ratingText(back, state, p, false)
				label.Importance = ratingImportance(pending)
				label.SetText(text)
			case colMMR:
				if entry, ok := lobbyEntry(back, state, p); ok && entry.Ranked {
					label.Hide()
					mmrText.Text = fmt.Sprintf("%d", entry.MMR)
					mmrText.Color = pal.TierColor(entry.Tier)
					mmrText.Refresh()
					mmrBox.Show()
					break
				}
				text, pending := ratingText(back, state, p, true)
				label.Importance = ratingImportance(pending)
				label.SetText(text)
			case colGoals:
				label.SetText(fmt.Sprintf("%d", p.Goals))
			case colAssists:
				label.SetText(fmt.Sprintf("%d", p.Assists))
			case colSaves:
				label.SetText(fmt.Sprintf("%d", p.Saves))
			case colGames:
				games := hist.Games(p.PrimaryId)
				label.Importance = gamesImportance(games)
				if games > 0 {
					label.SetText(fmt.Sprintf("%d", games))
				} else {
					label.SetText("")
				}
			}
		},
	)
	for i, name := range cols {
		table.SetColumnWidth(i, guiColumnWidth(name))
	}

	// Ratings arrive on their own schedule, not with match state, so the table
	// is redrawn when they land rather than waiting for the next tick.
	if back.lobby != nil {
		back.lobby.OnUpdate(func() { fyne.Do(table.Refresh) })
	}

	// Every colour named in palette is ours, so Fyne repainting its own
	// widgets on an OS light/dark switch is not enough - these have to be
	// re-read or the window is left with light text on a light ground.
	watchThemeVariant(a, func() {
		title.Color = pal.Text
		arenaLabel.Color = pal.Muted
		status.Color = pal.Muted
		title.Refresh()
		arenaLabel.Refresh()
		status.Refresh()
		refreshSession()
		table.Refresh()
	})

	playlistPicker := newPlaylistPicker(back, table)

	table.OnSelected = func(id widget.TableCellID) {
		table.Unselect(id)
		if id.Row == 0 {
			return
		}

		mu.Lock()
		players := state.Players
		mu.Unlock()

		idx := id.Row - 1
		if idx < 0 || idx >= len(players) {
			return
		}
		p := players[idx]
		name := strings.TrimSpace(p.Name)
		status.Color = pal.Muted
		status.Text = fmt.Sprintf("Opening tracker.network for %s...", name)
		status.Refresh()

		go func() {
			err := browser.Open(p.TrackerURL())
			fyne.Do(func() {
				if err != nil {
					status.Color = pal.Danger
					status.Text = fmt.Sprintf("Failed to open browser: %v", err)
					status.Refresh()
					return
				}
				status.Color = pal.Ok
				status.Text = fmt.Sprintf("Opened tracker.network for %s", name)
				status.Refresh()
			})
		}()
	}

	headerRows := []fyne.CanvasObject{
		title,
		arenaLabel,
		container.NewBorder(nil, nil, nil, resetSession, sessionBox),
	}
	// Left out rather than shown disabled when there are no ratings: an empty
	// control invites a click that cannot do anything.
	if playlistPicker != nil {
		headerRows = append(headerRows, playlistPicker)
	}
	headerRows = append(headerRows, widget.NewSeparator())

	header := container.NewVBox(headerRows...)

	card := widget.NewCard("", "", table)

	// The sign-in control sits in the bottom-right corner: it is used once, or
	// once more when an account changes, and does not belong in the header
	// competing with what the window is actually for. The status line keeps
	// the rest of the row and expands into it.
	footer := container.NewBorder(
		nil, nil,
		nil, newLinkButton(a, cfg, back),
		container.NewPadded(status),
	)

	content := container.NewBorder(
		header,
		footer,
		nil, nil,
		container.NewPadded(card),
	)

	w.SetContent(content)
	w.Resize(preferredWindowSize(content, cols))

	go rlstats.Watch(addr, func(s rlstats.MatchState) {
		hist.Observe(s.MatchGuid, s.Players)
		sess.Observe(s)
		rich.Observe(s)
		back.Observe(s)

		mu.Lock()
		state = s
		mu.Unlock()

		fyne.Do(func() {
			if s.Arena != "" {
				arenaLabel.Text = "Arena: " + s.Arena
				arenaLabel.Refresh()
			}
			refreshSession()
			table.Refresh()
		})
	})

	w.ShowAndRun()
	return nil
}
