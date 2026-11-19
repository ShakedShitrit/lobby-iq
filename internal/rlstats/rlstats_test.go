package rlstats

import (
	"encoding/json"
	"net"
	"os"
	"testing"
	"time"
)

// capturePath is a recording of a real Stats API session, kept at the repo
// root. It's a dev artifact rather than a fixture, so this test skips when
// it's absent.
const capturePath = "../../capture.json"

// TestWatchParsesCapturedSession replays a real recording through Watch and
// checks the fields the Discord presence depends on actually survive the
// wire format - the capture is the only ground truth we have for them.
func TestWatchParsesCapturedSession(t *testing.T) {
	capture, err := os.ReadFile(capturePath)
	if err != nil {
		t.Skipf("no capture to replay: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = conn.Write(capture)
		// Hold the connection open: closing it makes Watch publish an empty
		// state, which would race with the assertions below.
		time.Sleep(5 * time.Second)
	}()

	states := make(chan MatchState, 4096)
	go Watch(ln.Addr().String(), func(s MatchState) {
		select {
		case states <- s:
		default:
		}
	})

	var last MatchState
	deadline := time.After(10 * time.Second)
	for {
		done := false
		select {
		case s := <-states:
			if s.MatchGuid != "" {
				last = s
			}
			if last.TargetName != "" && last.TimeSeconds > 0 {
				done = true
			}
		case <-deadline:
			t.Fatalf("timed out; last state = %+v", last)
		}
		if done {
			break
		}
	}

	if last.TargetName != "shkd ." {
		t.Errorf("TargetName = %q, want %q", last.TargetName, "shkd .")
	}
	if last.TargetTeamNum != 0 {
		t.Errorf("TargetTeamNum = %d, want 0", last.TargetTeamNum)
	}
	if last.Teams[0] != "Blue" || last.Teams[1] != "Orange" {
		t.Errorf("Teams = %v, want Blue/Orange", last.Teams)
	}
	if got := PrettyArena(last.Arena); got != "Blackout" {
		t.Errorf("PrettyArena(%q) = %q, want %q", last.Arena, got, "Blackout")
	}
	if len(last.Players) == 0 {
		t.Fatal("no players parsed")
	}
	if last.Players[0].Name != "shkd ." {
		t.Errorf("player name = %q, want %q", last.Players[0].Name, "shkd .")
	}
}

// TestWatchEventsDeliversRawEvents checks the events MatchState has no room
// for still reach onEvent, unwrapped from the wire format's nested string.
func TestWatchEventsDeliversRawEvents(t *testing.T) {
	capture, err := os.ReadFile(capturePath)
	if err != nil {
		t.Skipf("no capture to replay: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = conn.Write(capture)
		time.Sleep(5 * time.Second)
	}()

	events := make(chan Event, 4096)
	go WatchEvents(ln.Addr().String(), func(MatchState) {}, func(e Event) {
		select {
		case events <- e:
		default:
		}
	})

	want := map[string]bool{"GoalScored": false, "GoalReplayStart": false}
	deadline := time.After(10 * time.Second)
	for !want["GoalScored"] || !want["GoalReplayStart"] {
		select {
		case e := <-events:
			if _, ok := want[e.Name]; !ok {
				continue
			}
			want[e.Name] = true
			var data struct{ MatchGuid string }
			if err := json.Unmarshal(e.Data, &data); err != nil || data.MatchGuid == "" {
				t.Errorf("%s data not unwrapped JSON: %s (%v)", e.Name, e.Data, err)
			}
			if e.At.IsZero() {
				t.Errorf("%s has no arrival time", e.Name)
			}
		case <-deadline:
			t.Fatalf("timed out; seen = %v", want)
		}
	}
}

// A saved replay comes through under the original match's ID; it has to be
// told apart from that match being played, by its replay timing or by
// ReplayCreated.
func TestWatchFlagsHistoryReplay(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	tick := func(guid string, replay bool) string {
		timing := ""
		if replay {
			timing = `,\"Frame\":300,\"Elapsed\":10.5`
		}
		return `{"Event":"UpdateState","Data":"{\"MatchGuid\":\"` + guid + `\",\"Players\":[],\"Game\":{\"Teams\":[],\"TimeSeconds\":300` + timing + `}}"}`
	}
	stream := tick("LIVE", false) +
		tick("LIVE", true) + // the live match's own goal replay
		`{"Event":"MatchDestroyed","Data":"{\"MatchGuid\":\"LIVE\"}"}` +
		tick("OLD", true) +
		`{"Event":"ReplayCreated","Data":"{\"MatchGuid\":\"OTHER\"}"}` +
		tick("OTHER", false)

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = conn.Write([]byte(stream))
		time.Sleep(5 * time.Second)
	}()

	states := make(chan MatchState, 16)
	go Watch(ln.Addr().String(), func(s MatchState) { states <- s })

	want := []struct {
		guid    string
		history bool
		timed   bool
	}{
		{"LIVE", false, false},
		{"LIVE", false, true},
		{"OLD", true, true},
		{"OTHER", true, false},
	}
	for _, w := range want {
		select {
		case s := <-states:
			if s.MatchGuid != w.guid || s.HistoryReplay != w.history || s.HasReplayTime != w.timed {
				t.Errorf("got %s history=%v timed=%v, want %+v", s.MatchGuid, s.HistoryReplay, s.HasReplayTime, w)
			}
			if w.timed && (s.ReplayFrame != 300 || s.ReplayElapsed != 10.5) {
				t.Errorf("replay timing = %d, %v", s.ReplayFrame, s.ReplayElapsed)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timed out")
		}
	}
}
