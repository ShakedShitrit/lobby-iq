package inputview

import (
	"encoding/json"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/ShakedShitrit/lobby-iq/internal/inputrec"
	"github.com/ShakedShitrit/lobby-iq/internal/rlstats"
)

const followGuid = "7812382611F1E3A91F44A38BCE788AEF"

// followRecording is shaped on a real recording: two kickoffs, a goal with a
// quickly skipped goal replay between them.
func followRecording() *inputrec.Recording {
	ev := func(at int64, name, extra string) inputrec.EventMark {
		return inputrec.EventMark{AtMs: at, Name: name,
			Data: json.RawMessage(`{"MatchGuid":"` + followGuid + `"` + extra + `}`)}
	}
	hit := func(x, y float64) string {
		return fmt.Sprintf(`,"Ball":{"Location":{"X":%g,"Y":%g,"Z":93.1}}`, x, y)
	}
	one := 0
	return &inputrec.Recording{
		MatchGuid:  followGuid,
		DurationMs: 70000,
		Segments: []inputrec.Segment{
			{Index: 0, AnchorMs: 11017, EndMs: 32713, Anchor: "round_started", ClockAtAnchor: 300, EndedBy: "goal", GoalTeam: &one},
			{Index: 1, AnchorMs: 40680, EndMs: 70000, Anchor: "round_started", ClockAtAnchor: 282, EndedBy: "match_end"},
		},
		Events: []inputrec.EventMark{
			ev(7002, "CountdownBegin", ""),
			ev(11017, "RoundStarted", ""),
			ev(13000, "BallHit", hit(0, 0)),
			ev(20000, "BallHit", hit(-712.5, 4000)),
			ev(32713, "GoalScored", `,"GoalSpeed":62.74799728393555`),
			ev(36235, "GoalReplayStart", ""),
			ev(36673, "GoalScored", `,"GoalSpeed":0`),
			ev(36674, "GoalReplayEnd", ""),
			ev(36674, "CountdownBegin", ""),
			ev(40680, "RoundStarted", ""),
			ev(42600, "BallHit", hit(0, 0)),
			ev(55000, "BallHit", hit(1500, -300)),
		},
	}
}

// replaySim plays rec's saved replay: replay time is the recording's
// continuous time plus offset, and it runs at 1x from wall time t0.
type replaySim struct {
	rec    *inputrec.Recording
	cuts   []inputrec.Cut
	offset int64
	t0     time.Time
}

func (s replaySim) replayMs(live int64) int64 { return inputrec.ContinuousMs(s.cuts, live) + s.offset }
func (s replaySim) wall(replayMs int64) time.Time {
	return s.t0.Add(time.Duration(replayMs) * time.Millisecond)
}

// feed sends the follower what the game would while the replay plays from
// replay time from to to: UpdateState twice a second, and every recorded
// event (bar goal-replay ones, which the saved replay doesn't contain) at
// its moment.
func (s replaySim) feed(f *follower, from, to int64) {
	type msg struct {
		at int64
		e  rlstats.Event
	}
	var msgs []msg
	for e := from; e <= to; e += 500 {
		live := inputrec.LiveMs(s.cuts, e-s.offset)
		clock := 300
		if live >= 36674 {
			clock = 282
		}
		data := fmt.Sprintf(`{"MatchGuid":"%s","Game":{"TimeSeconds":%d,"bOvertime":false,"Frame":%d,"Elapsed":%g}}`,
			followGuid, clock, e*30/1000, float64(e)/1000)
		msgs = append(msgs, msg{e, rlstats.Event{Name: "UpdateState", Data: json.RawMessage(data), At: s.wall(e)}})
	}
	for _, ev := range s.rec.Events {
		if ev.Name == "GoalReplayStart" || ev.Name == "GoalReplayEnd" || (ev.Name == "GoalScored" && ev.AtMs == 36673) {
			continue
		}
		r := s.replayMs(ev.AtMs)
		if r < from || r > to {
			continue
		}
		msgs = append(msgs, msg{r, rlstats.Event{Name: ev.Name, Data: ev.Data, At: s.wall(r)}})
	}
	sort.SliceStable(msgs, func(i, j int) bool { return msgs[i].at < msgs[j].at })
	for _, m := range msgs {
		f.onEvent(m.e)
	}
}

func TestFollowerTracksReplay(t *testing.T) {
	rec := followRecording()
	sim := replaySim{rec: rec, cuts: rec.Cuts(), offset: -5000, t0: time.Now()}
	f := newFollower(rec)

	// Nothing to go on before the first kickoff.
	sim.feed(f, 0, sim.replayMs(11000))
	if _, ok := f.liveAt(sim.wall(sim.replayMs(11000))); ok {
		t.Error("claimed a position before any kickoff was seen")
	}

	sim.feed(f, sim.replayMs(11000)+1, sim.replayMs(70000))
	for _, live := range []int64{11017, 20000, 30000, 40680, 50000, 65000} {
		got, ok := f.liveAt(sim.wall(sim.replayMs(live)))
		if !ok || abs(got-live) > 5 {
			t.Errorf("at live %d: got %d (ok=%v)", live, got, ok)
		}
	}
	if len(f.anchors) < 5 {
		t.Errorf("only %d anchors: %+v", len(f.anchors), f.anchors)
	}
}

// Seeking back to the second kickoff, the first thing seen is its
// RoundStarted - picked by its game clock, not by order.
func TestFollowerAfterSeek(t *testing.T) {
	rec := followRecording()
	sim := replaySim{rec: rec, cuts: rec.Cuts(), offset: 2000, t0: time.Now()}
	f := newFollower(rec)

	start := sim.replayMs(39000)
	sim.feed(f, start, sim.replayMs(60000))
	for _, live := range []int64{40680, 50000} {
		got, ok := f.liveAt(sim.wall(sim.replayMs(live)))
		if !ok || abs(got-live) > 5 {
			t.Errorf("at live %d: got %d (ok=%v)", live, got, ok)
		}
	}
}

func TestFollowerDetectsPause(t *testing.T) {
	f := newFollower(followRecording())
	t0 := time.Now()
	for i, e := range []float64{10, 10.5, 11, 11, 11} {
		data := fmt.Sprintf(`{"MatchGuid":"%s","Game":{"TimeSeconds":300,"Elapsed":%g}}`, followGuid, e)
		f.onEvent(rlstats.Event{Name: "UpdateState", Data: json.RawMessage(data), At: t0.Add(time.Duration(i) * 500 * time.Millisecond)})
	}
	if f.rate != 0 {
		t.Errorf("rate = %v while paused", f.rate)
	}
	if e, _ := f.replayAtTime(t0.Add(5 * time.Second)); e != 11000 {
		t.Errorf("paused replay clock moved to %v", e)
	}
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
