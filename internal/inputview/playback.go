// Package inputview plays an input recording back as an animated
// controller, for watching alongside the match's saved replay.
package inputview

import (
	"sort"
	"time"

	"github.com/ShakedShitrit/lobby-iq/internal/inputrec"
)

// timeline is one device's samples from a recording, for looking up what
// was held at any moment.
type timeline struct {
	samples []inputrec.Sample
}

func newTimeline(rec *inputrec.Recording, device int) *timeline {
	t := &timeline{}
	for _, s := range rec.Samples {
		if s.Device == device {
			t.samples = append(t.samples, s)
		}
	}
	sort.SliceStable(t.samples, func(i, j int) bool { return t.samples[i].AtMs < t.samples[j].AtMs })
	return t
}

// stateAt returns the device's state at atMs: that of the last sample at or
// before it. Samples are only taken on change, so that is what was held.
func (t *timeline) stateAt(atMs int64) inputrec.State {
	i := sort.Search(len(t.samples), func(i int) bool { return t.samples[i].AtMs > atMs })
	if i == 0 {
		return inputrec.State{}
	}
	return t.samples[i-1].State
}

// bestDevice picks the device to show first: the one with the most
// samples, which is the one that was actually played with.
func bestDevice(rec *inputrec.Recording) int {
	counts := map[int]int{}
	for _, s := range rec.Samples {
		counts[s.Device]++
	}
	best, most := 0, -1
	for _, d := range rec.Devices {
		if counts[d.ID] > most {
			best, most = d.ID, counts[d.ID]
		}
	}
	return best
}

// clock is a playback position within one segment that runs at a chosen
// speed from the moment it is synced. It never reads the time itself, so it
// can be tested with made-up times.
type clock struct {
	durMs   int64
	baseMs  float64 // position when started, or the held position when paused
	started time.Time
	speed   float64
	playing bool
}

func newClock(durMs int64) clock { return clock{durMs: durMs, speed: 1} }

// at returns the position at now, in ms from the segment's anchor, and
// whether the segment has run out.
func (c *clock) at(now time.Time) (int64, bool) {
	pos := c.baseMs
	if c.playing {
		pos += float64(now.Sub(c.started).Microseconds()) / 1000 * c.speed
	}
	if pos >= float64(c.durMs) {
		return c.durMs, true
	}
	return int64(max(pos, 0)), false
}

// sync starts playing from the segment's anchor - the moment the replay
// shows "GO".
func (c *clock) sync(now time.Time) {
	c.baseMs, c.started, c.playing = 0, now, true
}

func (c *clock) pause(now time.Time) {
	pos, _ := c.at(now)
	c.baseMs, c.playing = float64(pos), false
}

func (c *clock) resume(now time.Time) {
	c.started, c.playing = now, true
}

func (c *clock) seek(now time.Time, ms int64) {
	c.baseMs, c.started = float64(min(max(ms, 0), c.durMs)), now
}

// nudge moves the position by ms without disturbing playback, for fine
// alignment once synced.
func (c *clock) nudge(ms int64) { c.baseMs += float64(ms) }

func (c *clock) setSpeed(now time.Time, speed float64) {
	pos, _ := c.at(now)
	c.baseMs, c.started, c.speed = float64(pos), now, speed
}
