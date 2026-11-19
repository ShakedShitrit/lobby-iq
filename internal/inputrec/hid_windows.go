//go:build windows

package inputrec

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"go.uber.org/zap"
	"golang.org/x/sys/windows"
)

var (
	hidDLL               = windows.NewLazySystemDLL("hid.dll")
	procHidDGetAttribute = hidDLL.NewProc("HidD_GetAttributes")
	procHidDGetProduct   = hidDLL.NewProc("HidD_GetProductString")
)

// hidInterfaceGUID is GUID_DEVINTERFACE_HID.
var hidInterfaceGUID = windows.GUID{
	Data1: 0x4D1E55B2, Data2: 0xF16F, Data3: 0x11CF,
	Data4: [8]byte{0x88, 0xCB, 0x00, 0x11, 0x11, 0x00, 0x00, 0x30},
}

// bluetoothHIDService appears in the device path of every Bluetooth HID
// device: it is the Bluetooth HID profile's service UUID.
const bluetoothHIDService = "00001124-0000-1000-8000-00805f9b34fb"

// hidRescan is how often to look for a pad while none is open.
const hidRescan = 2 * time.Second

// hidAttributes is HIDD_ATTRIBUTES.
type hidAttributes struct {
	Size          uint32
	VendorID      uint16
	ProductID     uint16
	VersionNumber uint16
}

// hidSource reads a DualShock 4 or DualSense connected directly - the way
// Rocket League itself reads one, without Steam Input or DS4Windows turning
// it into an Xbox pad.
type hidSource struct {
	mu       sync.Mutex
	dev      *hidDevice
	lastScan time.Time
	// kind and name outlive dev, so a pad unplugged mid-match is still
	// described correctly in the recording.
	kind, name string
}

func newHIDSource() Source {
	if hidDLL.Load() != nil {
		return nil
	}
	h := &hidSource{kind: KindPlayStation, name: "PlayStation controller"}
	h.mu.Lock()
	h.scanLocked()
	h.mu.Unlock()
	return h
}

func (h *hidSource) Kind() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.kind
}

func (h *hidSource) Name() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.name
}

func (h *hidSource) Poll() (State, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.dev != nil && !h.dev.alive.Load() {
		h.dev = nil
	}
	if h.dev == nil {
		if time.Since(h.lastScan) < hidRescan {
			return State{}, false
		}
		h.scanLocked()
		if h.dev == nil {
			return State{}, false
		}
	}
	return h.dev.latest()
}

func (h *hidSource) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.dev != nil {
		h.dev.close()
		h.dev = nil
	}
}

// scanLocked opens the first supported pad found, if any.
func (h *hidSource) scanLocked() {
	h.lastScan = time.Now()
	paths, err := windows.CM_Get_Device_Interface_List("", &hidInterfaceGUID, windows.CM_GET_DEVICE_INTERFACE_LIST_PRESENT)
	if err != nil {
		zap.L().Debug("listing HID devices", zap.Error(err))
		return
	}
	for _, path := range paths {
		// Every Sony device path carries the vendor ID one way or another
		// ("vid_054c" over USB, "vid&0002054c" over Bluetooth), which saves
		// opening every keyboard and mouse to ask.
		if !strings.Contains(strings.ToLower(path), "054c") {
			continue
		}
		if dev := openHIDDevice(path); dev != nil {
			h.dev = dev
			h.kind, h.name = dev.kind, dev.name
			zap.L().Info("reading PlayStation controller",
				zap.String("kind", dev.kind), zap.String("name", dev.name), zap.Bool("bluetooth", dev.bluetooth))
			return
		}
	}
}

// hidDevice is one open pad, read by its own goroutine.
type hidDevice struct {
	handle    windows.Handle
	kind      string
	name      string
	bluetooth bool

	mu    sync.Mutex
	state State
	have  bool

	alive   atomic.Bool
	closing atomic.Bool
}

func openHIDDevice(path string) *hidDevice {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil
	}
	// Shared, because the game (or Steam) has the pad open too. Read-only is
	// tried second: some drivers refuse write access to a second opener, and
	// reading is all this needs.
	var handle windows.Handle
	for _, access := range []uint32{windows.GENERIC_READ | windows.GENERIC_WRITE, windows.GENERIC_READ} {
		handle, err = windows.CreateFile(p, access,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil
	}

	attrs := hidAttributes{Size: uint32(unsafe.Sizeof(hidAttributes{}))}
	if ok, _, _ := procHidDGetAttribute.Call(uintptr(handle), uintptr(unsafe.Pointer(&attrs))); ok == 0 {
		windows.CloseHandle(handle)
		return nil
	}
	kind := sonyKind(attrs.VendorID, attrs.ProductID)
	if kind == "" {
		windows.CloseHandle(handle)
		return nil
	}

	d := &hidDevice{
		handle:    handle,
		kind:      kind,
		name:      hidProductName(handle, kind),
		bluetooth: strings.Contains(strings.ToLower(path), bluetoothHIDService),
	}
	d.alive.Store(true)
	go d.run()
	return d
}

func hidProductName(handle windows.Handle, kind string) string {
	buf := make([]uint16, 128)
	if ok, _, _ := procHidDGetProduct.Call(uintptr(handle), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)*2)); ok != 0 {
		if name := windows.UTF16ToString(buf); name != "" {
			return name
		}
	}
	if kind == KindDualSense {
		return "DualSense"
	}
	return "DualShock 4"
}

// run reads reports until the pad goes away or close is called. The pads
// send reports continuously, so a read never blocks for long and closing
// takes effect on the next one.
func (d *hidDevice) run() {
	defer func() {
		d.alive.Store(false)
		windows.CloseHandle(d.handle)
	}()
	buf := make([]byte, 1024)
	for !d.closing.Load() {
		var n uint32
		if err := windows.ReadFile(d.handle, buf, &n, nil); err != nil {
			if !d.closing.Load() {
				zap.L().Info("PlayStation controller stopped reporting", zap.Error(err))
			}
			return
		}
		if st, ok := parsePSReport(d.kind, d.bluetooth, buf[:n]); ok {
			d.mu.Lock()
			d.state, d.have = st, true
			d.mu.Unlock()
		}
	}
}

func (d *hidDevice) latest() (State, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state, d.have
}

func (d *hidDevice) close() {
	d.closing.Store(true)
	// Unblocks a read in progress rather than waiting for the next report.
	_ = windows.CancelIoEx(d.handle, nil)
}
