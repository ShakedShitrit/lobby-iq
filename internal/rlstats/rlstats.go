// Package rlstats connects to Rocket League's local Stats API (a raw TCP
// socket, not a real websocket) and exposes the current match state in a
// form any UI (console, GUI, ...) can consume without touching the wire
// protocol.
package rlstats

import (
	"encoding/json"
	"maps"
	"net"
	"sort"
	"time"

	"go.uber.org/zap"
)

type Player struct {
	Name      string `json:"Name"`
	PrimaryId string `json:"PrimaryId"`
	TeamNum   int    `json:"TeamNum"`
	// Score is the in-game points total (the scoreboard's leftmost number),
	// not a goal count.
	Score   int `json:"Score"`
	Goals   int `json:"Goals"`
	Assists int `json:"Assists"`
	Saves   int `json:"Saves"`
	Shots   int `json:"Shots"`
	Demos   int `json:"Demos"`
}

type MatchState struct {
	Arena string
	// MatchGuid identifies the current match, stable across all UpdateState
	// ticks within it and changing between matches.
	MatchGuid string
	// Teams maps TeamNum to the team's display name (e.g. "Blue", "Orange").
	Teams map[int]string
	// Scores maps TeamNum to that team's current goal count.
	Scores map[int]int
	// Winner is the winning team's display name once the game reports one,
	// and empty while the match is still undecided.
	Winner string
	// Ended is true only on the state emitted for a MatchEnded event, which
	// is the signal that the result is final.
	Ended bool
	// TargetTeamNum is the TeamNum of the player the game is currently
	// following - you, unless you're spectating or watching a goal replay.
	// -1 when the game reports no target.
	TargetTeamNum int
	// TargetName is that same followed player's display name, empty when the
	// game reports no target.
	TargetName string
	// TimeSeconds is the game clock: seconds *elapsed* in the current match,
	// which is what the Stats API reports even though the on-screen clock in
	// most playlists counts down.
	TimeSeconds int
	// Overtime is true once the match has gone past regulation.
	Overtime bool
	// Replay is true while a goal replay is playing, during which the game
	// follows the scorer rather than you.
	Replay bool
	// Players is sorted by TeamNum, then Name.
	Players []Player
}

type envelope struct {
	Event string `json:"Event"`
	Data  string `json:"Data"`
}

type updateStateData struct {
	MatchGuid string   `json:"MatchGuid"`
	Players   []Player `json:"Players"`
	Game      struct {
		Teams []struct {
			Name    string `json:"Name"`
			TeamNum int    `json:"TeamNum"`
			Score   int    `json:"Score"`
		} `json:"Teams"`
		Arena       string `json:"Arena"`
		TimeSeconds int    `json:"TimeSeconds"`
		Overtime    bool   `json:"bOvertime"`
		Replay      bool   `json:"bReplay"`
		HasWinner   bool   `json:"bHasWinner"`
		Winner      string `json:"Winner"`
		HasTarget   bool   `json:"bHasTarget"`
		Target      struct {
			Name    string `json:"Name"`
			TeamNum int    `json:"TeamNum"`
		} `json:"Target"`
	} `json:"Game"`
}

// Watch connects to the Stats API at addr and invokes onUpdate with the
// latest known MatchState whenever it changes. It reconnects with backoff on
// failure and never returns.
func Watch(addr string, onUpdate func(MatchState)) {
	backoff := time.Second
	for {
		zap.L().Info("connecting", zap.String("addr", addr))
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			zap.L().Warn("connect failed", zap.Error(err), zap.Duration("retry_in", backoff))
			time.Sleep(backoff)
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		zap.L().Info("connected", zap.String("addr", addr))

		// Reset on every (re)connect so a match ending, the player leaving,
		// or the game closing doesn't leave stale players on screen forever.
		teams := map[int]string{}
		scores := map[int]int{}
		arena := ""
		last := MatchState{TargetTeamNum: -1}

		dec := json.NewDecoder(conn)
		for {
			var env envelope
			if err := dec.Decode(&env); err != nil {
				zap.L().Warn("read error", zap.Error(err))
				break
			}

			// MatchEnded carries no roster, so it's republished as the last
			// known state flagged Ended - that flag is what makes a result
			// final for consumers, rather than any single UpdateState tick.
			if env.Event == "MatchEnded" && last.MatchGuid != "" {
				last.Ended = true
				onUpdate(last)
				continue
			}
			if env.Event != "UpdateState" {
				continue
			}

			var data updateStateData
			if err := json.Unmarshal([]byte(env.Data), &data); err != nil {
				continue
			}

			// data.Players is the full current roster for this tick, not an
			// incremental diff, so it's used as-is rather than merged into
			// prior state - that way a player who leaves (or the match
			// ending) is reflected immediately instead of lingering.
			players := map[string]Player{}
			for _, p := range data.Players {
				if p.PrimaryId == "" {
					continue
				}
				players[p.PrimaryId] = p
			}
			for _, t := range data.Game.Teams {
				teams[t.TeamNum] = t.Name
				scores[t.TeamNum] = t.Score
			}
			arena = data.Game.Arena

			target, targetName := -1, ""
			if data.Game.HasTarget {
				target = data.Game.Target.TeamNum
				targetName = data.Game.Target.Name
			}
			winner := ""
			if data.Game.HasWinner {
				winner = data.Game.Winner
			}

			last = MatchState{
				Arena:         arena,
				MatchGuid:     data.MatchGuid,
				Teams:         maps.Clone(teams),
				Scores:        maps.Clone(scores),
				Winner:        winner,
				TargetTeamNum: target,
				TargetName:    targetName,
				TimeSeconds:   data.Game.TimeSeconds,
				Overtime:      data.Game.Overtime,
				Replay:        data.Game.Replay,
				Players:       sortedPlayers(players),
			}
			onUpdate(last)
		}
		conn.Close()
		onUpdate(MatchState{TargetTeamNum: -1})
	}
}

// sortedPlayers flattens the roster into MatchState's stable order: by
// TeamNum, then Name.
func sortedPlayers(players map[string]Player) []Player {
	list := make([]Player, 0, len(players))
	for _, p := range players {
		list = append(list, p)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].TeamNum != list[j].TeamNum {
			return list[i].TeamNum < list[j].TeamNum
		}
		return list[i].Name < list[j].Name
	})
	return list
}
