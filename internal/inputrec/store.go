package inputrec

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Store is where recordings live. Every match is first saved to tmp/,
// which only keeps the newest few; a recording moves to saved/, kept for
// good, once a replay of its match is saved in Rocket League - the game's
// own way of saying a match is worth keeping - or when kept by hand.
type Store struct {
	dir        string
	keep       int
	replaysDir string

	mu sync.Mutex
	// replays indexes the game's saved replays by match ID. scanned holds
	// each replay file's modification time when it was read, so a scan only
	// opens files that are new or changed.
	replays map[string]ReplayInfo
	scanned map[string]time.Time
}

// Entry is one recording in the store.
type Entry struct {
	Path string
	// Kept is true for recordings in saved/, which rotation never touches.
	Kept bool
}

// NewStore returns the store rooted at dir, keeping the newest keep
// temporary recordings (0 or less keeps them all). replaysDir is where the
// game saves replays; "" turns off keeping recordings by replay.
func NewStore(dir string, keep int, replaysDir string) *Store {
	return &Store{
		dir:        dir,
		keep:       keep,
		replaysDir: replaysDir,
		replays:    map[string]ReplayInfo{},
		scanned:    map[string]time.Time{},
	}
}

// Dir is the store's root folder.
func (s *Store) Dir() string { return s.dir }

// Limit is how many temporary recordings are kept.
func (s *Store) Limit() int { return s.keep }

func (s *Store) tmpDir() string   { return filepath.Join(s.dir, "tmp") }
func (s *Store) savedDir() string { return filepath.Join(s.dir, "saved") }

// Migrate moves recordings left in the root folder by earlier versions, which
// had no tmp/ and saved/, into tmp/ - from where any with a saved replay are
// promoted like the rest.
func (s *Store) Migrate() {
	old, err := listDir(s.dir)
	if err != nil || len(old) == 0 {
		return
	}
	if err := os.MkdirAll(s.tmpDir(), 0755); err != nil {
		zap.L().Warn("making temporary recordings folder", zap.Error(err))
		return
	}
	for _, p := range old {
		if err := os.Rename(p, filepath.Join(s.tmpDir(), filepath.Base(p))); err != nil {
			zap.L().Warn("moving recording into tmp", zap.String("path", p), zap.Error(err))
		}
	}
	zap.L().Info("moved older recordings into tmp", zap.Int("count", len(old)))
}

// List returns every recording, newest first, kept and temporary together.
func (s *Store) List() ([]Entry, error) {
	var entries []Entry
	for _, d := range []struct {
		dir  string
		kept bool
	}{{s.savedDir(), true}, {s.tmpDir(), false}, {s.dir, false}} {
		paths, err := listDir(d.dir)
		if err != nil {
			return nil, err
		}
		for _, p := range paths {
			entries = append(entries, Entry{Path: p, Kept: d.kept})
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return filepath.Base(entries[i].Path) > filepath.Base(entries[j].Path)
	})
	return entries, nil
}

// FindByGuid returns the recording of match guid, or "" if there is none.
// Only files whose name carries the guid's prefix are opened.
func (s *Store) FindByGuid(guid string) (string, error) {
	prefix := unsafeName.ReplaceAllString(guid, "")
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	if prefix == "" {
		return "", nil
	}
	entries, err := s.List()
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if !strings.Contains(filepath.Base(e.Path), "_"+prefix) {
			continue
		}
		if rec, err := Load(e.Path); err == nil && rec.MatchGuid == guid {
			return e.Path, nil
		}
	}
	return "", nil
}

// IsKept reports whether path is a kept recording.
func (s *Store) IsKept(path string) bool {
	return filepath.Clean(filepath.Dir(path)) == filepath.Clean(s.savedDir())
}

