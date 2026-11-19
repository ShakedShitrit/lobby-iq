package app

import (
	"path/filepath"
	"time"

	"go.uber.org/zap"

	"github.com/ShakedShitrit/lobby-iq/internal/config"
	"github.com/ShakedShitrit/lobby-iq/internal/inputrec"
	"github.com/ShakedShitrit/lobby-iq/internal/rlsetup"
	"github.com/ShakedShitrit/lobby-iq/internal/rlstats"
)

// replayCheckInterval is how often the game's replay folder is checked for
// newly saved replays. A replay is saved by hand, so a few seconds' delay
// in keeping its recording is never noticed.
const replayCheckInterval = 5 * time.Second

// startRecorder returns the input recorder, or nil when recording is off -
// a nil recorder accepts everything and does nothing. onKept hears of each
// recording kept because its replay was saved.
func startRecorder(cfg *config.Config, onKept func(path string, replay inputrec.ReplayInfo)) *inputrec.Recorder {
	if !cfg.InputRecording.Enabled {
		return nil
	}
	opts := RecorderOptions(cfg)
	rec := inputrec.New(opts)
	zap.L().Info("input recording on",
		zap.String("dir", rec.Dir()), zap.Int("keep", cfg.InputRecording.Keep),
		zap.String("replays", opts.ReplaysDir))
	// Runs for the life of the app, so there's nothing to stop it with.
	go rec.Store().Watch(replayCheckInterval, nil, onKept)
	return rec
}

// RecorderOptions translates the config into the recorder's options. The
// viewer uses it too, to find recordings where the recorder put them.
func RecorderOptions(cfg *config.Config) inputrec.Options {
	ir := cfg.InputRecording
	return inputrec.Options{
		Dir:        ir.Dir,
		Keep:       ir.Keep,
		ReplaysDir: replaysDir(ir.ReplaysDir),
		PollHz:     ir.PollHz,
		Devices:    ir.DeviceList(),
	}
}

// Store opens the recordings store the config describes, bringing recordings
// from older versions into its layout.
func Store(cfg *config.Config) *inputrec.Store {
	opts := RecorderOptions(cfg)
	dir := opts.Dir
	if dir == "" {
		dir = inputrec.DefaultDir
	}
	store := inputrec.NewStore(dir, opts.Keep, opts.ReplaysDir)
	store.Migrate()
	return store
}

// replaysDir is where Rocket League saves replays: configured, or the
// game's Demos folder beside its config.
func replaysDir(configured string) string {
	if configured != "" {
		return configured
	}
	docs, err := rlsetup.DocumentsDir()
	if err != nil {
		zap.L().Warn("finding the replays folder; recordings won't be kept by replay", zap.Error(err))
		return ""
	}
	return filepath.Join(docs, "My Games", "Rocket League", "TAGame", "Demos")
}

// replayLabel names a saved replay for a status line: by the name it was
// saved under, or its file when it has none.
func replayLabel(r inputrec.ReplayInfo) string {
	if r.Name != "" {
		return "“" + r.Name + "”"
	}
	return r.File
}

// eventSink builds the raw-event callback for rlstats.WatchEvents: the
// recorder needs kickoffs and goals, dump_events wants everything, and an
// open input viewer follows replays through bus (which may be nil). It
// returns nil when none of them is in use, and a close func for the dump.
func eventSink(cfg *config.Config, rec *inputrec.Recorder, bus *rlstats.Broadcaster) (func(rlstats.Event), func()) {
	var dump *rlstats.EventDump
	if cfg.DumpEvents != "" {
		d, err := rlstats.OpenEventDump(cfg.DumpEvents)
		if err != nil {
			zap.L().Warn("opening event dump", zap.String("path", cfg.DumpEvents), zap.Error(err))
		} else {
			zap.L().Info("dumping Stats API events", zap.String("path", cfg.DumpEvents))
			dump = d
		}
	}
	if rec == nil && dump == nil && bus == nil {
		return nil, func() {}
	}

	sink := func(e rlstats.Event) {
		if dump != nil {
			dump.Write(e)
		}
		rec.OnEvent(e)
		if bus != nil {
			bus.Publish(e)
		}
	}
	closeDump := func() {
		if dump != nil {
			dump.Close()
		}
	}
	return sink, closeDump
}
