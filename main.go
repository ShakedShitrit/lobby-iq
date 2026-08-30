package main

import (
	"fmt"
	"os"
	"runtime/debug"

	"github.com/ShakedShitrit/lobby-iq/cmd"
	"github.com/ShakedShitrit/lobby-iq/internal/logging"
	"github.com/ShakedShitrit/lobby-iq/internal/startup"
)

func main() {
	// A marker written before anything else can fail, so "the app did
	// nothing" can be told apart from "the app never started". If this line
	// is absent after a launch, the process never reached main and the cause
	// is outside this program.
	logging.Note(bootLine())

	// A panic in a GUI-subsystem process writes its stack to a stderr that
	// goes nowhere, so the app just vanishes. Catch it and put it in the log
	// where it can actually be read.
	defer func() {
		if r := recover(); r != nil {
			logging.Note(fmt.Sprintf("PANIC: %v\n%s", r, debug.Stack()))
			startup.ReportFatal("LobbyIQ crashed", fmt.Sprintf("%v\n\nSee lobby-iq.log for details.", r))
			os.Exit(2)
		}
	}()

	// Reconnect to the terminal we were launched from, if any, before cobra
	// has a chance to write to stdout. Double-clicked, there is nothing to
	// attach to and the GUI just opens.
	if !startup.AttachParent() {
		// No console to attach to, so send stdout/stderr to the log file
		// rather than letting them vanish into null handles.
		startup.RedirectStdio(logging.OpenForStdio())
	}

	if err := cmd.Execute(); err != nil {
		// To the log file as well as the screen: a message box is gone the
		// moment it's dismissed, and double-clicked it's the only sign
		// anything went wrong.
		logging.Note("startup failed: " + err.Error())
		startup.ReportFatal("LobbyIQ", err.Error())
		os.Exit(1)
	}
}

// bootLine describes how this process was launched, which is the information
// missing when an app started from Explorer misbehaves.
func bootLine() string {
	exe, _ := os.Executable()
	cwd, _ := os.Getwd()
	return fmt.Sprintf("boot pid=%d exe=%q cwd=%q args=%v", os.Getpid(), exe, cwd, os.Args[1:])
}
