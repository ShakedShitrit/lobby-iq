package rlstats

import (
	"encoding/json"
	"os"
	"sync"
	"time"

	"go.uber.org/zap"
)

// dumpLine is one line of an event dump: the envelope as the game sent it,
// plus when it arrived. Data stays a string, exactly as on the wire, so a dump
// can be stripped of At and replayed through Watch the way capture.json is.
type dumpLine struct {
	At    time.Time `json:"At"`
	Event string    `json:"Event"`
	Data  string    `json:"Data"`
}

// EventDump appends every event it is given to a file, one JSON object per
// line. It exists to find out what the game actually sends - which events, in
// what order, how far apart - since the Stats API's documentation doesn't say
// and the input recorder's kickoff alignment depends on it.
type EventDump struct {
	mu   sync.Mutex
	file *os.File
	enc  *json.Encoder
}

// OpenEventDump opens path for appending, creating it if needed.
func OpenEventDump(path string) (*EventDump, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}
	return &EventDump{file: file, enc: json.NewEncoder(file)}, nil
}

// Write records e. Failures are logged rather than returned: a dump is a
// diagnostic, and losing a line of it is no reason to disturb the app.
func (d *EventDump) Write(e Event) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enc.Encode(dumpLine{At: e.At, Event: e.Name, Data: string(e.Data)}); err != nil {
		zap.L().Warn("writing event dump", zap.Error(err))
	}
}

// Close closes the file.
func (d *EventDump) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.file.Close()
}
