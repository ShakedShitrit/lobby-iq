package inputrec

import (
	"encoding/json"
	"sort"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/ShakedShitrit/lobby-iq/internal/rlstats"
)

// Defaults for Options left zero.
const (
	DefaultKeep   = 20
	DefaultPollHz = 250
	DefaultDir    = "recordings"
)

// minDuration is how long a recording has to run to be worth a file. Shorter
// than this is the app being started on the podium, not a match.
const minDuration = 5 * time.Second

// countdownLength is how long after CountdownBegin the game reports
// RoundStarted - measured at 4.0s in real matches - used to place "GO" when
// RoundStarted doesn't arrive.
const countdownLength = 4 * time.Second

// Options configure a Recorder.
type Options struct {
	// Dir is where recordings are saved.
	Dir string
	// Keep is how many temporary recordings to keep; the oldest beyond it
	// are deleted after each save. Kept recordings don't count.
	Keep int
	// ReplaysDir is where Rocket League saves replays. A recording whose
	// match has a replay saved there is kept for good. Empty turns that off.
	ReplaysDir string
	// PollHz is how often devices are read.
	PollHz int
	// Devices lists the Kind* sources to record from; empty means all.
	Devices []string
}

// Recorder turns Stats API traffic into recordings: it starts reading your
// devices when a match starts and saves what it read when the match ends.
// A nil *Recorder is valid and does nothing, so callers needn't check whether
// recording is on.
type Recorder struct {
	opts  Options
	store *Store
	// open is how sources are obtained; replaced in tests.
	open func(kinds []string) []Source

	mu  sync.Mutex
	cur *take
	// finished is the last match a recording was saved for, so the
	// UpdateState ticks that keep coming after MatchEnded (the podium,
	// the scoreboard) don't start another.
	finished string
	// replayGuid is the match whose saved replay is being watched, so its
	// ticks and events neither start a recording nor announce the replay
	// again.
	replayGuid string
	onChange   func(recording bool)
	saved      func(path string)
	onReplay   func(guid, path string)
}

// take is one match being recorded.
type take struct {
	rec     Recording
	start   time.Time
	sources []Source
	poller  *poller

	phase       string
	open        int // index of the open segment in rec.Segments, or -1
	countdownAt time.Time
	clock       int
	overtime    bool
	sawEnd      bool
	// discard drops the take instead of saving it: it turned out to be a
	// saved replay being watched, not a match being played.
	discard bool
}

// New returns a Recorder with opts, filling in defaults.
func New(opts Options) *Recorder {
	if opts.Dir == "" {
		opts.Dir = DefaultDir
	}
	if opts.Keep == 0 {
		opts.Keep = DefaultKeep
	}
	if opts.PollHz <= 0 {
		opts.PollHz = DefaultPollHz
	}
	store := NewStore(opts.Dir, opts.Keep, opts.ReplaysDir)
	store.Migrate()
	return &Recorder{opts: opts, store: store, open: openSources}
}

// Dir is where recordings are saved.
func (r *Recorder) Dir() string {
	if r == nil {
		return ""
	}
	return r.opts.Dir
}

// Store is where the recordings are kept.
func (r *Recorder) Store() *Store {
	if r == nil {
		return nil
	}
	return r.store
}

// OnChange registers f to be told whenever recording starts or stops. It is
// called from the Stats API goroutine, never with the Recorder locked.
func (r *Recorder) OnChange(f func(recording bool)) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onChange = f
}

// OnSaved registers f to be told the path of each recording saved.
func (r *Recorder) OnSaved(f func(path string)) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.saved = f
}

// OnReplay registers f to be told when a saved replay starts playing of a
// match that has a recording, with that recording's path - the cue to show
// the recording alongside it.
func (r *Recorder) OnReplay(f func(guid, path string)) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onReplay = f
}

// Recording reports whether a match is being recorded right now.
func (r *Recorder) Recording() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cur != nil
}

