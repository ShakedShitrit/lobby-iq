// Package history persists how many matches you've played alongside each
// player (teammate or opponent) across sessions, keyed by their PrimaryId.
package history

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/ShakedShitrit/lobby-iq/internal/rlstats"
)

// FileName is the JSON file LobbyIQ stores match history in, alongside
// config.yaml and lobby-iq.log.
const FileName = "players.json"

type Record struct {
	Name      string    `json:"name"`
	Games     int       `json:"games"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
}

// Store tracks, per player PrimaryId, how many distinct matches they've
// shared with the local player. It's safe for concurrent use.
type Store struct {
	mu   sync.Mutex
	path string
	data map[string]*Record

	// lastMatchGuid/countedThisMatch dedupe the many UpdateState ticks that
	// occur within a single match, so each match increments a player's
	// count exactly once.
	lastMatchGuid    string
	countedThisMatch map[string]bool
}

// Open loads path if it exists. Any read or parse failure is logged and
// treated as an empty history rather than failing startup - a corrupt or
// missing history file shouldn't block using the rest of the app.
func Open(path string) *Store {
	s := &Store{path: path, data: map[string]*Record{}}

	b, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			zap.L().Warn("reading history file, starting fresh", zap.Error(err))
		}
		return s
	}
	if err := json.Unmarshal(b, &s.data); err != nil {
		zap.L().Warn("parsing history file, starting fresh", zap.Error(err))
		s.data = map[string]*Record{}
	}
	return s
}

// Observe records that players were seen together in matchGuid, incrementing
// each player's game count the first time this matchGuid is observed.
func (s *Store) Observe(matchGuid string, players []rlstats.Player) {
	if matchGuid == "" || len(players) == 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if matchGuid != s.lastMatchGuid {
		s.lastMatchGuid = matchGuid
		s.countedThisMatch = map[string]bool{}
	}

	changed := false
	now := time.Now()
	for _, p := range players {
		if p.PrimaryId == "" || s.countedThisMatch[p.PrimaryId] {
			continue
		}
		s.countedThisMatch[p.PrimaryId] = true

		rec, ok := s.data[p.PrimaryId]
		if !ok {
			rec = &Record{FirstSeen: now}
			s.data[p.PrimaryId] = rec
		}
		rec.Name = strings.TrimSpace(p.Name)
		rec.Games++
		rec.LastSeen = now
		changed = true
	}

	if changed {
		s.writeLocked()
	}
}

// Games returns how many matches primaryId has been seen in, including the
// current one if already observed. Zero if never seen.
func (s *Store) Games(primaryId string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rec, ok := s.data[primaryId]; ok {
		return rec.Games
	}
	return 0
}

func (s *Store) writeLocked() {
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		zap.L().Warn("marshaling history", zap.Error(err))
		return
	}

	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		zap.L().Warn("writing history file", zap.Error(err))
		return
	}
	if err := os.Rename(tmp, s.path); err != nil {
		zap.L().Warn("saving history file", zap.Error(err))
	}
}
