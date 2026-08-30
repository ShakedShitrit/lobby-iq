# Building LobbyIQ

```
go build
```

That's it. The resulting `lobby-iq.exe` opens the desktop GUI when you
double-click it, with no console window behind it - no flags, no build script.

Two things make that true, and both are easy to undo by accident:

- `internal/startup/subsystem_windows.go` passes `--subsystem,windows` to the
  linker from inside the code, so no console window appears.
- `cmd/root.go` sets `cobra.MousetrapHelpText = ""`. Cobra otherwise detects
  that it was started from Explorer, prints *"This is a command line tool. You
  need to open cmd.exe and run it from there."*, waits five seconds and exits
  without ever running the app. That message goes to a stderr that a GUI
  process doesn't have, so the symptom is simply that nothing happens.

Read the comments at both before removing them.

## Running from a terminal

A GUI-subsystem process starts with no console, which would normally mean
`--help` printing into nowhere. LobbyIQ attaches to the console it was
launched from at startup, so

```
lobby-iq.exe --help
lobby-iq.exe --lightweight
```

both behave normally, and `lobby-iq.exe --help > out.txt` still writes to the
file rather than the screen.

Two Windows quirks worth knowing:

- PowerShell does not wait for a GUI-subsystem program, so your prompt returns
  immediately and output arrives after it. `Start-Process -Wait` waits properly
  if that matters.
- Launched with `--lightweight` from a shortcut there is no console to attach
  to, so LobbyIQ allocates its own window for the terminal UI.

## Where it looks for things

`config.yaml` is read from the working directory first, then from the folder
containing the exe - so a desktop shortcut finds its config regardless of what
its "Start in" is set to. `lobby-iq.log` follows the same order, falling back
to the temp directory; if no log file can be opened the app starts anyway
rather than failing over logging.
