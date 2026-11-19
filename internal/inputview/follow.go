package inputview

import (
	"encoding/json"
	"math"
	"sort"
	"time"

	"github.com/ShakedShitrit/lobby-iq/internal/inputrec"
	"github.com/ShakedShitrit/lobby-iq/internal/rlstats"
)

// The follower works out where in a recording a saved replay is, from what
// the game reports while the replay plays.
//
// The replay's own clock comes from UpdateState's Elapsed, which the game
// sends only while a replay is playing - but only as often as PacketSendRate
// (2 a second by default), so between updates it is extrapolated. Tying that
// clock to the recording takes anchors: moments seen both live and in the
// replay. Events fire on the exact tick they happen in both, so each one
// that can be recognised pins the two clocks together:
//
//   - RoundStarted is the kickoff whose game clock reads the same.
//   - GoalScored is the goal with the same GoalSpeed.
//   - BallHit is the touch at the same spot on the field.
//
// Between anchors the recording is assumed to run in step with the replay,
// with goal replays and pauses cut out (see inputrec.Cuts).
type follower struct {
	rec  *inputrec.Recording
	cuts []inputrec.Cut

	goals []goalMark
	hits  []hitMark

	// The replay's clock, in ms: replayMs at replayAt, advancing at rate.
	haveReplay bool
	replayMs   float64
	replayAt   time.Time
	rate       float64
	lastUpdate time.Time

	clock     int
	overtime  bool
	haveClock bool

	anchors []anchor
}

type anchor struct {
	replayMs float64
	liveMs   int64
	kind     string
}

type goalMark struct {
	atMs  int64
	speed float64
}

type hitMark struct {
	atMs    int64
	x, y, z float64
}

const (
	// maxExtrapolate is how far past the last update the replay clock is
	// carried forward. Beyond it the replay is taken to have stopped: an
	// update every 500ms is normal, so a full second without one means
	// paused, or gone.
	maxExtrapolate = 1000 * time.Millisecond
	// staleAfter is how long without updates before the replay is taken to
	// have been closed.
	staleAfter = 3 * time.Second
	// hitTolerance is how far apart, in Unreal units, two ball hits can be
	// and still be the same one.
	hitTolerance = 5.0
	// hitWindowMs is how far from where it's expected a ball hit may be
	// matched, once the clocks are tied together at all.
	hitWindowMs = 4000
)

func newFollower(rec *inputrec.Recording) *follower {
	f := &follower{rec: rec, cuts: rec.Cuts(), rate: 1}
	for _, e := range rec.Events {
		switch e.Name {
		case "GoalScored":
			var d struct{ GoalSpeed float64 }
			if json.Unmarshal(e.Data, &d) == nil && d.GoalSpeed > 0 {
				f.goals = append(f.goals, goalMark{e.AtMs, d.GoalSpeed})
			}
		case "BallHit":
			if x, y, z, ok := hitLocation(e.Data); ok {
				f.hits = append(f.hits, hitMark{e.AtMs, x, y, z})
			}
		}
	}
	return f
}

func hitLocation(data json.RawMessage) (x, y, z float64, ok bool) {
	var d struct {
		Ball struct {
			Location *struct{ X, Y, Z float64 }
		}
	}
	if json.Unmarshal(data, &d) != nil || d.Ball.Location == nil {
		return 0, 0, 0, false
	}
	l := d.Ball.Location
	return l.X, l.Y, l.Z, true
}

// onEvent takes every raw Stats API event.
func (f *follower) onEvent(e rlstats.Event) {
	var d struct {
		MatchGuid string
		Game      *struct {
			TimeSeconds int
			Overtime    bool     `json:"bOvertime"`
			Elapsed     *float64 `json:"Elapsed"`
		}
		GoalSpeed float64
	}
	if json.Unmarshal(e.Data, &d) != nil {
		return
	}
	if d.MatchGuid != "" && d.MatchGuid != f.rec.MatchGuid {
		return
	}

	switch e.Name {
	case "UpdateState":
		if d.Game == nil {
			return
		}
		f.clock, f.overtime, f.haveClock = d.Game.TimeSeconds, d.Game.Overtime, true
		if d.Game.Elapsed != nil {
			f.observeElapsed(*d.Game.Elapsed*1000, e.At)
		}
	case "RoundStarted":
		f.anchorKickoff(e.At)
	case "GoalScored":
		if d.GoalSpeed > 0 {
			f.anchorGoal(d.GoalSpeed, e.At)
		}
	case "BallHit":
		if x, y, z, ok := hitLocation(e.Data); ok {
			f.anchorHit(x, y, z, e.At)
		}
	}
}

