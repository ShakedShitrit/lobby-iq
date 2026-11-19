package inputrec

// Cut is a stretch of the live match that a saved replay leaves out, so
// playing the recording continuously alongside the replay means skipping it.
type Cut struct {
	FromMs int64
	ToMs   int64
	// Reason is "goal_replay" or "paused".
	Reason string
}

// Cuts lists the stretches of rec that a saved replay doesn't contain, in
// order: goal replays, and pauses. They are read from the recorded events
// rather than stored, so recordings made before this existed get them too.
func (r *Recording) Cuts() []Cut {
	var cuts []Cut
	var open *Cut
	end := func(at int64) {
		if open != nil && at > open.FromMs {
			open.ToMs = at
			cuts = append(cuts, *open)
		}
		open = nil
	}
	for _, e := range r.Events {
		switch e.Name {
		case "GoalReplayStart":
			if open == nil {
				open = &Cut{FromMs: e.AtMs, Reason: "goal_replay"}
			}
		// A goal replay ends with GoalReplayEnd; the countdown or kickoff
		// starting is a backstop for one whose end wasn't reported.
		case "GoalReplayEnd", "CountdownBegin", "RoundStarted":
			if open != nil && open.Reason == "goal_replay" {
				end(e.AtMs)
			}
		case "MatchPaused":
			if open == nil {
				open = &Cut{FromMs: e.AtMs, Reason: "paused"}
			}
		case "MatchUnpaused":
			if open != nil && open.Reason == "paused" {
				end(e.AtMs)
			}
		}
	}
	end(r.DurationMs)
	return cuts
}

// ContinuousMs converts a recording time to continuous time: the time with
// every cut removed, which is how far into the saved replay it should be. A
// time inside a cut maps to where the cut starts.
func ContinuousMs(cuts []Cut, liveMs int64) int64 {
	out := liveMs
	for _, c := range cuts {
		switch {
		case liveMs >= c.ToMs:
			out -= c.ToMs - c.FromMs
		case liveMs > c.FromMs:
			out -= liveMs - c.FromMs
		}
	}
	return out
}

// LiveMs is the inverse of ContinuousMs. The moment a cut starts maps to
// where it ends - the replay jumps straight there.
func LiveMs(cuts []Cut, contMs int64) int64 {
	live := contMs
	for _, c := range cuts {
		if live < c.FromMs {
			break
		}
		live += c.ToMs - c.FromMs
	}
	return live
}
