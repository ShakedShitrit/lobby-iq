package inputview

import (
	"fmt"
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"

	"github.com/ShakedShitrit/lobby-iq/internal/inputrec"
)

// The drawing's fixed size. Everything inside is placed by hand: a
// controller's shape isn't something a layout can produce.
const (
	padW = 440
	padH = 250
)

var (
	colBody  = color.NRGBA{R: 0x2B, G: 0x2F, B: 0x36, A: 0xFF}
	colIdle  = color.NRGBA{R: 0x4A, G: 0x50, B: 0x5A, A: 0xFF}
	colLine  = color.NRGBA{R: 0x8A, G: 0x91, B: 0x9C, A: 0xFF}
	colLabel = color.NRGBA{R: 0xE6, G: 0xE8, B: 0xEB, A: 0xFF}
	colLit   = color.NRGBA{R: 0x3D, G: 0xA5, B: 0xFF, A: 0xFF}
	colBoost = color.NRGBA{R: 0xFF, G: 0x8A, B: 0x1F, A: 0xFF}
)

// faceColors light each face button in its own colour, the way the buttons
// are printed on an Xbox pad, so a glance tells which was pressed.
var faceColors = map[uint32]color.Color{
	inputrec.ButtonA: color.NRGBA{R: 0x4C, G: 0xC2, B: 0x4C, A: 0xFF},
	inputrec.ButtonB: color.NRGBA{R: 0xE5, G: 0x48, B: 0x48, A: 0xFF},
	inputrec.ButtonX: color.NRGBA{R: 0x3D, G: 0x7D, B: 0xFF, A: 0xFF},
	inputrec.ButtonY: color.NRGBA{R: 0xF2, G: 0xC2, B: 0x2E, A: 0xFF},
}

// button is a circle or rectangle that lights while its bit is held.
type button struct {
	mask  uint32
	shape fyne.CanvasObject
	label *canvas.Text
	lit   color.Color
}

func (b *button) set(st inputrec.State) {
	fill := color.Color(colIdle)
	if st.Pressed(b.mask) {
		fill = b.lit
	}
	switch s := b.shape.(type) {
	case *canvas.Circle:
		s.FillColor = fill
	case *canvas.Rectangle:
		s.FillColor = fill
	}
	b.shape.Refresh()
}

// stick is a ring with a dot showing where the stick is pushed. The ring
// fills when the stick is clicked.
type stick struct {
	cx, cy, r float32
	click     uint32
	ring      *canvas.Circle
	dot       *canvas.Circle
}

func (s *stick) set(x, y int16, st inputrec.State) {
	const dotR = 9
	dx := float32(x) / 32768 * s.r
	dy := -float32(y) / 32768 * s.r
	s.dot.Move(fyne.NewPos(s.cx+dx-dotR, s.cy+dy-dotR))
	if st.Pressed(s.click) {
		s.ring.FillColor = colLit
	} else {
		s.ring.FillColor = color.Transparent
	}
	s.ring.Refresh()
	s.dot.Refresh()
}

// trigger is a horizontal bar filled as far as the trigger is pulled.
type trigger struct {
	x, y, w, h float32
	fill       *canvas.Rectangle
	value      *canvas.Text
}

func (t *trigger) set(v uint8) {
	t.fill.Resize(fyne.NewSize(t.w*float32(v)/255, t.h))
	t.fill.Refresh()
	t.value.Text = fmt.Sprintf("%d%%", int(v)*100/255)
	t.value.Refresh()
}

// padView draws a controller and shows a State on it.
type padView struct {
	root     *fyne.Container
	buttons  []*button
	faces    []*button
	sticks   [2]*stick
	triggers [2]*trigger
	touchpad *canvas.Rectangle
}

