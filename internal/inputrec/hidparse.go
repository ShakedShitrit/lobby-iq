package inputrec

// Sony's USB vendor ID, and the product IDs of the pads Rocket League
// supports natively.
const (
	sonyVID          = 0x054C
	pidDS4v1         = 0x05C4
	pidDS4v2         = 0x09CC
	pidDS4Dongle     = 0x0BA0
	pidDualSense     = 0x0CE6
	pidDualSenseEdge = 0x0DF2
)

// sonyKind returns the device kind for a Sony product ID, or "" for one
// that isn't a supported pad.
func sonyKind(vid, pid uint16) string {
	if vid != sonyVID {
		return ""
	}
	switch pid {
	case pidDS4v1, pidDS4v2, pidDS4Dongle:
		return KindDS4
	case pidDualSense, pidDualSenseEdge:
		return KindDualSense
	}
	return ""
}

// psLayout locates a report's fields. The DualShock 4 and DualSense put the
// same things in different places, and Bluetooth reports shift them along
// again; once found, the fields themselves mean the same on both pads.
type psLayout struct {
	sticks   int // LX, LY, RX, RY follow in that order
	triggers int // L2, R2
	buttons  int // three bytes: d-pad+face, shoulders+sticks, PS+touchpad
}

var (
	// layoutDS4 is the DualShock 4's report 0x01 over USB, and also the
	// reduced report 0x01 either pad sends over Bluetooth until a driver asks
	// for the full one.
	layoutDS4 = psLayout{sticks: 1, buttons: 5, triggers: 8}
	// layoutDS4BT is the DualShock 4's full Bluetooth report 0x11.
	layoutDS4BT = psLayout{sticks: 3, buttons: 7, triggers: 10}
	// layoutDualSense is the DualSense's report 0x01 over USB.
	layoutDualSense = psLayout{sticks: 1, triggers: 5, buttons: 8}
	// layoutDualSenseBT is the DualSense's full Bluetooth report 0x31.
	layoutDualSenseBT = psLayout{sticks: 2, triggers: 6, buttons: 9}
)

// hatButtons maps the d-pad's hat value, 0 = up clockwise to 7 = up-left,
// to button bits. 8 and above mean released.
var hatButtons = [8]uint32{
	DpadUp,
	DpadUp | DpadRight,
	DpadRight,
	DpadDown | DpadRight,
	DpadDown,
	DpadDown | DpadLeft,
	DpadLeft,
	DpadUp | DpadLeft,
}

// parsePSReport decodes one input report from a DualShock 4 or DualSense
// into a State, mapping each control to the Xbox one in the same position.
// bluetooth says how the pad is connected, which is the only way to tell a
// DualSense's USB report 0x01 from its reduced Bluetooth one - Windows pads
// every report to the same length, so length can't. It returns false for
// reports that carry no input state.
func parsePSReport(kind string, bluetooth bool, report []byte) (State, bool) {
	if len(report) == 0 {
		return State{}, false
	}
	var l psLayout
	switch id := report[0]; {
	case id == 0x01 && kind == KindDualSense && !bluetooth:
		l = layoutDualSense
	case id == 0x01:
		l = layoutDS4
	case id == 0x11 && kind == KindDS4:
		l = layoutDS4BT
	case id == 0x31 && kind == KindDualSense:
		l = layoutDualSenseBT
	default:
		return State{}, false
	}
	if len(report) < max(l.sticks+4, l.triggers+2, l.buttons+3) {
		return State{}, false
	}

	st := State{
		LX: psAxis(report[l.sticks]),
		LY: psAxisInverted(report[l.sticks+1]),
		RX: psAxis(report[l.sticks+2]),
		RY: psAxisInverted(report[l.sticks+3]),
		LT: report[l.triggers],
		RT: report[l.triggers+1],
	}

	b0, b1, b2 := report[l.buttons], report[l.buttons+1], report[l.buttons+2]
	if hat := b0 & 0x0F; hat < 8 {
		st.Buttons |= hatButtons[hat]
	}
	for bit, mask := range map[byte]uint32{
		0x10: ButtonX, // square
		0x20: ButtonA, // cross
		0x40: ButtonB, // circle
		0x80: ButtonY, // triangle
	} {
		if b0&bit != 0 {
			st.Buttons |= mask
		}
	}
	for bit, mask := range map[byte]uint32{
		0x01: LeftShoulder,
		0x02: RightShoulder,
		0x10: Back,  // share / create
		0x20: Start, // options
		0x40: LeftThumb,
		0x80: RightThumb,
	} {
		if b1&bit != 0 {
			st.Buttons |= mask
		}
	}
	if b2&0x01 != 0 {
		st.Buttons |= Guide
	}
	if b2&0x02 != 0 {
		st.Buttons |= Touchpad
	}
	return st, true
}

// psAxis widens a 0..255 axis, centred at 128, to XInput's range.
func psAxis(v byte) int16 { return int16(int(v)*257 - 32768) }

// psAxisInverted is psAxis for a vertical axis: PlayStation pads report down
// as positive, XInput reports up as positive.
func psAxisInverted(v byte) int16 { return int16(32767 - int(v)*257) }
