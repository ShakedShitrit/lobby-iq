//go:build windows

package inputrec

import (
	"slices"

	"go.uber.org/zap"
)

// openSources opens a source for each kind asked for, or every kind when
// kinds is empty. A kind whose system library is missing is skipped.
func openSources(kinds []string) []Source {
	want := func(k string) bool { return len(kinds) == 0 || slices.Contains(kinds, k) }

	var sources []Source
	add := func(s Source) {
		if s != nil {
			sources = append(sources, s)
		}
	}
	if want(ConfigXInput) {
		add(newXInputSource())
	}
	if want(ConfigHID) {
		add(newHIDSource())
	}
	if want(ConfigKBM) {
		add(newKBMSource())
	}
	if len(sources) == 0 {
		zap.L().Warn("no input sources available to record", zap.Strings("asked", kinds))
	}
	return sources
}
