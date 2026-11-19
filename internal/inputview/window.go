package inputview

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"fyne.io/fyne/v2"
	fyneapp "fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/ShakedShitrit/lobby-iq/internal/inputrec"
	"github.com/ShakedShitrit/lobby-iq/internal/rlstats"
)

// frameRate is how often the drawing is redrawn during playback. The game
// runs at 120Hz physics, but the eye won't tell the difference past 60.
const frameRate = 60

var teamNames = map[int]string{0: "Blue", 1: "Orange"}

// Playback modes.
const (
	modeWhole   = "Whole match"
	modeKickoff = "One kickoff at a time"
)

// Run opens the viewer as a program of its own, returning when its window
// is closed. store holds the recordings; path, if set, is the one to open
// first. addr is the Stats API, listened to so the viewer can follow a
// replay as it plays.
func Run(store *inputrec.Store, path, addr string) {
	a := fyneapp.New()
	events := &rlstats.Broadcaster{}
	go rlstats.WatchEvents(addr, func(rlstats.MatchState) {}, events.Publish)
	v := Open(a, store, path, events)
	v.w.SetMaster()
	a.Run()
}

// Open shows the viewer in a new window of a. With path empty it opens the
// newest recording in store. events, if not nil, is the Stats API traffic that
// lets it follow a replay by itself.
func Open(a fyne.App, store *inputrec.Store, path string, events *rlstats.Broadcaster) *Viewer {
	w := a.NewWindow("LobbyIQ - Inputs")
	v := &Viewer{
		w:     w,
		store: store,
		pad:   newPadView(),
		mode:  modeWhole,
		tried: map[string]bool{},
		clk:   newClock(0),
	}
	v.build()
	v.refreshFiles(path)

	stopTicker := make(chan struct{})
	go func() {
		t := time.NewTicker(time.Second / frameRate)
		defer t.Stop()
		for {
			select {
			case <-stopTicker:
				return
			case <-t.C:
				fyne.Do(v.frame)
			}
		}
	}()
	stopHotkeys := listenHotkeys(
		func() { fyne.Do(v.syncNow) },
		func() { fyne.Do(v.togglePause) },
	)
	unsubscribe := func() {}
	if events != nil {
		unsubscribe = events.Subscribe(func(e rlstats.Event) {
			fyne.Do(func() { v.onEvent(e) })
		})
	}
	w.SetOnClosed(func() {
		v.closed = true
		close(stopTicker)
		stopHotkeys()
		unsubscribe()
	})

	w.Show()
	return v
}

// Viewer is one open viewer window. Everything on it runs on Fyne's
// goroutine, via fyne.Do, so it needs no locking of its own.
type Viewer struct {
	w      fyne.Window
	store  *inputrec.Store
	closed bool

	rec     *inputrec.Recording
	loaded  string
	entries []inputrec.Entry
	cuts    []inputrec.Cut
	contDur int64
	tl      *timeline
	kind    string
	mode    string
	seg     int
	clk     clock
	follow  *follower
	// tried holds match IDs already looked up for a recording, so a replay
	// with none isn't searched for on every tick.
	tried map[string]bool

	pad       *padView
	files     *widget.Select
	devices   *widget.Select
	modes     *widget.Select
	kickoffs  *widget.Select
	speed     *widget.Select
	followChk *widget.Check
	pauseBtn  *widget.Button
	keepBtn   *widget.Button
	slider    *widget.Slider
	timeLabel *widget.Label
	syncLabel *canvas.Text
	actLabel  *canvas.Text
	keyLabel  *widget.Label
	hint      *widget.Label

	// settingSlider is set while the slider is moved to follow playback, so
	// its change handler can tell that apart from the user dragging it.
	settingSlider bool
}

// Closed reports whether the window has been closed.
func (v *Viewer) Closed() bool { return v.closed }

// ShowRecording opens path and brings the window forward. Call it on Fyne's
// goroutine.
func (v *Viewer) ShowRecording(path string) {
	v.refreshFiles(path)
	v.w.Show()
}

// Focus brings the window forward.
func (v *Viewer) Focus() {
	v.w.Show()
	v.w.RequestFocus()
}