// Observe takes each MatchState from rlstats.Watch. A new match starts a
// recording; the match ending, or the connection to the game going, saves it.
func (r *Recorder) Observe(s rlstats.MatchState) {
	if r == nil {
		return
	}
	r.mu.Lock()
	var done *take
	var replay *replayNote
	started := false
	now := time.Now()

	switch {
	case s.MatchGuid == "":
		// The game closed or the connection dropped mid-match.
		done = r.detachLocked(now)
		r.replayGuid = ""
	case s.HistoryReplay:
		done, replay = r.replayLocked(s.MatchGuid, now)
	default:
		done, started, replay = r.ensureLocked(s.MatchGuid, now)
		// Game.bReplay is deliberately not used: real matches showed it set
		// during live play, which cut kickoffs short as if a goal had been
		// scored. The GoalReplay* events are reliable instead.
		if t := r.cur; t != nil {
			if s.Arena != "" {
				t.rec.Arena = s.Arena
			}
			t.clock, t.overtime = s.TimeSeconds, s.Overtime
			if s.Ended {
				t.sawEnd = true
				r.closeSegmentLocked(t, now, "match_end", nil)
				t.setPhase(now, PhaseEnded)
				if d := r.detachLocked(now); d != nil {
					done = d
				}
			}
		}
	}
	r.mu.Unlock()
	r.settle(done, started, replay)
}

// OnEvent takes each raw event from rlstats.WatchEvents. These are what mark
// kickoffs, goals and goal replays - the segment boundaries.
func (r *Recorder) OnEvent(e rlstats.Event) {
	if r == nil || e.Name == "UpdateState" {
		return
	}
	var data struct {
		MatchGuid   string
		TimeSeconds *int
		Overtime    *bool `json:"bOvertime"`
		Scorer      *struct{ TeamNum int }
	}
	_ = json.Unmarshal(e.Data, &data)

	if e.Name == "ReplayCreated" {
		// Sent when a replay is opened from Match History: whatever started
		// recording under its match ID was the replay loading, not a match.
		r.mu.Lock()
		done, replay := r.replayLocked(data.MatchGuid, e.At)
		r.mu.Unlock()
		r.settle(done, false, replay)
		return
	}

	r.mu.Lock()
	var done *take
	var replay *replayNote
	started := false
	switch {
	case e.Name == "MatchDestroyed" && data.MatchGuid != "" && data.MatchGuid == r.replayGuid:
		r.replayGuid = ""
	case data.MatchGuid != "":
		done, started, replay = r.ensureLocked(data.MatchGuid, e.At)
	}
	t := r.cur
	if t == nil {
		r.mu.Unlock()
		r.settle(done, started, replay)
		return
	}

	t.rec.Events = append(t.rec.Events, EventMark{AtMs: t.ms(e.At), Name: e.Name, Data: e.Data})
	if data.TimeSeconds != nil {
		t.clock = *data.TimeSeconds
	}
	if data.Overtime != nil {
		t.overtime = *data.Overtime
	}

	switch e.Name {
	case "CountdownBegin":
		// Normally a goal has already closed the segment; this catches a
		// stoppage the game didn't announce.
		r.closeSegmentLocked(t, e.At, "countdown", nil)
		t.countdownAt = e.At
		t.setPhase(e.At, PhaseCountdown)
	case "RoundStarted":
		r.openSegmentLocked(t, e.At, "round_started")
	case "ClockUpdatedSeconds":
		// The clock running with no segment open means play is live but its
		// kickoff wasn't reported - place "GO" as best we can.
		if t.open < 0 && t.phase != PhaseGoalReplay && t.phase != PhasePostGoal && t.phase != PhaseEnded {
			if !t.countdownAt.IsZero() {
				r.openSegmentLocked(t, t.countdownAt.Add(countdownLength), "countdown")
			} else {
				r.openSegmentLocked(t, e.At.Add(-time.Second), "clock")
			}
		}
	case "GoalScored":
		// The game sends GoalScored a second time, with GoalSpeed 0, as a
		// goal replay ends. Only the first, which closes the kickoff, counts.
		if t.open >= 0 {
			var team *int
			if data.Scorer != nil {
				team = &data.Scorer.TeamNum
			}
			r.closeSegmentLocked(t, e.At, "goal", team)
			t.setPhase(e.At, PhasePostGoal)
		}
	case "GoalReplayStart":
		t.setPhase(e.At, PhaseGoalReplay)
	case "GoalReplayEnd":
		t.setPhase(e.At, PhasePostGoal)
	case "MatchPaused":
		r.closeSegmentLocked(t, e.At, "paused", nil)
		t.setPhase(e.At, PhasePaused)
	case "MatchUnpaused":
		if t.phase == PhasePaused {
			r.openSegmentLocked(t, e.At, "unpaused")
		}
	case "MatchEnded":
		t.sawEnd = true
		r.closeSegmentLocked(t, e.At, "match_end", nil)
		t.setPhase(e.At, PhaseEnded)
		done = r.detachLocked(e.At)
	case "MatchDestroyed":
		done = r.detachLocked(e.At)
	}
	r.mu.Unlock()
	r.settle(done, started, replay)
}

