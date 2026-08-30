# Building LobbyIQ

```
go build
```

That's it. The resulting `lobby-iq.exe` opens the desktop GUI when you
double-click it, with no console window behind it - no flags, no build script.

Three things make that true, and all are easy to undo by accident:

- `internal/startup/subsystem_windows.go` passes `--subsystem,windows` to the
  linker from inside the code, so no console window appears.
- `cmd/root.go` sets `cobra.MousetrapHelpText = ""`. Cobra otherwise detects
  that it was started from Explorer, prints *"This is a command line tool. You
  need to open cmd.exe and run it from there."*, waits five seconds and exits
  without ever running the app. That message goes to a stderr that a GUI
  process doesn't have, so the symptom is simply that nothing happens.

- `resource.syso` in the repository root is a compiled Windows resource holding
  the app icon. The Go toolchain links any `.syso` in the main package
  automatically, which is why no build flag is needed. Delete it and the exe
  gets Windows' blank default icon.

Read the comments at the first two before removing them.

### Rebuilding the icon

Only needed when the source art changes. `resource.syso` is committed so that a
plain `go build` stays enough.

```
go run ./tools/mkico -in assets/brand/emblem.png -out assets/brand/lobbyiq.ico
go run github.com/akavel/rsrc@v0.10.2 -ico assets/brand/lobbyiq.ico -arch amd64 -o resource.syso
```

`tools/mkico` writes the seven sizes Windows asks for, as bitmaps below 128px
and PNG above - see the comments there for why the two differ.

## Building the installer

CI does this on every tag; `.github/workflows/release.yml` is the reference.
Locally you need [Inno Setup 6](https://jrsoftware.org/isdl.php):

```
mkdir dist
go build -o dist/lobby-iq.exe .
iscc /DAppVersion=0.0.0-dev installer\lobbyiq.iss
```

The result lands in `dist\`. The script is deliberately thin - it installs
files, makes shortcuts, and calls `lobby-iq setup` to configure Rocket League.
That command lives in `internal/rlsetup` and is covered by tests, because
installer scripting is a bad place for logic that has to cope with OneDrive
redirection, two different game stores and a config file the game owns.

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