func (v *Viewer) build() {
	v.files = widget.NewSelect(nil, func(string) {
		if i := v.files.SelectedIndex(); i >= 0 && i < len(v.entries) {
			v.load(v.entries[i].Path)
		}
	})
	v.devices = widget.NewSelect(nil, func(string) {
		v.selectDevice(v.devices.SelectedIndex())
	})
	v.modes = widget.NewSelect([]string{modeWhole, modeKickoff}, func(m string) {
		v.setMode(m)
	})
	v.kickoffs = widget.NewSelect(nil, func(string) {
		v.selectKickoff(v.kickoffs.SelectedIndex())
	})
	v.speed = widget.NewSelect([]string{"0.25×", "0.5×", "1×", "2×"}, func(s string) {
		speeds := map[string]float64{"0.25×": 0.25, "0.5×": 0.5, "1×": 1, "2×": 2}
		v.clk.setSpeed(time.Now(), speeds[s])
	})
	v.speed.SetSelected("1×")
	v.followChk = widget.NewCheck("Follow the replay automatically", nil)
	v.followChk.SetChecked(true)

	syncLabel := "Sync at GO"
	pauseLabel := "Pause"
	if syncHotkeyName != "" {
		syncLabel += " (" + syncHotkeyName + ")"
		pauseLabel += " (" + pauseHotkeyName + ")"
	}
	syncBtn := widget.NewButton(syncLabel, v.syncNow)
	syncBtn.Importance = widget.HighImportance
	v.pauseBtn = widget.NewButton(pauseLabel, v.togglePause)
	v.keepBtn = widget.NewButton("Keep", v.keepCurrent)

	nudge := func(ms int64) *widget.Button {
		return widget.NewButton(fmt.Sprintf("%+d ms", ms), func() { v.clk.nudge(ms) })
	}

	v.slider = widget.NewSlider(0, 1)
	v.slider.Step = 1
	v.slider.OnChanged = func(f float64) {
		if v.settingSlider {
			return
		}
		v.clk.seek(time.Now(), int64(f))
	}

	v.timeLabel = widget.NewLabel("")
	v.syncLabel = canvas.NewText("", colLit)
	v.syncLabel.Alignment = fyne.TextAlignCenter
	v.actLabel = canvas.NewText("—", colLit)
	v.actLabel.TextStyle = fyne.TextStyle{Bold: true}
	v.actLabel.TextSize = 16
	v.actLabel.Alignment = fyne.TextAlignCenter
	v.keyLabel = widget.NewLabel("")
	v.keyLabel.Alignment = fyne.TextAlignCenter
	v.hint = widget.NewLabel("")
	v.hint.Wrapping = fyne.TextWrapWord

	form := widget.NewForm(
		widget.NewFormItem("Recording", container.NewBorder(nil, nil, nil, v.keepBtn, v.files)),
		widget.NewFormItem("Device", v.devices),
		widget.NewFormItem("Playback", v.modes),
		widget.NewFormItem("Kickoff", v.kickoffs),
	)
	v.modes.SetSelected(modeWhole)
	controls := container.NewVBox(
		container.NewCenter(container.NewHBox(syncBtn, v.pauseBtn, v.speed)),
		container.NewCenter(container.NewHBox(nudge(-100), nudge(-10), nudge(10), nudge(100))),
	)

	v.w.SetContent(container.NewPadded(container.NewVBox(
		form,
		container.NewCenter(v.followChk),
		v.syncLabel,
		container.NewCenter(v.pad.root),
		v.actLabel,
		v.keyLabel,
		v.slider,
		v.timeLabel,
		controls,
		v.hint,
	)))
}

// refreshFiles relists the recordings and opens path, or the newest.
func (v *Viewer) refreshFiles(path string) {
	entries, err := v.store.List()
	if err != nil {
		v.hint.SetText("Couldn't list " + v.store.Dir() + ": " + err.Error())
	}
	if path != "" && !containsEntry(entries, path) {
		entries = append([]inputrec.Entry{{Path: path, Kept: v.store.IsKept(path)}}, entries...)
	}
	v.entries = entries
	if len(entries) == 0 {
		abs, _ := filepath.Abs(v.store.Dir())
		v.hint.SetText("No recordings yet in " + abs + ". Turn on input_recording in config.yaml and play a match.")
		return
	}

	names := make([]string, len(entries))
	selected := 0
	for i, e := range entries {
		// Kept ones are starred: they're the ones that will still be here
		// after the next twenty matches.
		names[i] = filepath.Base(e.Path)
		if e.Kept {
			names[i] = "★ " + names[i]
		}
		if path != "" && filepath.Clean(e.Path) == filepath.Clean(path) {
			selected = i
		}
	}
	v.files.Options = names
	v.files.SetSelectedIndex(selected)
	// Applied directly as well: Select only reports a change, and this may
	// be the entry already selected.
	v.load(entries[selected].Path)
}