func newPadView() *padView {
	p := &padView{}
	var objs []fyne.CanvasObject
	add := func(o ...fyne.CanvasObject) { objs = append(objs, o...) }

	place := func(o fyne.CanvasObject, x, y, w, h float32) {
		o.Move(fyne.NewPos(x, y))
		o.Resize(fyne.NewSize(w, h))
		add(o)
	}
	text := func(s string, x, y float32, size float32) *canvas.Text {
		t := canvas.NewText(s, colLabel)
		t.TextSize = size
		t.Alignment = fyne.TextAlignCenter
		place(t, x-20, y-size/2-2, 40, size+4)
		return t
	}

	// Triggers, then bumpers, sit above the body like on the real thing.
	for i, x := range []float32{50, 290} {
		name := []string{"LT", "RT"}[i]
		bg := canvas.NewRectangle(colIdle)
		bg.CornerRadius = 4
		place(bg, x, 8, 100, 16)
		fill := canvas.NewRectangle(colBoost)
		fill.CornerRadius = 4
		place(fill, x, 8, 0, 16)
		text(name, x-18, 16, 11)
		val := text("0%", x+122, 16, 11)
		p.triggers[i] = &trigger{x: x, y: 8, w: 100, h: 16, fill: fill, value: val}
	}

	body := canvas.NewRectangle(colBody)
	body.CornerRadius = 70
	body.StrokeColor = colLine
	body.StrokeWidth = 1
	// Bumpers are drawn before the body's top edge is covered.
	for i, x := range []float32{50, 290} {
		b := canvas.NewRectangle(colIdle)
		b.CornerRadius = 6
		place(b, x, 32, 100, 18)
		lbl := text([]string{"LB", "RB"}[i], x+50, 41, 11)
		p.buttons = append(p.buttons, &button{
			mask:  []uint32{inputrec.LeftShoulder, inputrec.RightShoulder}[i],
			shape: b, label: lbl, lit: colLit,
		})
	}
	// The body is added first in z-order by prepending, so the bumpers,
	// triggers and everything else draw over it.
	body.Move(fyne.NewPos(10, 54))
	body.Resize(fyne.NewSize(padW-20, padH-60))
	objs = append([]fyne.CanvasObject{body}, objs...)

	newStick := func(cx, cy, r float32, click uint32) *stick {
		ring := canvas.NewCircle(color.Transparent)
		ring.StrokeColor = colLine
		ring.StrokeWidth = 2
		place(ring, cx-r, cy-r, 2*r, 2*r)
		dot := canvas.NewCircle(colLabel)
		place(dot, cx-9, cy-9, 18, 18)
		return &stick{cx: cx, cy: cy, r: r, click: click, ring: ring, dot: dot}
	}
	p.sticks[0] = newStick(105, 115, 34, inputrec.LeftThumb)
	p.sticks[1] = newStick(285, 190, 30, inputrec.RightThumb)

	// D-pad.
	for _, d := range []struct {
		mask uint32
		x, y float32
	}{
		{inputrec.DpadUp, 155, 166},
		{inputrec.DpadDown, 155, 202},
		{inputrec.DpadLeft, 137, 184},
		{inputrec.DpadRight, 173, 184},
	} {
		r := canvas.NewRectangle(colIdle)
		r.CornerRadius = 3
		place(r, d.x-9, d.y-9, 18, 18)
		p.buttons = append(p.buttons, &button{mask: d.mask, shape: r, lit: colLit})
	}

	// Menu buttons and the guide/PS button in the middle.
	for _, m := range []struct {
		mask uint32
		x, y float32
		r    float32
	}{
		{inputrec.Back, 185, 105, 8},
		{inputrec.Guide, 220, 85, 11},
		{inputrec.Start, 255, 105, 8},
	} {
		c := canvas.NewCircle(colIdle)
		place(c, m.x-m.r, m.y-m.r, 2*m.r, 2*m.r)
		p.buttons = append(p.buttons, &button{mask: m.mask, shape: c, lit: colLit})
	}
	p.touchpad = canvas.NewRectangle(colIdle)
	p.touchpad.CornerRadius = 5
	place(p.touchpad, 195, 122, 50, 22)
	p.buttons = append(p.buttons, &button{mask: inputrec.Touchpad, shape: p.touchpad, lit: colLit})

	// Face buttons.
	for _, f := range []struct {
		mask uint32
		x, y float32
	}{
		{inputrec.ButtonA, 335, 145},
		{inputrec.ButtonB, 365, 115},
		{inputrec.ButtonX, 305, 115},
		{inputrec.ButtonY, 335, 85},
	} {
		c := canvas.NewCircle(colIdle)
		place(c, f.x-14, f.y-14, 28, 28)
		lbl := text("", f.x, f.y, 13)
		lbl.TextStyle = fyne.TextStyle{Bold: true}
		b := &button{mask: f.mask, shape: c, label: lbl, lit: faceColors[f.mask]}
		p.faces = append(p.faces, b)
		p.buttons = append(p.buttons, b)
	}
	// Labels go on top of their buttons, so move them to the end.
	for _, b := range p.faces {
		for i, o := range objs {
			if o == b.label {
				objs = append(objs[:i], objs[i+1:]...)
				break
			}
		}
		objs = append(objs, b.label)
	}

	frame := canvas.NewRectangle(color.Transparent)
	frame.SetMinSize(fyne.NewSize(padW, padH))
	p.root = container.NewStack(frame, container.NewWithoutLayout(objs...))
	p.setKind(inputrec.KindXInput)
	return p
}