// Save writes a finished recording: straight to saved/ if a replay of the
// match has already been saved, otherwise to tmp/, after which the oldest
// temporary recordings beyond the limit are deleted.
func (s *Store) Save(rec *Recording) (path string, kept bool, err error) {
	s.Scan()
	s.mu.Lock()
	info, hasReplay := s.replays[rec.MatchGuid]
	s.mu.Unlock()

	if hasReplay {
		rec.ReplayFile, rec.ReplayName = info.File, info.Name
		path, err = save(s.savedDir(), rec)
		return path, true, err
	}
	path, err = save(s.tmpDir(), rec)
	if err != nil {
		return "", false, err
	}
	if err := rotate(s.tmpDir(), s.keep); err != nil {
		zap.L().Warn("rotating temporary recordings", zap.Error(err))
	}
	return path, false, nil
}

// Keep moves a temporary recording to saved/, noting the replay it was kept
// for if there is one. It returns the recording's new path; a recording
// already kept is left where it is.
func (s *Store) Keep(path string, replay *ReplayInfo) (string, error) {
	if s.IsKept(path) {
		return path, nil
	}
	rec, err := Load(path)
	if err != nil {
		return "", err
	}
	if replay != nil {
		rec.ReplayFile, rec.ReplayName = replay.File, replay.Name
	}
	newPath, err := save(s.savedDir(), rec)
	if err != nil {
		return "", err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return newPath, fmt.Errorf("kept, but removing the temporary copy: %w", err)
	}
	return newPath, nil
}

// Scan reads any replays saved since the last scan and returns them.
func (s *Store) Scan() []ReplayInfo {
	if s.replaysDir == "" {
		return nil
	}
	entries, err := os.ReadDir(s.replaysDir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			zap.L().Debug("listing replays", zap.String("dir", s.replaysDir), zap.Error(err))
		}
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	var found []ReplayInfo
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ReplayExt) {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		if seen, ok := s.scanned[e.Name()]; ok && seen.Equal(fi.ModTime()) {
			continue
		}
		info, err := ReadReplayInfo(filepath.Join(s.replaysDir, e.Name()))
		if err != nil {
			// Possibly still being written; it'll be read again once its
			// modification time settles.
			zap.L().Debug("reading replay header", zap.String("file", e.Name()), zap.Error(err))
			continue
		}
		s.scanned[e.Name()] = fi.ModTime()
		s.replays[info.MatchGuid] = info
		found = append(found, info)
	}
	return found
}

// Promote keeps every temporary recording whose match has a saved replay,
// returning the new paths with the replays they were kept for.
func (s *Store) Promote() map[string]ReplayInfo {
	s.Scan()
	tmp, err := listDir(s.tmpDir())
	if err != nil || len(tmp) == 0 {
		return nil
	}
	s.mu.Lock()
	replays := make(map[string]ReplayInfo, len(s.replays))
	for k, v := range s.replays {
		replays[k] = v
	}
	s.mu.Unlock()

	kept := map[string]ReplayInfo{}
	for _, p := range tmp {
		rec, err := Load(p)
		if err != nil {
			continue
		}
		info, ok := replays[rec.MatchGuid]
		if !ok {
			continue
		}
		newPath, err := s.Keep(p, &info)
		if err != nil {
			zap.L().Warn("keeping recording for saved replay", zap.String("path", p), zap.Error(err))
			continue
		}
		zap.L().Info("kept recording: its replay was saved",
			zap.String("recording", newPath), zap.String("replay", info.File), zap.String("name", info.Name))
		kept[newPath] = info
	}
	return kept
}

// Watch promotes recordings as their replays are saved, checking every
// interval until stop is closed. onKept, if set, hears of each one.
func (s *Store) Watch(interval time.Duration, stop <-chan struct{}, onKept func(path string, replay ReplayInfo)) {
	if s.replaysDir == "" {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		for path, info := range s.Promote() {
			if onKept != nil {
				onKept(path, info)
			}
		}
		select {
		case <-stop:
			return
		case <-t.C:
		}
	}
}

// listDir returns the recordings directly in dir, newest first.
func listDir(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), Ext) {
			names = append(names, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	paths := make([]string, len(names))
	for i, n := range names {
		paths[i] = filepath.Join(dir, n)
	}
	return paths, nil
}