// Refresh relists the recordings, following the one shown if it has moved -
// as it does when it's kept because its replay was saved.
func (v *Viewer) Refresh() {
	path := v.loaded
	if v.rec != nil {
		if _, err := os.Stat(path); err != nil {
			if moved, _ := v.store.FindByGuid(v.rec.MatchGuid); moved != "" {
				path = moved
			}
		}
	}
	v.refreshFiles(path)
}

// keepCurrent keeps the recording shown for good, as saving its replay
// would.
func (v *Viewer) keepCurrent() {
	if v.loaded == "" || v.store.IsKept(v.loaded) {
		return
	}
	path, err := v.store.Keep(v.loaded, nil)
	if err != nil {
		v.hint.SetText("Couldn't keep it: " + err.Error())
		return
	}
	v.refreshFiles(path)
}

func (v *Viewer) load(path string) {
	if path == v.loaded && v.rec != nil {
		return
	}
	rec, err := inputrec.Load(path)
	if err != nil {
		v.rec = nil
		v.hint.SetText("Couldn't open " + filepath.Base(path) + ": " + err.Error())
		return
	}
	v.rec, v.loaded = rec, path
	if v.store.IsKept(path) {
		v.keepBtn.SetText("Kept")
		v.keepBtn.Disable()
	} else {
		v.keepBtn.SetText("Keep")
		v.keepBtn.Enable()
	}
	v.cuts = rec.Cuts()
	v.contDur = inputrec.ContinuousMs(v.cuts, rec.DurationMs)
	v.follow = newFollower(rec)
	v.tried[rec.MatchGuid] = true

	opts := make([]string, len(rec.Devices))
	for i, d := range rec.Devices {
		n := 0
		for _, s := range rec.Samples {
			if s.Device == d.ID {
				n++
			}
		}
		plural := "s"
		if n == 1 {
			plural = ""
		}
		opts[i] = fmt.Sprintf("%s (%d change%s)", d.Name, n, plural)
	}
	v.devices.Options = opts
	best := bestDevice(rec)
	if best < len(opts) {
		v.devices.SetSelectedIndex(best)
	}
	v.selectDevice(best)

	segs := make([]string, len(rec.Segments))
	for i, s := range rec.Segments {
		segs[i] = segmentLabel(s)
	}
	v.kickoffs.Options = segs
	v.setMode(v.mode)

	note := ""
	switch {
	case rec.ReplayName != "":
		note += "\nKept with the saved replay “" + rec.ReplayName + "”."
	case rec.ReplayFile != "":
		note += "\nKept with the saved replay " + rec.ReplayFile + "."
	case !v.store.IsKept(path):
		note += fmt.Sprintf("\nTemporary: only the last %d matches are kept, unless you save this "+
			"match's replay in Rocket League or press Keep.", v.store.Limit())
	}
	if !rec.Complete {
		note += "\nThis match wasn't recorded to the end."
	}
	if len(rec.Segments) == 1 && rec.Segments[0].Anchor == "none" {
		note += "\nNo kickoffs were detected, so line it up with Sync and the nudge buttons."
	}
	v.hint.SetText("Open this match's replay in Rocket League and the viewer follows it by itself, " +
		"locking on at the first kickoff, goal or ball touch it recognises.\n" +
		"Or sync by hand: press Sync the instant the replay shows GO" + hotkeyHint() + "." + note)
}

func (v *Viewer) selectDevice(i int) {
	if v.rec == nil || i < 0 || i >= len(v.rec.Devices) {
		v.tl, v.kind = &timeline{}, ""
		return
	}
	d := v.rec.Devices[i]
	v.tl = newTimeline(v.rec, d.ID)
	v.kind = d.Kind
	v.pad.setKind(d.Kind)
	// One or the other: a keyboard has no sticks to draw, a pad no keys.
	if d.Kind == inputrec.KindKBM {
		v.pad.root.Hide()
		v.keyLabel.Show()
	} else {
		v.pad.root.Show()
		v.keyLabel.Hide()
	}
}

