// Package selfid works out which player in the lobby is you.
//
// It exists because the account LobbyIQ signs in with is not necessarily the
// account playing. Rocket League's backend allows one live session per account
// and evicts the older one, so querying it as the account in the game
// disconnects the game; a separate account avoids that, and then "my rank" can
// no longer be inferred from who is signed in.
//
// The Stats API does not flag the local player. It reports the roster and, per
// tick, which player the camera is following - and outside of goal replays,
// the camera follows you. So the local player is the one followed for most of
// a match, which is the same reasoning internal/session already uses to decide
// which team is yours, applied per player instead of per team.
//
// Replay ticks are excluded rather than outvoted: during a goal replay the
// camera follows the scorer, who is often an opponent, and that is the only
// systematic way the signal goes wrong.
//
// Once resolved the answer is written to disk, so it is known from startup on
// every later run and the first match is the only one that has to work it out.
package selfid

import (
	"encoding/json"
	"os"
	"strings"
	"sync"

	"go.uber.org/zap"

	"github.com/ShakedShitrit/lobby-iq/internal/rlstats"
)

// FileName is where a resolved identity is remembered, alongside config.yaml
// and players.json.
const FileName = "self.json"

// minTicks is how much of a match must be observed before believing the
// result. UpdateState arrives several times a second, so this is a few seconds
// of play - enough that a single stray tick at kickoff cannot decide it.
const minTicks = 30

// minShare is the fraction of non-replay ticks the winner must hold. In a real
// match the local player is followed for nearly all of them; anything close to
// a tie means something unusual is going on - spectating, or a camera that
// spent the match on someone else - and is not worth guessing from.
const minShare = 0.6

// Store resolves and remembers the local player's PrimaryId.
//
// The zero value is not usable; call Open.
type Store struct {
	path string

	mu sync.Mutex
	// id is the resolved PrimaryId, empty until known.
	id string
	// explicit records that id came from configuration, which is a statement
	// of fact from the user and is never revised by observation.
	explicit bool

	// guid is the match currently being counted, so a new match starts fresh.
	guid string
	// ticks counts camera focus per player name, for this match only.
	ticks map[string]int
	// roster maps display name to every PrimaryId seen under it this match.
	// It is a list because the Stats API reports the camera's target by name
	// only, so two players sharing a display name make that name useless as an
	// identity - a case that has to be detectable rather than silently
	// resolving to whichever was seen last.
	roster map[string][]string

	onResolve func(string)
}

type persisted struct {
	PrimaryID string `json:"primary_id"`
}

// Open loads a previously resolved identity, if there is one.
//
// configured, when set, wins outright and is not persisted - it came from the
// config file and belongs there. A read failure is logged and treated as
// "not known yet" rather than failing startup: the cost is one match of
// re-detection.
func Open(path, configured string) *Store {
	s := &Store{
		path:   path,
		ticks:  map[string]int{},
		roster: map[string][]string{},
	}

	if configured = strings.TrimSpace(configured); configured != "" {
		s.id, s.explicit = configured, true
		zap.L().Debug("selfid: using the configured player ID",
			zap.String("player", configured))
		return s
	}

	b, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			zap.L().Warn("selfid: reading stored identity, will detect again", zap.Error(err))
		}
		return s
	}
	var p persisted
	if err := json.Unmarshal(b, &p); err != nil {
		zap.L().Warn("selfid: parsing stored identity, will detect again", zap.Error(err))
		return s
	}
	s.id = strings.TrimSpace(p.PrimaryID)
	if s.id != "" {
		zap.L().Debug("selfid: recalled who you are", zap.String("player", s.id))
	}
	return s
}

// ID returns the local player's PrimaryId. ok is false while it is still
// unknown, which a caller should treat as "wait" rather than as an answer.
func (s *Store) ID() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id, s.id != ""
}