// replayNote is a replay to announce once the lock is released.
type replayNote struct{ guid, path string }

// ensureLocked makes sure guid is the match being recorded, starting a new
// recording if it isn't. Any recording of a different match is detached and
// returned for saving.
//
// A match that already has a recording isn't recorded again: its ID turning
// up a second time means its saved replay is being watched. The exception
// is the match just finished, whose ID keeps arriving through the podium.
func (r *Recorder) ensureLocked(guid string, at time.Time) (done *take, started bool, replay *replayNote) {
	if r.cur != nil && r.cur.rec.MatchGuid == guid {
		return nil, false, nil
	}
	if guid == r.replayGuid {
		return nil, false, nil
	}
	if r.cur != nil {
		done = r.detachLocked(at)
	}
	if guid == r.finished {
		return done, false, nil
	}
	if path, _ := r.store.FindByGuid(guid); path != "" {
		r.replayGuid = guid
		return done, false, &replayNote{guid: guid, path: path}
	}

	t := &take{
		rec: Recording{
			Version:   FormatVersion,
			MatchGuid: guid,
			StartedAt: at,
			PollHz:    r.opts.PollHz,
		},
		start: at,
		open:  -1,
	}
	t.setPhase(at, PhasePregame)
	t.sources = r.open(r.opts.Devices)
	for i, src := range t.sources {
		t.rec.Devices = append(t.rec.Devices, Device{ID: i, Kind: src.Kind(), Name: src.Name()})
	}
	if len(t.sources) > 0 {
		t.poller = startPoller(t.sources, r.opts.PollHz, func(dev int, at time.Time, st State) {
			r.sample(t, dev, at, st)
		})
	}
	r.cur = t
	zap.L().Info("input recording started",
		zap.String("match", guid), zap.Int("devices", len(t.sources)))
	return done, true, nil
}

// replayLocked notes that guid's saved replay is playing. A recording begun
// for it - a replay loading looks like a match starting - is dropped, and
// the match's own recording, if it has one, is looked up to announce.
func (r *Recorder) replayLocked(guid string, at time.Time) (done *take, replay *replayNote) {
	if r.cur != nil {
		if r.cur.rec.MatchGuid == guid || guid == "" {
			r.cur.discard = true
		}
		done = r.detachLocked(at)
		if done.discard {
			r.finished = ""
		}
	}
	if guid == "" || guid == r.replayGuid {
		return done, nil
	}
	r.replayGuid = guid
	path, err := r.store.FindByGuid(guid)
	if err != nil || path == "" {
		zap.L().Info("watching a replay with no input recording", zap.String("match", guid))
		return done, nil
	}
	return done, &replayNote{guid: guid, path: path}
}

// sample records a device's new state into t.
func (r *Recorder) sample(t *take, dev int, at time.Time, st State) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t.rec.Samples = append(t.rec.Samples, Sample{AtMs: t.ms(at), Segment: -1, Device: dev, State: st})
}

// detachLocked stops recording into the current take and returns it for
// settle to save. The finished match is remembered so it isn't restarted.
func (r *Recorder) detachLocked(at time.Time) *take {
	t := r.cur
	if t == nil {
		return nil
	}
	r.cur = nil
	r.finished = t.rec.MatchGuid
	r.closeSegmentLocked(t, at, "recording_end", nil)
	t.rec.Complete = t.sawEnd
	t.rec.DurationMs = t.ms(at)
	return t
}

