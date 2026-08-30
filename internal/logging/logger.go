// Package logging configures the global zap logger.
package logging

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var levels = map[string]zapcore.Level{
	"debug": zapcore.DebugLevel,
	"info":  zapcore.InfoLevel,
	"warn":  zapcore.WarnLevel,
	"error": zapcore.ErrorLevel,
}

const logFileName = "lobby-iq.log"

// Init configures the global zap logger, writing to lobby-iq.log rather
// than stdout/stderr since LobbyIQ renders a full-screen TUI that direct
// console writes would corrupt.
//
// It never fails. Not being able to write a log file is no reason to refuse to
// start - least of all for a double-clicked app, where the error would be the
// only thing the user ever saw.
func Init(level string) *zap.Logger {
	lvl, ok := levels[level]
	if !ok {
		lvl = zapcore.InfoLevel
	}

	encoder := zapcore.NewConsoleEncoder(zap.NewDevelopmentEncoderConfig())

	var sink zapcore.WriteSyncer = zapcore.AddSync(io.Discard)
	if file, path := openLogFile(); file != nil {
		sink = zapcore.AddSync(file)
		defer func() { zap.L().Debug("logging to file", zap.String("path", path)) }()
	}

	logger := zap.New(zapcore.NewCore(encoder, sink, lvl), zap.AddCaller())
	zap.ReplaceGlobals(logger)
	return logger
}

// Note appends a line to the log file without needing Init to have run.
//
// It exists for failures that happen before the logger is configured - the
// config file failing to parse, say. Double-clicked there is no console for
// such an error to appear on, so without this it would leave no trace at all
// and the app would just seem to do nothing.
func Note(message string) {
	file, _ := openLogFile()
	if file == nil {
		return
	}
	defer file.Close()

	fmt.Fprintf(file, "%s\t%s\n", time.Now().Format(time.RFC3339), message)
}

// OpenForStdio returns a handle on the log file for use as stdout/stderr when
// there is no console. The caller owns it for the life of the process.
func OpenForStdio() *os.File {
	file, _ := openLogFile()
	return file
}

// openLogFile tries the working directory first, so a run from a checkout
// leaves the log where you'd expect it, then falls back to somewhere that is
// writable no matter where the exe was launched from.
func openLogFile() (*os.File, string) {
	candidates := []string{logFileName}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), logFileName))
	}
	candidates = append(candidates, filepath.Join(os.TempDir(), logFileName))

	for _, path := range candidates {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err == nil {
			return file, path
		}
	}
	return nil, ""
}