// OnResolve registers a callback for when the identity is worked out, so a
// rank source that started up pointed at the wrong player can be corrected
// without waiting for a restart.
//
// It fires once per resolution: normally that is once ever, but Forget makes
// the next match resolve again.
func (s *Store) OnResolve(fn func(string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onResolve = fn
}

// Forget discards a detected identity so the next match works it out again.
//
// This is what a change of Epic account needs. The identity is detected from
// the game rather than from the sign-in, so re-linking does not invalidate it
// by itself - but the two usually change together, and a remembered player who
// is no longer the one at the keyboard would put someone else's rank on the
// card indefinitely. Detecting again costs one match and cannot be wrong for
// longer than that.
//
// A configured identity is left alone: it is a statement from the user, and
// nothing here has standing to overrule it.
func (s *Store) Forget() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.explicit {
		return
	}
	s.id = ""
	s.guid = ""
	s.ticks = map[string]int{}
	s.roster = map[string][]string{}

	if s.path == "" {
		return
	}
	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		// The in-memory identity is already cleared, so this run re-detects
		// regardless; the stale file would only be read by a later run.
		zap.L().Warn("selfid: could not delete stored identity", zap.Error(err))
	}
}

// Observe feeds a match state in. Safe to call on every tick, and cheap once
// the identity is known.
func (s *Store) Observe(state rlstats.MatchState) {
	s.mu.Lock()

	if s.id != "" {
		s.mu.Unlock()
		return
	}
	if state.MatchGuid == "" {
		s.mu.Unlock()
		return
	}

	if state.MatchGuid != s.guid {
		s.guid = state.MatchGuid
		s.ticks = map[string]int{}
		s.roster = map[string][]string{}
	}
	for _, p := range state.Players {
		name := strings.TrimSpace(p.Name)
		if name == "" || p.PrimaryId == "" {
			continue
		}
		if !contains(s.roster[name], p.PrimaryId) {
			s.roster[name] = append(s.roster[name], p.PrimaryId)
		}
	}

	// The camera follows the scorer during a goal replay, so those ticks say
	// nothing about who is playing.
	if !state.Replay && state.TargetName != "" {
		s.ticks[strings.TrimSpace(state.TargetName)]++
	}

	id, ok := s.resolveLocked()
	if !ok {
		s.mu.Unlock()
		return
	}
	s.id = id
	fn := s.onResolve
	s.writeLocked()
	s.mu.Unlock()

	zap.L().Info("selfid: worked out which player is you", zap.String("player", id))
	// Outside the lock: the callback re-enters the rank source, and holding
	// this lock across it would tie two subsystems' locks together.
	if fn != nil {
		fn(id)
	}
}

// resolveLocked reports the local player if the evidence is good enough yet.
func (s *Store) resolveLocked() (string, bool) {
	total := 0
	bestName, bestTicks := "", 0
	for name, n := range s.ticks {
		total += n
		if n > bestTicks {
			bestName, bestTicks = name, n
		}
	}
	if total < minTicks || bestName == "" {
		return "", false
	}
	if float64(bestTicks)/float64(total) < minShare {
		return "", false
	}
	// A followed player who is not in the roster cannot be identified, and a
	// name worn by two players is not an identity - better to stay unresolved
	// for a match than to publish someone else's rank as yours.
	ids := s.roster[bestName]
	if len(ids) != 1 || ids[0] == "" {
		return "", false
	}
	return ids[0], true
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// writeLocked persists the resolved identity. A write failure costs one match
// of re-detection on the next run, so it is logged rather than propagated.
func (s *Store) writeLocked() {
	if s.path == "" || s.explicit {
		return
	}
	b, err := json.MarshalIndent(persisted{PrimaryID: s.id}, "", "  ")
	if err != nil {
		zap.L().Warn("selfid: marshaling identity", zap.Error(err))
		return
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		zap.L().Warn("selfid: writing identity", zap.Error(err))
		return
	}
	if err := os.Rename(tmp, s.path); err != nil {
		zap.L().Warn("selfid: saving identity", zap.Error(err))
	}
}