// observeElapsed takes one reading of the replay's clock and updates the
// estimate of how fast it is running - which is how a paused or slowed
// replay shows up.
func (f *follower) observeElapsed(ms float64, at time.Time) {
	if f.haveReplay {
		dt := float64(at.Sub(f.replayAt).Microseconds()) / 1000
		de := ms - f.replayMs
		switch {
		case dt < 50:
			// Too close together to measure a rate from.
		case de == 0:
			f.rate = 0
		case de < 0 || de/dt > 8:
			// A seek. The rate it was playing at still stands.
		default:
			f.rate = snapRate(de / dt)
		}
	}
	f.haveReplay, f.replayMs, f.replayAt, f.lastUpdate = true, ms, at, at
}

// snapRate rounds a measured rate to the replay speed it almost certainly
// is, since measuring over half a second is noisy.
func snapRate(r float64) float64 {
	for _, s := range []float64{0.05, 0.1, 0.25, 0.5, 1, 1.5, 2, 4} {
		if math.Abs(r-s) <= s*0.08 {
			return s
		}
	}
	return r
}

// replayAtTime is the replay's clock at now.
func (f *follower) replayAtTime(now time.Time) (float64, bool) {
	if !f.haveReplay {
		return 0, false
	}
	dt := min(now.Sub(f.replayAt), maxExtrapolate)
	return f.replayMs + float64(dt.Microseconds())/1000*f.rate, true
}

// active reports whether a replay of this recording is playing right now.
func (f *follower) active(now time.Time) bool {
	return f.haveReplay && now.Sub(f.lastUpdate) < staleAfter
}

// liveAt is the recording time matching the replay at now, once at least
// one anchor ties the two together.
func (f *follower) liveAt(now time.Time) (int64, bool) {
	e, ok := f.replayAtTime(now)
	if !ok {
		return 0, false
	}
	return f.liveFor(e)
}

func (f *follower) liveFor(replayMs float64) (int64, bool) {
	if len(f.anchors) == 0 {
		return 0, false
	}
	// The latest anchor at or before this point: its offset is the one in
	// force, until the next anchor corrects it.
	i := sort.Search(len(f.anchors), func(i int) bool { return f.anchors[i].replayMs > replayMs }) - 1
	if i < 0 {
		i = 0
	}
	a := f.anchors[i]
	cont := inputrec.ContinuousMs(f.cuts, a.liveMs) + int64(math.Round(replayMs-a.replayMs))
	return inputrec.LiveMs(f.cuts, cont), true
}

func (f *follower) addAnchor(at time.Time, liveMs int64, kind string) {
	e, ok := f.replayAtTime(at)
	if !ok {
		return
	}
	// The same moment seen twice - the replay seeked back over it - replaces
	// the old reading rather than piling up beside it.
	kept := f.anchors[:0]
	for _, a := range f.anchors {
		if a.liveMs != liveMs {
			kept = append(kept, a)
		}
	}
	f.anchors = append(kept, anchor{replayMs: e, liveMs: liveMs, kind: kind})
	sort.Slice(f.anchors, func(i, j int) bool { return f.anchors[i].replayMs < f.anchors[j].replayMs })
}

func (f *follower) anchorKickoff(at time.Time) {
	var best *inputrec.Segment
	bestDist := math.Inf(1)
	e, _ := f.replayAtTime(at)
	for i := range f.rec.Segments {
		s := &f.rec.Segments[i]
		if s.Anchor != "round_started" && s.Anchor != "countdown" {
			continue
		}
		if f.haveClock && (s.ClockAtAnchor != f.clock || s.Overtime != f.overtime) {
			continue
		}
		dist := float64(s.Index) // with nothing better, the earliest
		if live, ok := f.liveFor(e); ok {
			dist = math.Abs(float64(live - s.AnchorMs))
		}
		if dist < bestDist {
			best, bestDist = s, dist
		}
	}
	if best != nil {
		f.addAnchor(at, best.AnchorMs, "kickoff")
	}
}

func (f *follower) anchorGoal(speed float64, at time.Time) {
	for _, g := range f.goals {
		if math.Abs(g.speed-speed) < 0.01 {
			f.addAnchor(at, g.atMs, "goal")
			return
		}
	}
}

func (f *follower) anchorHit(x, y, z float64, at time.Time) {
	e, _ := f.replayAtTime(at)
	expected, tied := f.liveFor(e)
	var best *hitMark
	bestDist := math.Inf(1)
	candidates := 0
	for i := range f.hits {
		h := &f.hits[i]
		d := math.Sqrt((h.x-x)*(h.x-x) + (h.y-y)*(h.y-y) + (h.z-z)*(h.z-z))
		if d > hitTolerance {
			continue
		}
		if tied && math.Abs(float64(h.atMs-expected)) > hitWindowMs {
			continue
		}
		candidates++
		// Ties go to the touch nearest where it's expected.
		if tied {
			d += math.Abs(float64(h.atMs-expected)) / 1000
		}
		if d < bestDist {
			best, bestDist = h, d
		}
	}
	// Untied, a touch is only trusted if nothing else matches: every
	// kickoff's first touch is at centre field, so location alone can't say
	// which kickoff it was.
	if best != nil && (tied || candidates == 1) {
		f.addAnchor(at, best.atMs, "ball hit")
	}
}