// setMode switches between playing the whole match continuously and
// playing one kickoff at a time, cued up paused at the start.
func (v *Viewer) setMode(m string) {
	v.mode = m
	if v.rec == nil {
		return
	}
	speed := v.clk.speed
	if m == modeWhole {
		v.clk = newClock(v.contDur)
		v.slider.Max = float64(max(v.contDur, 1))
		// Cued at the first kickoff, ready for the first Sync.
		if len(v.rec.Segments) > 0 {
			v.clk.seek(time.Now(), inputrec.ContinuousMs(v.cuts, v.rec.Segments[0].AnchorMs))
		}
	} else {
		v.selectSegment(0)
	}
	if speed > 0 {
		v.clk.speed = speed
	}
	v.slider.Refresh()
	if len(v.rec.Segments) > 0 {
		v.kickoffs.SetSelectedIndex(0)
	}
	v.updatePauseLabel()
}

// selectKickoff cues kickoff i: in whole-match mode by seeking to it, in
// kickoff mode by making it the stretch being played.
func (v *Viewer) selectKickoff(i int) {
	if v.rec == nil || i < 0 || i >= len(v.rec.Segments) {
		return
	}
	if v.mode == modeWhole {
		v.clk.pause(time.Now())
		v.clk.seek(time.Now(), inputrec.ContinuousMs(v.cuts, v.rec.Segments[i].AnchorMs))
		v.updatePauseLabel()
		return
	}
	if i != v.seg {
		v.selectSegment(i)
	}
}

// selectSegment cues up segment i at its start, paused.
func (v *Viewer) selectSegment(i int) {
	if v.rec == nil || i < 0 || i >= len(v.rec.Segments) {
		return
	}
	v.seg = i
	dur := v.rec.Segments[i].DurationMs()
	speed := v.clk.speed
	v.clk = newClock(dur)
	if speed > 0 {
		v.clk.speed = speed
	}
	v.slider.Max = float64(max(dur, 1))
	v.slider.Refresh()
	v.updatePauseLabel()
}

// syncNow starts playback at "GO". In whole-match mode that's the kickoff
// nearest the current position, so a Sync at any GO in the replay corrects
// whatever drift has built up.
func (v *Viewer) syncNow() {
	if v.rec == nil {
		return
	}
	now := time.Now()
	if v.mode == modeWhole && len(v.rec.Segments) > 0 {
		pos, _ := v.clk.at(now)
		target, best := int64(0), math.Inf(1)
		for _, s := range v.rec.Segments {
			c := inputrec.ContinuousMs(v.cuts, s.AnchorMs)
			if d := math.Abs(float64(c - pos)); d < best {
				target, best = c, d
			}
		}
		v.clk.seek(now, target)
		v.clk.resume(now)
	} else {
		v.clk.sync(now)
	}
	v.updatePauseLabel()
}

func (v *Viewer) togglePause() {
	now := time.Now()
	if v.clk.playing {
		v.clk.pause(now)
	} else {
		v.clk.resume(now)
	}
	v.updatePauseLabel()
}

func (v *Viewer) updatePauseLabel() {
	label := "Play"
	if v.clk.playing {
		label = "Pause"
	}
	if pauseHotkeyName != "" {
		label += " (" + pauseHotkeyName + ")"
	}
	v.pauseBtn.SetText(label)
}

// onEvent takes Stats API traffic: it drives the follower, and switches to
// the recording of whichever match's replay starts playing.
func (v *Viewer) onEvent(e rlstats.Event) {
	if guid := replayGuid(e); guid != "" && (v.rec == nil || guid != v.rec.MatchGuid) && !v.tried[guid] {
		v.tried[guid] = true
		if path, _ := v.store.FindByGuid(guid); path != "" {
			v.refreshFiles(path)
		}
	}
	if v.follow != nil {
		v.follow.onEvent(e)
	}
}

// replayGuid returns the match ID of the replay e shows to be playing, or
// "" if e doesn't show one: only ReplayCreated and replay-timed UpdateState
// ticks do, so a live match never switches the recording shown.
func replayGuid(e rlstats.Event) string {
	var d struct {
		MatchGuid string
		Game      *struct {
			Elapsed *float64 `json:"Elapsed"`
		}
	}
	if json.Unmarshal(e.Data, &d) != nil {
		return ""
	}
	switch {
	case e.Name == "ReplayCreated":
		return d.MatchGuid
	case e.Name == "UpdateState" && d.Game != nil && d.Game.Elapsed != nil:
		return d.MatchGuid
	}
	return ""
}

