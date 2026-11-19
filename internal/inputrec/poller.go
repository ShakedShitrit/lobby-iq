package inputrec

import (
	"time"
)

// poller reads every source at a fixed rate and reports each state that
// differs from the last one that source reported.
type poller struct {
	stop chan struct{}
	done chan struct{}
}

// startPoller polls sources hz times a second until stopped, calling emit
// from a single goroutine with each changed state, in time order.
//
// A plain ticker is accurate enough here: since Go 1.23 timers on Windows
// use high-resolution waitable timers, so there is no 15.6ms system tick to
// raise with timeBeginPeriod.
func startPoller(sources []Source, hz int, emit func(device int, at time.Time, st State)) *poller {
	p := &poller{stop: make(chan struct{}), done: make(chan struct{})}
	if hz <= 0 {
		hz = DefaultPollHz
	}

	go func() {
		defer close(p.done)
		last := make([]State, len(sources))
		seen := make([]bool, len(sources))

		ticker := time.NewTicker(time.Second / time.Duration(hz))
		defer ticker.Stop()
		for {
			select {
			case <-p.stop:
				return
			case <-ticker.C:
			}
			for i, src := range sources {
				st, ok := src.Poll()
				if !ok || (seen[i] && st.Equal(last[i])) {
					continue
				}
				last[i], seen[i] = st, true
				emit(i, time.Now(), st)
			}
		}
	}()
	return p
}

// Stop ends polling and waits until emit will not be called again.
func (p *poller) Stop() {
	close(p.stop)
	<-p.done
}
