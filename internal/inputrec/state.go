// Package inputrec records what you do on your controller (or keyboard)
// during a Rocket League match, and saves it in a form that can be played
// back in step with the match's saved replay.
//
// The hard part is the "in step". A saved replay leaves out the goal replays
// that run live, so wall-clock time since kickoff 1 drifts further from replay
// time with every goal. What does hold is that, inside one kickoff - from
// "GO" until the next goal - game time and real time advance together, and
// the replay plays back in real time. So the recording is cut into segments,
// one per kickoff, each anchored at its own "GO", and every input is placed
// by its offset from that anchor. See docs/input-recording.md.
package inputrec

import "slices"

// Button bits, laid out as XInput's wButtons so an Xbox pad's state needs no
// translation. Other devices are mapped onto the same positions: a
// PlayStation pad's cross is A, a keyboard has no buttons at all and uses
// State.Keys instead.
const (
	DpadUp        uint32 = 0x0001
	DpadDown      uint32 = 0x0002
	DpadLeft      uint32 = 0x0004
	DpadRight     uint32 = 0x0008
	Start         uint32 = 0x0010
	Back          uint32 = 0x0020
	LeftThumb     uint32 = 0x0040
	RightThumb    uint32 = 0x0080
	LeftShoulder  uint32 = 0x0100
	RightShoulder uint32 = 0x0200
	// Guide and Touchpad sit in bits XInput's public API leaves unused, so
	// they can't collide with anything an Xbox pad reports.
	Guide    uint32 = 0x0400
	Touchpad uint32 = 0x0800
	ButtonA  uint32 = 0x1000
	ButtonB  uint32 = 0x2000
	ButtonX  uint32 = 0x4000
	ButtonY  uint32 = 0x8000
)

// State is one device's input at one instant, in XInput's ranges: sticks
// -32768..32767 with up positive, triggers 0..255.
type State struct {
	Buttons uint32
	LX, LY  int16
	RX, RY  int16
	LT, RT  uint8
	// Keys holds the virtual-key codes held down, ascending. Only keyboard
	// and mouse sources set it.
	Keys []uint16
}

// Equal reports whether two states are the same input.
func (s State) Equal(o State) bool {
	return s.Buttons == o.Buttons &&
		s.LX == o.LX && s.LY == o.LY && s.RX == o.RX && s.RY == o.RY &&
		s.LT == o.LT && s.RT == o.RT &&
		slices.Equal(s.Keys, o.Keys)
}

// Pressed reports whether every bit in mask is held.
func (s State) Pressed(mask uint32) bool { return s.Buttons&mask == mask }

// Device kinds, as they appear in a recording's device list.
const (
	KindXInput    = "xinput"
	KindDS4       = "ds4"
	KindDualSense = "dualsense"
	KindKBM       = "kbm"
	// KindPlayStation is a PlayStation pad that wasn't connected when
	// recording started, so which model it is isn't known.
	KindPlayStation = "playstation"
)

// Source kinds as named in config: "hid" covers both PlayStation pads.
const (
	ConfigXInput = "xinput"
	ConfigHID    = "hid"
	ConfigKBM    = "kbm"
)

// Device describes one input source in a recording. ID is its index in the
// recording's device list, which is what samples refer to it by.
type Device struct {
	ID   int    `json:"id"`
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// Source is one kind of input device that can be polled.
type Source interface {
	// Kind is one of the Kind* constants.
	Kind() string
	// Name describes the device currently being read, for the recording's
	// device list. It may change as devices are connected.
	Name() string
	// Poll returns the current state, and false while no device of this kind
	// is connected.
	Poll() (State, bool)
	// Close releases the device.
	Close()
}