// frame draws the current position.
func (v *Viewer) frame() {
	if v.rec == nil || v.tl == nil {
		return
	}
	now := time.Now()

	live, following := int64(0), false
	v.syncLabel.Text = ""
	if v.followChk.Checked && v.follow != nil && v.follow.active(now) {
		if l, ok := v.follow.liveAt(now); ok {
			live, following = l, true
			v.syncLabel.Text = fmt.Sprintf("● Following the replay  ·  locked on %d moments", len(v.follow.anchors))
		} else {
			v.syncLabel.Text = "Replay detected  ·  waiting for a kickoff, goal or touch to lock on to"
		}
	}
	v.syncLabel.Refresh()

	var pos, dur int64
	var segIdx int
	switch {
	case following:
		segIdx = segmentAt(v.rec, live)
		if v.mode == modeWhole {
			pos, dur = inputrec.ContinuousMs(v.cuts, live), v.contDur
		} else {
			if segIdx != v.seg {
				v.kickoffs.SetSelectedIndex(segIdx)
				v.selectSegment(segIdx)
			}
			seg := v.rec.Segments[v.seg]
			pos, dur = min(max(live-seg.AnchorMs, 0), seg.DurationMs()), seg.DurationMs()
		}
	case v.mode == modeWhole:
		p, done := v.clk.at(now)
		pos, dur, live = p, v.contDur, inputrec.LiveMs(v.cuts, p)
		segIdx = segmentAt(v.rec, live)
		if done && v.clk.playing {
			v.clk.pause(now)
			v.updatePauseLabel()
		}
	default:
		if v.seg >= len(v.rec.Segments) {
			return
		}
		seg := v.rec.Segments[v.seg]
		p, done := v.clk.at(now)
		pos, dur, live, segIdx = p, seg.DurationMs(), seg.AnchorMs+p, v.seg
		// The kickoff has played out: cue the next one, so the next Sync is
		// a single keypress from inside the game.
		if done && v.clk.playing && v.seg+1 < len(v.rec.Segments) {
			v.kickoffs.SetSelectedIndex(v.seg + 1)
			v.selectSegment(v.seg + 1)
		} else if done && v.clk.playing {
			v.clk.pause(now)
			v.updatePauseLabel()
		}
	}

	st := v.tl.stateAt(live)
	v.pad.set(st)
	v.actLabel.Text = actions(st)
	if v.kind == inputrec.KindKBM {
		v.actLabel.Text = "Keys held"
	}
	v.actLabel.Refresh()
	v.keyLabel.SetText(keysText(st))

	v.settingSlider = true
	v.slider.SetValue(float64(pos))
	v.settingSlider = false

	state := "paused"
	switch {
	case following:
		state = "following the replay"
	case v.clk.playing:
		state = "playing"
	}
	where := "before the first kickoff"
	if segIdx >= 0 {
		where = fmt.Sprintf("kickoff %d", segIdx+1)
	}
	v.timeLabel.SetText(fmt.Sprintf("%s of %s  ·  %s  ·  %s",
		mmss(pos), mmss(dur), where, state))
}

// segmentAt is the index of the last kickoff at or before liveMs, or -1.
func segmentAt(rec *inputrec.Recording, liveMs int64) int {
	idx := -1
	for i, s := range rec.Segments {
		if s.AnchorMs <= liveMs {
			idx = i
		}
	}
	return idx
}

func mmss(ms int64) string {
	return fmt.Sprintf("%d:%04.1f", ms/60000, float64(ms%60000)/1000)
}

func segmentLabel(s inputrec.Segment) string {
	label := fmt.Sprintf("Kickoff %d  ·  clock %d:%02d  ·  %.1f s", s.Index+1,
		s.ClockAtAnchor/60, s.ClockAtAnchor%60, float64(s.DurationMs())/1000)
	if s.Overtime {
		label += "  ·  OT"
	}
	switch s.EndedBy {
	case "goal":
		if s.GoalTeam != nil {
			name := teamNames[*s.GoalTeam]
			if name == "" {
				name = fmt.Sprintf("team %d", *s.GoalTeam)
			}
			label += "  ·  " + name + " goal"
		} else {
			label += "  ·  goal"
		}
	case "match_end":
		label += "  ·  end of match"
	case "paused":
		label += "  ·  paused"
	}
	return label
}

func hotkeyHint() string {
	if syncHotkeyName == "" {
		return ""
	}
	return " (or " + syncHotkeyName + " from inside the game; " + pauseHotkeyName + " pauses)"
}

func containsEntry(entries []inputrec.Entry, p string) bool {
	for _, e := range entries {
		if filepath.Clean(e.Path) == filepath.Clean(p) {
			return true
		}
	}
	return false
}
