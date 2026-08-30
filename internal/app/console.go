// Package app wires the rlstats client into a bubbletea TUI.
package app

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ShakedShitrit/lobby-iq/internal/browser"
	"github.com/ShakedShitrit/lobby-iq/internal/config"
	"github.com/ShakedShitrit/lobby-iq/internal/history"
	"github.com/ShakedShitrit/lobby-iq/internal/lobby"
	"github.com/ShakedShitrit/lobby-iq/internal/rlstats"
	"github.com/ShakedShitrit/lobby-iq/internal/session"
)

type matchStateMsg rlstats.MatchState

type openedMsg struct{ err error }

// lobbyUpdatedMsg says ratings changed. It carries nothing: the view reads
// them from the lobby source, so arriving is the whole message.
type lobbyUpdatedMsg struct{}

type model struct {
	state   rlstats.MatchState
	history *history.Store
	session *session.Tracker
	// lobby is nil unless lobby_mmr is on, in which case the MMR column is
	// left out entirely rather than shown empty.
	lobby  *lobby.Source
	cursor int
	status string
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case matchStateMsg:
		m.state = rlstats.MatchState(msg)
		if m.cursor >= len(m.state.Players) {
			m.cursor = len(m.state.Players) - 1
		}
		if m.cursor < 0 {
			m.cursor = 0
		}
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.state.Players)-1 {
				m.cursor++
			}
		case "r":
			if m.session != nil {
				m.session.Reset()
				m.status = "session reset"
			}
		case "enter":
			if m.cursor >= 0 && m.cursor < len(m.state.Players) {
				p := m.state.Players[m.cursor]
				m.status = fmt.Sprintf("opening tracker.network for %s...", strings.TrimSpace(p.Name))
				return m, openBrowserCmd(p.TrackerURL())
			}
		}
		return m, nil

	case openedMsg:
		if msg.err != nil {
			m.status = fmt.Sprintf("failed to open browser: %v", msg.err)
		}
		return m, nil

	case lobbyUpdatedMsg:
		// Nothing to store: View reads the ratings straight from the source,
		// and returning is enough to trigger a redraw.
		return m, nil
	}
	return m, nil
}

func openBrowserCmd(url string) tea.Cmd {
	return func() tea.Msg {
		return openedMsg{err: browser.Open(url)}
	}
}

func (m model) View() string {
	var b strings.Builder
	if m.state.Arena != "" {
		fmt.Fprintf(&b, "Arena: %s\n\n", m.state.Arena)
	}
	if len(m.state.Players) == 0 {
		b.WriteString("waiting for match data...\n")
	}

	mmrHeader := ""
	if m.lobby != nil {
		mmrHeader = fmt.Sprintf(" %6s", "MMR")
	}
	fmt.Fprintf(&b, "   %-20s %-14s %-10s %5s %7s %5s %7s%s\n",
		"NAME", "PLATFORM", "TEAM", "GOALS", "ASSISTS", "SAVES", "GAMES", mmrHeader)
	for i, p := range m.state.Players {
		team := m.state.Teams[p.TeamNum]
		if team == "" {
			team = fmt.Sprintf("Team %d", p.TeamNum)
		}
		games := ""
		repeat := false
		if m.history != nil {
			if n := m.history.Games(p.PrimaryId); n > 0 {
				games = fmt.Sprintf("%d", n)
				repeat = n > 1
			}
		}
		games = fmt.Sprintf("%7s", games)
		if repeat {
			games = "\x1b[33m" + games + "\x1b[0m"
		}
		line := fmt.Sprintf("%-20s %-14s %-10s %5d %7d %5d %s%s",
			strings.TrimSpace(p.Name), p.Platform(), team, p.Goals, p.Assists, p.Saves, games,
			m.mmrCell(p))

		cursor := "  "
		if i == m.cursor {
			cursor = "> "
			line = "\x1b[7m" + line + "\x1b[0m"
		}
		fmt.Fprintf(&b, "%s%s\n", cursor, line)
	}

	b.WriteString("\n" + m.sessionLine() + "\n")

	b.WriteString("\n↑/↓ to move, enter to open tracker.network, r to reset session, q to quit.\n")
	if m.status != "" {
		b.WriteString(m.status + "\n")
	}
	return b.String()
}

// mmrCell renders one player's rating, or blank space while the lookup is
// still outstanding. It returns the empty string when lobby ratings are off,
// so the column disappears rather than standing empty.
func (m model) mmrCell(p rlstats.Player) string {
	if m.lobby == nil {
		return ""
	}
	entry, ok := m.lobby.For(m.state, p.PrimaryId)
	switch {
	case !ok:
		// Still being read. A dot rather than a blank distinguishes "not yet"
		// from "unranked", which is the question being asked at kickoff.
		return fmt.Sprintf(" \x1b[2m%6s\x1b[0m", "...")
	case !entry.Ranked:
		return fmt.Sprintf(" \x1b[2m%6s\x1b[0m", "unrkd")
	default:
		return fmt.Sprintf(" %6d", entry.MMR)
	}
}

// sessionLine renders this run's win/loss tally, e.g.
// "SESSION  2v2 +3  3v3 -2  |  TOTAL +1".
func (m model) sessionLine() string {
	if m.session == nil {
		return ""
	}
	stats := m.session.Snapshot()
	if len(stats) == 0 {
		return "\x1b[2mSESSION  no finished matches yet\x1b[0m"
	}

	var b strings.Builder
	b.WriteString("SESSION ")
	for _, s := range stats {
		fmt.Fprintf(&b, " %s %s", s.Mode, colorNet(s.Net(), s.String()))
	}
	total := m.session.Total()
	fmt.Fprintf(&b, "  |  TOTAL %s", colorNet(total.Net(), total.String()))
	return b.String()
}

// colorNet tints a net tally green when up, red when down, and leaves it
// plain at even.
func colorNet(net int, text string) string {
	switch {
	case net > 0:
		return "\x1b[32m" + text + "\x1b[0m"
	case net < 0:
		return "\x1b[31m" + text + "\x1b[0m"
	}
	return text
}

// RunConsole connects to the Stats API and runs the lightweight terminal UI
// until the user quits.
func RunConsole(cfg *config.Config) error {
	addr := fmt.Sprintf("127.0.0.1:%d", cfg.Port)
	hist := history.Open(history.FileName)
	sess := session.New()

	back, closeBackend := startBackend(cfg)
	rich := startPresence(cfg, sess, back)
	defer rich.Close()
	// After the card, since the card reads from the rank source.
	defer closeBackend()

	p := tea.NewProgram(
		model{history: hist, session: sess, lobby: back.lobby},
		tea.WithAltScreen())

	// Ratings arrive on their own schedule, not with match state, so a landed
	// lookup has to prompt a redraw of its own.
	if back.lobby != nil {
		back.lobby.OnUpdate(func() { p.Send(lobbyUpdatedMsg{}) })
	}

	go rlstats.Watch(addr, func(s rlstats.MatchState) {
		hist.Observe(s.MatchGuid, s.Players)
		sess.Observe(s)
		rich.Observe(s)
		back.Observe(s)
		p.Send(matchStateMsg(s))
	})

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("running tui: %w", err)
	}
	return nil
}
