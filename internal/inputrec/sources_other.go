//go:build !windows

package inputrec

// openSources has nothing to open off Windows, where Rocket League's Stats
// API - and so match detection - doesn't exist. It is here so the package
// builds everywhere, as the rest of the repository does.
func openSources([]string) []Source { return nil }
