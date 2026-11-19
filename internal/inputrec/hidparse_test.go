package inputrec

import "testing"

func TestParsePSReport(t *testing.T) {
	// Each report holds: left stick full left and up, right stick centred,
	// d-pad down-right, cross held, L1 and options held, PS held, L2 at 0x40
	// and R2 fully in.
	ds4USB := make([]byte, 64)
	copy(ds4USB, []byte{0x01, 0x00, 0x00, 0x80, 0x80, 0x23, 0x21, 0x01, 0x40, 0xFF})

	ds4BT := make([]byte, 78)
	copy(ds4BT, []byte{0x11, 0xC0, 0x00, 0x00, 0x00, 0x80, 0x80, 0x23, 0x21, 0x01, 0x40, 0xFF})

	dsUSB := make([]byte, 64)
	copy(dsUSB, []byte{0x01, 0x00, 0x00, 0x80, 0x80, 0x40, 0xFF, 0x07, 0x23, 0x21, 0x01})

	dsBT := make([]byte, 78)
	copy(dsBT, []byte{0x31, 0x10, 0x00, 0x00, 0x80, 0x80, 0x40, 0xFF, 0x07, 0x23, 0x21, 0x01})

	// The reduced report a DualSense sends over Bluetooth before a driver
	// switches it to 0x31: DualShock 4 layout, padded by Windows.
	dsBTSimple := make([]byte, 78)
	copy(dsBTSimple, ds4USB[:10])

	want := State{
		Buttons: DpadDown | DpadRight | ButtonA | LeftShoulder | Start | Guide,
		LX:      -32768, LY: 32767,
		RX: 128, RY: -129,
		LT: 0x40, RT: 0xFF,
	}

	for _, tc := range []struct {
		name      string
		kind      string
		bluetooth bool
		report    []byte
	}{
		{"DS4 USB", KindDS4, false, ds4USB},
		{"DS4 Bluetooth", KindDS4, true, ds4BT},
		{"DualSense USB", KindDualSense, false, dsUSB},
		{"DualSense Bluetooth", KindDualSense, true, dsBT},
		{"DualSense Bluetooth simple", KindDualSense, true, dsBTSimple},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parsePSReport(tc.kind, tc.bluetooth, tc.report)
			if !ok {
				t.Fatal("report not parsed")
			}
			if !got.Equal(want) {
				t.Errorf("got  %+v\nwant %+v", got, want)
			}
		})
	}
}

func TestParsePSReportRejectsOthers(t *testing.T) {
	if _, ok := parsePSReport(KindDS4, false, []byte{0x05, 0, 0}); ok {
		t.Error("parsed an output-style report as input")
	}
	if _, ok := parsePSReport(KindDS4, false, []byte{0x01, 0x80}); ok {
		t.Error("parsed a truncated report")
	}
}