func (r *Recorder) openSegmentLocked(t *take, at time.Time, anchor string) {
	r.closeSegmentLocked(t, at, "recording_end", nil)
	t.rec.Segments = append(t.rec.Segments, Segment{
		Index:         len(t.rec.Segments),
		AnchorMs:      t.ms(at),
		EndMs:         -1,
		Anchor:        anchor,
		ClockAtAnchor: t.clock,
		Overtime:      t.overtime,
	})
	t.open = len(t.rec.Segments) - 1
	t.countdownAt = time.Time{}
	t.setPhase(at, PhaseLive)
}

func (r *Recorder) closeSegmentLocked(t *take, at time.Time, endedBy string, goalTeam *int) {
	if t.open < 0 {
		return
	}
	seg := &t.rec.Segments[t.open]
	seg.EndMs = max(t.ms(at), seg.AnchorMs)
	seg.EndedBy = endedBy
	seg.GoalTeam = goalTeam
	t.open = -1
}

// settle finishes what a locked section decided, outside the lock: the
// poller's last emit needs the lock to land, and saving does file I/O.
func (r *Recorder) settle(done *take, started bool, replay *replayNote) {
	r.mu.Lock()
	onChange, saved, onReplay := r.onChange, r.saved, r.onReplay
	r.mu.Unlock()

	if done != nil {
		if done.poller != nil {
			done.poller.Stop()
		}
		for i, src := range done.sources {
			// Asked again now: a pad plugged in mid-match is only
			// identified once it's connected.
			done.rec.Devices[i].Kind, done.rec.Devices[i].Name = src.Kind(), src.Name()
			src.Close()
		}
		if done.discard {
			zap.L().Info("dropped a recording that was a replay being watched", zap.String("match", done.rec.MatchGuid))
		} else if path := r.write(done); path != "" && saved != nil {
			saved(path)
		}
	}
	if onChange != nil && (done != nil || started) {
		onChange(started)
	}
	if replay != nil {
		zap.L().Info("watching the replay of a recorded match",
			zap.String("match", replay.guid), zap.String("recording", replay.path))
		if onReplay != nil {
			onReplay(replay.guid, replay.path)
		}
	}
}

// write finalises a detached take and saves it, returning the path or ""
// if nothing was saved.
func (r *Recorder) write(t *take) string {
	rec := &t.rec
	if time.Duration(rec.DurationMs)*time.Millisecond < minDuration {
		zap.L().Info("input recording too short to keep", zap.String("match", rec.MatchGuid))
		return ""
	}

	// No kickoff seen at all - most likely the game names its events
	// differently than expected. Keep everything as one segment rather than
	// throw the match away; the viewer can still be lined up by hand.
	if len(rec.Segments) == 0 {
		rec.Segments = []Segment{{AnchorMs: 0, EndMs: rec.DurationMs, Anchor: "none", EndedBy: "recording_end"}}
	}
	assignSegments(rec)

	path, kept, err := r.store.Save(rec)
	if err != nil {
		zap.L().Warn("saving input recording", zap.Error(err))
		return ""
	}
	zap.L().Info("input recording saved",
		zap.String("path", path), zap.Int("samples", len(rec.Samples)),
		zap.Int("segments", len(rec.Segments)), zap.Bool("complete", rec.Complete),
		zap.Bool("kept", kept))
	return path
}

// assignSegments places every sample in the segment it falls in. It is done
// once at the end rather than as samples arrive because some anchors are
// only known after the fact - a "countdown" anchor is placed when the clock
// is first seen moving, a second or so after the inputs it covers.
func assignSegments(rec *Recording) {
	segs := rec.Segments
	for i := range rec.Samples {
		s := &rec.Samples[i]
		s.Segment, s.TMs = -1, 0
		j := sort.Search(len(segs), func(k int) bool { return segs[k].EndMs > s.AtMs })
		if j < len(segs) && segs[j].AnchorMs <= s.AtMs {
			s.Segment = segs[j].Index
			s.TMs = s.AtMs - segs[j].AnchorMs
		}
	}
}

func (t *take) ms(at time.Time) int64 { return at.Sub(t.start).Milliseconds() }

func (t *take) setPhase(at time.Time, phase string) {
	if t.phase == phase {
		return
	}
	t.phase = phase
	t.rec.Phases = append(t.rec.Phases, PhaseMark{AtMs: t.ms(at), Phase: phase})
}
