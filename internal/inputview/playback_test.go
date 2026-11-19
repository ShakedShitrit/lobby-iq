package inputview

import (
	"testing"
	"time"

	"github.com/ShakedShitrit/lobby-iq/internal/inputrec"
)

func TestTimelineStateAt(t *testing.T) {
	rec := &inputrec.Recording{
		Devices: []inputrec.Device{{ID: 0}, {ID: 1}},
		Samples: []inputrec.Sample{
			{AtMs: 100, Device: 0, State: inputrec.State{Buttons: inputrec.ButtonA}},
			{AtMs: 150, Device: 1, State: inputrec.State{Keys: []uint16{0x57}}},
			{AtMs: 200, Device: 0, State: inputrec.State{}},
			{AtMs: 300, Device: 0, State: inputrec.State{RT: 255}},
		},
	}
	tl := newTimeline(rec, 0)
	for _, tc := range []struct {
		at   int64
		want inputrec.State
	}{
		{50, inputrec.State{}},
		{100, inputrec.State{Buttons: inputrec.ButtonA}},
		{199, inputrec.State{Buttons: inputrec.ButtonA}},
		{250, inputrec.State{}},
		{9999, inputrec.State{RT: 255}},
	} {
		if got := tl.stateAt(tc.at); !got.Equal(tc.want) {
			t.Errorf("stateAt(%d) = %+v, want %+v", tc.at, got, tc.want)
		}
	}
	if d := bestDevice(rec); d != 0 {
		t.Errorf("bestDevice = %d, want 0", d)
	}
}

func TestClock(t *testing.T) {
	t0 := time.Now()
	ms := func(n int) time.Time { return t0.Add(time.Duration(n) * time.Millisecond) }

	c := newClock(1000)
	if pos, _ := c.at(ms(500)); pos != 0 {
		t.Errorf("unsynced clock moved to %d", pos)
	}

	c.sync(ms(0))
	if pos, _ := c.at(ms(400)); pos != 400 {
		t.Errorf("at 400ms = %d", pos)
	}

	c.setSpeed(ms(400), 0.5)
	if pos, _ := c.at(ms(600)); pos != 500 {
		t.Errorf("half speed for 200ms from 400 = %d, want 500", pos)
	}

	c.pause(ms(600))
	if pos, _ := c.at(ms(5000)); pos != 500 {
		t.Errorf("paused clock moved to %d", pos)
	}

	c.nudge(-10)
	c.resume(ms(5000))
	if pos, _ := c.at(ms(5200)); pos != 590 {
		t.Errorf("after nudge and resume = %d, want 590", pos)
	}

	if pos, done := c.at(ms(99999)); pos != 1000 || !done {
		t.Errorf("past the end = %d, %v", pos, done)
	}
}
