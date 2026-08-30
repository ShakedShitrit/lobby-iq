//go:build !windows

package rlsetup

import "errors"

// errUnsupported is returned everywhere off Windows. Rocket League's Stats API
// is Windows-only, so there is no config to find; these exist so the package
// compiles for anyone building or vetting on another platform, which the rest
// of this repository already supports.
var errUnsupported = errors.New("rocket league configuration is only supported on windows")

func DocumentsDir() (string, error) { return "", errUnsupported }

func ConfigDir() (string, error) { return "", errUnsupported }

func FindRocketLeague() (Install, bool) { return Install{}, false }

func GameRunning() bool { return false }
