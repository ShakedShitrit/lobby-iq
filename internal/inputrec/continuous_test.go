package inputrec

import "testing"

func TestContinuousTime(t *testing.T) {
	rec := &Recording{
		DurationMs: 100000,
		Events: []EventMark{
			{AtMs: 11000, Name: "RoundStarted"},
			{AtMs: 32713, Name: "GoalScored"},
			{AtMs: 36235, Name: "GoalReplayStart"},
			{AtMs: 36674, Name: "GoalReplayEnd"},
			{AtMs: 36674, Name: "CountdownBegin"},
			{AtMs: 50000, Name: "MatchPaused"},
			{AtMs: 60000, Name: "MatchUnpaused"},
		},
	}
	cuts := rec.Cuts()
	want := []Cut{{36235, 36674, "goal_replay"}, {50000, 60000, "paused"}}
	if len(cuts) != len(want) || cuts[0] != want[0] || cuts[1] != want[1] {
		t.Fatalf("cuts = %+v, want %+v", cuts, want)
	}

	for _, tc := range []struct{ live, cont int64 }{
		{1000, 1000},
		{36235, 36235},
		{36500, 36235}, // inside the goal replay
		{36674, 36235},
		{40000, 40000 - 439},
		{70000, 70000 - 439 - 10000},
	} {
		if got := ContinuousMs(cuts, tc.live); got != tc.cont {
			t.Errorf("ContinuousMs(%d) = %d, want %d", tc.live, got, tc.cont)
		}
	}
	for _, tc := range []struct{ cont, live int64 }{
		{1000, 1000},
		{36235, 36674}, // the cut is jumped over
		{40000 - 439, 40000},
		{70000 - 439 - 10000, 70000},
	} {
		if got := LiveMs(cuts, tc.cont); got != tc.live {
			t.Errorf("LiveMs(%d) = %d, want %d", tc.cont, got, tc.live)
		}
	}
}
