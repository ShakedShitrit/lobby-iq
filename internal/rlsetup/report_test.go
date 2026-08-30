package rlsetup

import (
	"strings"
	"testing"
)

// Installing while playing is an ordinary thing to do, and if the config was
// already right there is nothing for the game to undo. Reporting that as a
// failure would make the installer complain on a perfectly good machine.
func TestRunningGameIsOnlyAProblemWhenSomethingChanged(t *testing.T) {
	for _, tc := range []struct {
		name     string
		report   Report
		wantOK   bool
		wantWarn bool
	}{
		{
			name:   "running, nothing changed",
			report: Report{Running: true},
			wantOK: true,
		},
		{
			name:     "running, a setting changed",
			report:   Report{Running: true, Changes: []Change{{Key: keyPort, From: "1", To: "49123"}}},
			wantOK:   false,
			wantWarn: true,
		},
		{
			name:     "running, file created",
			report:   Report{Running: true, Created: true},
			wantOK:   false,
			wantWarn: true,
		},
		{
			name:   "not running, a setting changed",
			report: Report{Changes: []Change{{Key: keyPort, To: "49123"}}},
			wantOK: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.report.OK(); got != tc.wantOK {
				t.Errorf("OK() = %v, want %v", got, tc.wantOK)
			}
			warned := strings.Contains(tc.report.Summary(), "WARNING")
			if warned != tc.wantWarn {
				t.Errorf("warned = %v, want %v; summary:\n%s",
					warned, tc.wantWarn, tc.report.Summary())
			}
		})
	}
}

// A missing game is not a failure: the config lives under Documents and is
// read whenever the game is next installed and run.
func TestSummaryExplainsAMissingGame(t *testing.T) {
	r := Report{ConfigPath: `C:\somewhere\TAStatsAPI.ini`, Created: true}
	if !r.OK() {
		t.Error("a missing game should not make the setup a failure")
	}
	if !strings.Contains(r.Summary(), "not found") {
		t.Errorf("summary should say the game was not found:\n%s", r.Summary())
	}
}

func TestSummaryNamesTheStoreItFoundTheGameIn(t *testing.T) {
	r := Report{
		ConfigPath: `C:\somewhere\TAStatsAPI.ini`,
		Found:      true,
		Install:    Install{Path: `D:\Games\rocketleague`, Store: "Steam"},
	}
	s := r.Summary()
	if !strings.Contains(s, "Steam") || !strings.Contains(s, `D:\Games\rocketleague`) {
		t.Errorf("summary should name the store and path:\n%s", s)
	}
}

func TestApplyRejectsAnImpossiblePort(t *testing.T) {
	for _, port := range []int{0, -1, 70000} {
		if _, err := Plan(port); err == nil {
			t.Errorf("Plan(%d) should have failed", port)
		}
	}
}
