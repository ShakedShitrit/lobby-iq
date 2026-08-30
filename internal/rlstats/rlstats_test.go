package rlstats

import (
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
