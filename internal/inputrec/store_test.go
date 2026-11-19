package inputrec

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unicode/utf16"
)

// writeReplay writes a file shaped like a saved replay's header, with the
// properties the store reads buried among others.
func writeReplay(t *testing.T, dir, file, guid, name string) {
	t.Helper()
	prop := func(key, typ string, value []byte) []byte {
		b := append(ueString(key), ueString(typ)...)
		b = binary.LittleEndian.AppendUint64(b, uint64(len(value)))
		return append(b, value...)
	}
	str := func(s string) []byte {
		for _, r := range s {
			if r > 127 {
				units := append(utf16.Encode([]rune(s)), 0)
				b := binary.LittleEndian.AppendUint32(nil, uint32(-int32(len(units))))
				for _, u := range units {
					b = binary.LittleEndian.AppendUint16(b, u)
				}
				return b
			}
		}
		return ueString(s)
	}

	header := binary.LittleEndian.AppendUint32(nil, 868) // engine version
	header = append(header, ueString("TAGame.Replay_Soccar_TA")...)
	header = append(header, prop("TeamSize", "IntProperty", []byte{2, 0, 0, 0})...)
	header = append(header, prop("ReplayName", "StrProperty", str(name))...)
	header = append(header, prop("MatchGUID", "StrProperty", str(guid))...)
	header = append(header, prop("MapName", "NameProperty", str("Stadium_P"))...)
	header = append(header, ueString("None")...)

	b := binary.LittleEndian.AppendUint32(nil, uint32(len(header)))
	b = binary.LittleEndian.AppendUint32(b, 0) // CRC, not checked
	b = append(b, header...)
	b = append(b, make([]byte, 64)...) // the replay body
	if err := os.WriteFile(filepath.Join(dir, file), b, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestReadReplayInfo(t *testing.T) {
	dir := t.TempDir()
	writeReplay(t, dir, "A.replay", guid, "air bump")
	writeReplay(t, dir, "B.replay", "OTHER", "גול יפה")

	for _, tc := range []struct{ file, guid, name string }{
		{"A.replay", guid, "air bump"},
		{"B.replay", "OTHER", "גול יפה"},
	} {
		info, err := ReadReplayInfo(filepath.Join(dir, tc.file))
		if err != nil {
			t.Fatal(err)
		}
		if info.MatchGuid != tc.guid || info.Name != tc.name || info.File != tc.file {
			t.Errorf("%s: %+v", tc.file, info)
		}
	}
}

func testRecording(guid string, started time.Time) *Recording {
	return &Recording{Version: FormatVersion, MatchGuid: guid, Arena: "Stadium_P", StartedAt: started, DurationMs: 60000}
}

func TestStoreRotatesOnlyTemporary(t *testing.T) {
	root, replays := t.TempDir(), t.TempDir()
	s := NewStore(root, 2, replays)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	writeReplay(t, replays, "K.replay", "KEPT0000", "keeper")
	if _, kept, err := s.Save(testRecording("KEPT0000", t0)); err != nil || !kept {
		t.Fatalf("recording with a saved replay: kept=%v err=%v", kept, err)
	}
	for i := range 4 {
		if _, kept, err := s.Save(testRecording("TEMP000"+string(rune('1'+i)), t0.Add(time.Duration(i+1)*time.Minute))); err != nil || kept {
			t.Fatalf("temporary recording %d: kept=%v err=%v", i, kept, err)
		}
	}

	entries, _ := s.List()
	var kept, temp int
	for _, e := range entries {
		if e.Kept {
			kept++
		} else {
			temp++
		}
	}
	if kept != 1 || temp != 2 {
		t.Errorf("kept %d, temporary %d; want 1 and 2", kept, temp)
	}
	if path, _ := s.FindByGuid("KEPT0000"); path == "" || !s.IsKept(path) {
		t.Errorf("kept recording at %q", path)
	}
}

// A replay saved after the match - from the post-match screen or match
// history - promotes the recording already in tmp.
func TestStorePromotesWhenReplaySaved(t *testing.T) {
	root, replays := t.TempDir(), t.TempDir()
	s := NewStore(root, 5, replays)
	path, kept, err := s.Save(testRecording(guid, time.Now()))
	if err != nil || kept {
		t.Fatalf("kept=%v err=%v", kept, err)
	}
	if got := s.Promote(); len(got) != 0 {
		t.Fatalf("promoted %v with no replay saved", got)
	}

	writeReplay(t, replays, "R.replay", guid, "air bump")
	got := s.Promote()
	if len(got) != 1 {
		t.Fatalf("promoted %v", got)
	}
	for newPath, info := range got {
		if !s.IsKept(newPath) || info.Name != "air bump" {
			t.Errorf("promoted to %s for %+v", newPath, info)
		}
		rec, err := Load(newPath)
		if err != nil || rec.ReplayFile != "R.replay" || rec.ReplayName != "air bump" {
			t.Errorf("kept recording = %+v, %v", rec, err)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("temporary copy still there: %v", err)
	}
}

func TestStoreMigratesOldLayout(t *testing.T) {
	root := t.TempDir()
	old, err := save(root, testRecording(guid, time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	s := NewStore(root, 5, "")
	s.Migrate()
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("old recording not moved")
	}
	if path, _ := s.FindByGuid(guid); filepath.Dir(path) != s.tmpDir() {
		t.Errorf("found at %q, want in tmp", path)
	}
}