// setKind labels the face buttons the way the device being shown prints
// them.
func (p *padView) setKind(kind string) {
	labels := map[uint32]string{
		inputrec.ButtonA: "A", inputrec.ButtonB: "B", inputrec.ButtonX: "X", inputrec.ButtonY: "Y",
	}
	if isPlayStation(kind) {
		labels = map[uint32]string{
			inputrec.ButtonA: "✕", inputrec.ButtonB: "○", inputrec.ButtonX: "□", inputrec.ButtonY: "△",
		}
	}
	for _, b := range p.faces {
		b.label.Text = labels[b.mask]
		b.label.Refresh()
	}
	if isPlayStation(kind) {
		p.touchpad.Show()
	} else {
		p.touchpad.Hide()
	}
}

func (p *padView) set(st inputrec.State) {
	for _, b := range p.buttons {
		b.set(st)
	}
	p.sticks[0].set(st.LX, st.LY, st)
	p.sticks[1].set(st.RX, st.RY, st)
	p.triggers[0].set(st.LT)
	p.triggers[1].set(st.RT)
}

func isPlayStation(kind string) bool {
	return kind == inputrec.KindDS4 || kind == inputrec.KindDualSense || kind == inputrec.KindPlayStation
}

// actions names what the held inputs do with Rocket League's default
// controller bindings - what was actually *done*, which is the point of
// watching. Rebound controls will read wrong, which is why the raw controller
// is drawn too.
func actions(st inputrec.State) string {
	var a []string
	if st.Pressed(inputrec.ButtonA) {
		a = append(a, "Jump")
	}
	if st.Pressed(inputrec.ButtonB) {
		a = append(a, "Boost")
	}
	if st.Pressed(inputrec.ButtonX) {
		a = append(a, "Powerslide / Air roll")
	}
	if st.Pressed(inputrec.ButtonY) {
		a = append(a, "Ball cam")
	}
	if st.RT > 0 {
		a = append(a, fmt.Sprintf("Throttle %d%%", int(st.RT)*100/255))
	}
	if st.LT > 0 {
		a = append(a, fmt.Sprintf("Reverse %d%%", int(st.LT)*100/255))
	}
	if len(a) == 0 {
		return "—"
	}
	return strings.Join(a, "  ·  ")
}

// keyNames names the virtual-key codes a player is likely to have bound.
// Anything else shows as its code.
var keyNames = map[uint16]string{
	0x01: "Mouse L", 0x02: "Mouse R", 0x04: "Mouse M", 0x05: "Mouse 4", 0x06: "Mouse 5",
	0x08: "Backspace", 0x09: "Tab", 0x0D: "Enter", 0x13: "Pause", 0x14: "Caps",
	0x1B: "Esc", 0x20: "Space", 0x21: "PgUp", 0x22: "PgDn", 0x23: "End", 0x24: "Home",
	0x25: "←", 0x26: "↑", 0x27: "→", 0x28: "↓",
	0xA0: "LShift", 0xA1: "RShift", 0xA2: "LCtrl", 0xA3: "RCtrl", 0xA4: "LAlt", 0xA5: "RAlt",
	0xBA: ";", 0xBB: "=", 0xBC: ",", 0xBD: "-", 0xBE: ".", 0xBF: "/", 0xC0: "`",
}

func keyName(vk uint16) string {
	switch {
	case keyNames[vk] != "":
		return keyNames[vk]
	case vk >= '0' && vk <= '9', vk >= 'A' && vk <= 'Z':
		return string(rune(vk))
	case vk >= 0x70 && vk <= 0x87:
		return fmt.Sprintf("F%d", vk-0x6F)
	case vk >= 0x60 && vk <= 0x69:
		return fmt.Sprintf("Num%d", vk-0x60)
	}
	return fmt.Sprintf("0x%02X", vk)
}

func keysText(st inputrec.State) string {
	if len(st.Keys) == 0 {
		return "—"
	}
	names := make([]string, len(st.Keys))
	for i, k := range st.Keys {
		names[i] = keyName(k)
	}
	return strings.Join(names, "  ")
}
