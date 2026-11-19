package inputrec

import (
	"os"
	"testing"
	"time"
)

// TestLiveSources reads the real devices for a few seconds and logs what
// each reports - a way to check a controller is seen before playing a match
// with recording on. It touches hardware, so it only runs on request:
//
//	LOBBYIQ_LIVE_INPUT=1 go test -run TestLiveSources -v ./internal/inputrec
func TestLiveSources(t *testing.T) {
	if os.Getenv("LOBBYIQ_LIVE_INPUT") == "" {
		t.Skip("set LOBBYIQ_LIVE_INPUT=1 to read real devices")
	}
	sources := openSources(nil)
	defer func() {
		for _, s := range sources {
			s.Close()
		}
	}()
	for _, s := range sources {
		t.Logf("source %s: %s", s.Kind(), s.Name())
	}

	changes := make([]int, len(sources))
	p := startPoller(sources, DefaultPollHz, func(dev int, _ time.Time, st State) {
		changes[dev]++
		if changes[dev] <= 5 {
			t.Logf("%s: %+v", sources[dev].Kind(), st)
		}
	})
	time.Sleep(3 * time.Second)
	p.Stop()
	for i, s := range sources {
		t.Logf("%s (%s): %d changes", s.Kind(), s.Name(), changes[i])
	}
}
