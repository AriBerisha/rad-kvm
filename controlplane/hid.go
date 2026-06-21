package main

// HID bridge: translate input into USB HID gadget reports.
//   keyboard -> /dev/hidg0  (8-byte boot report: [mods][resv][k1..k6])
//   mouse    -> /dev/hidg1  (6 bytes: [buttons][x:0-32767][y][wheel], absolute)
//
// Interactive key/mouse events track held state (h.keys/h.mods). Type() and
// Chord() emit transient raw reports for macros/shortcuts/paste and do not
// disturb the interactive state. Each device write is serialized by h.mu;
// inter-keystroke sleeps happen outside the lock so the mouse stays responsive.

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

type HID struct {
	mu      sync.Mutex
	kbd     *os.File
	mouse   *os.File
	mods    byte
	keys    []byte
	buttons byte
	lastX   uint16
	lastY   uint16
}

func NewHID(kbdDev, mouseDev string) (*HID, error) {
	k, err := os.OpenFile(kbdDev, os.O_WRONLY, 0)
	if err != nil {
		return nil, err
	}
	m, err := os.OpenFile(mouseDev, os.O_WRONLY, 0)
	if err != nil {
		k.Close()
		return nil, err
	}
	return &HID{kbd: k, mouse: m, keys: make([]byte, 0, 6)}, nil
}

func (h *HID) Close() error {
	h.kbd.Close()
	return h.mouse.Close()
}

type inMsg struct {
	T       string   `json:"t"`       // k key, m mouse, w wheel, chord, type
	Code    string   `json:"code"`    // JS KeyboardEvent.code
	Down    bool     `json:"down"`
	X       float64  `json:"x"`       // 0..1 of the video rect
	Y       float64  `json:"y"`
	Buttons int      `json:"buttons"` // JS MouseEvent.buttons
	Dy      int      `json:"dy"`      // wheel delta sign
	Codes   []string `json:"codes"`   // chord: simultaneous keys
	Text    string   `json:"text"`    // type: string to send as keystrokes
}

func (h *HID) Handle(raw []byte) {
	var m inMsg
	if json.Unmarshal(raw, &m) != nil {
		return
	}
	switch m.T {
	case "k":
		h.key(m.Code, m.Down)
	case "m":
		h.mouseAbs(m.X, m.Y, byte(m.Buttons), 0)
	case "w":
		h.wheel(m.Dy)
	case "chord":
		if len(m.Codes) > 0 {
			go h.Chord(m.Codes)
		}
	case "type":
		if m.Text != "" {
			go h.Type(m.Text)
		}
	}
}

// Reset releases all keys/buttons (on connect/disconnect).
func (h *HID) Reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.mods = 0
	h.keys = h.keys[:0]
	h.buttons = 0
	h.kbd.Write(make([]byte, 8))
	h.writeMouse(h.lastX, h.lastY, 0, 0)
}

// --- interactive keyboard ---

func (h *HID) key(code string, down bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if bit, ok := modBits[code]; ok {
		if down {
			h.mods |= bit
		} else {
			h.mods &^= bit
		}
		h.writeKbd()
		return
	}
	usage, ok := keymap[code]
	if !ok {
		return
	}
	if down {
		for _, k := range h.keys {
			if k == usage {
				h.writeKbd()
				return
			}
		}
		if len(h.keys) < 6 {
			h.keys = append(h.keys, usage)
		}
	} else {
		for i, k := range h.keys {
			if k == usage {
				h.keys = append(h.keys[:i], h.keys[i+1:]...)
				break
			}
		}
	}
	h.writeKbd()
}

func (h *HID) writeKbd() { // lock held
	var rep [8]byte
	rep[0] = h.mods
	for i := 0; i < len(h.keys) && i < 6; i++ {
		rep[2+i] = h.keys[i]
	}
	h.kbd.Write(rep[:])
}

// --- interactive mouse (absolute) ---

func (h *HID) mouseAbs(xf, yf float64, buttons byte, wheel int8) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.writeMouse(clampU16(xf), clampU16(yf), buttons, wheel)
}

func (h *HID) wheel(dy int) {
	w := dy
	if w > 127 {
		w = 127
	} else if w < -127 {
		w = -127
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.writeMouse(h.lastX, h.lastY, h.buttons, int8(w))
}

func (h *HID) writeMouse(x, y uint16, buttons byte, wheel int8) { // lock held
	h.lastX, h.lastY, h.buttons = x, y, buttons
	h.mouse.Write([]byte{buttons, byte(x), byte(x >> 8), byte(y), byte(y >> 8), byte(wheel)})
}

func clampU16(f float64) uint16 {
	if f < 0 {
		f = 0
	} else if f > 1 {
		f = 1
	}
	return uint16(f * 32767)
}

// --- transient reports for chords / typing (don't touch interactive state) ---

func (h *HID) writeRaw(mods byte, usages ...byte) {
	var rep [8]byte
	rep[0] = mods
	for i, u := range usages {
		if i < 6 {
			rep[2+i] = u
		}
	}
	h.mu.Lock()
	h.kbd.Write(rep[:])
	h.mu.Unlock()
}

// Chord presses modifiers + keys together (e.g. Ctrl+Alt+Del), then releases.
func (h *HID) Chord(codes []string) {
	var mods byte
	var usages []byte
	for _, c := range codes {
		if b, ok := modBits[c]; ok {
			mods |= b
		} else if u, ok := keymap[c]; ok {
			usages = append(usages, u)
		}
	}
	h.writeRaw(mods, usages...)
	time.Sleep(40 * time.Millisecond)
	h.writeRaw(0)
}

// Type sends a string as keystrokes (US layout). Used by paste + macros.
func (h *HID) Type(text string) {
	for _, r := range text {
		u, shift, ok := charToUsage(r)
		if !ok {
			continue
		}
		var mods byte
		if shift {
			mods = 0x02 // left shift
		}
		h.writeRaw(mods, u)
		time.Sleep(6 * time.Millisecond)
		h.writeRaw(0)
		time.Sleep(6 * time.Millisecond)
	}
}

// --- keymaps (JS KeyboardEvent.code -> USB HID usage) ---

var modBits = map[string]byte{
	"ControlLeft": 0x01, "ShiftLeft": 0x02, "AltLeft": 0x04, "MetaLeft": 0x08,
	"ControlRight": 0x10, "ShiftRight": 0x20, "AltRight": 0x40, "MetaRight": 0x80,
}

var keymap = map[string]byte{
	"KeyA": 0x04, "KeyB": 0x05, "KeyC": 0x06, "KeyD": 0x07, "KeyE": 0x08,
	"KeyF": 0x09, "KeyG": 0x0a, "KeyH": 0x0b, "KeyI": 0x0c, "KeyJ": 0x0d,
	"KeyK": 0x0e, "KeyL": 0x0f, "KeyM": 0x10, "KeyN": 0x11, "KeyO": 0x12,
	"KeyP": 0x13, "KeyQ": 0x14, "KeyR": 0x15, "KeyS": 0x16, "KeyT": 0x17,
	"KeyU": 0x18, "KeyV": 0x19, "KeyW": 0x1a, "KeyX": 0x1b, "KeyY": 0x1c, "KeyZ": 0x1d,
	"Digit1": 0x1e, "Digit2": 0x1f, "Digit3": 0x20, "Digit4": 0x21, "Digit5": 0x22,
	"Digit6": 0x23, "Digit7": 0x24, "Digit8": 0x25, "Digit9": 0x26, "Digit0": 0x27,
	"Enter": 0x28, "Escape": 0x29, "Backspace": 0x2a, "Tab": 0x2b, "Space": 0x2c,
	"Minus": 0x2d, "Equal": 0x2e, "BracketLeft": 0x2f, "BracketRight": 0x30, "Backslash": 0x31,
	"Semicolon": 0x33, "Quote": 0x34, "Backquote": 0x35, "Comma": 0x36, "Period": 0x37, "Slash": 0x38,
	"CapsLock": 0x39,
	"F1": 0x3a, "F2": 0x3b, "F3": 0x3c, "F4": 0x3d, "F5": 0x3e, "F6": 0x3f,
	"F7": 0x40, "F8": 0x41, "F9": 0x42, "F10": 0x43, "F11": 0x44, "F12": 0x45,
	"PrintScreen": 0x46, "ScrollLock": 0x47, "Pause": 0x48, "Insert": 0x49,
	"Home": 0x4a, "PageUp": 0x4b, "Delete": 0x4c, "End": 0x4d, "PageDown": 0x4e,
	"ArrowRight": 0x4f, "ArrowLeft": 0x50, "ArrowDown": 0x51, "ArrowUp": 0x52,
	"NumLock": 0x53, "NumpadDivide": 0x54, "NumpadMultiply": 0x55, "NumpadSubtract": 0x56,
	"NumpadAdd": 0x57, "NumpadEnter": 0x58,
	"Numpad1": 0x59, "Numpad2": 0x5a, "Numpad3": 0x5b, "Numpad4": 0x5c, "Numpad5": 0x5d,
	"Numpad6": 0x5e, "Numpad7": 0x5f, "Numpad8": 0x60, "Numpad9": 0x61, "Numpad0": 0x62,
	"NumpadDecimal": 0x63, "IntlBackslash": 0x64, "ContextMenu": 0x65,
}

type charKey struct {
	code  byte
	shift bool
}

// charMap covers printable ASCII not handled by the a-z/0-9 ranges (US layout).
var charMap = map[rune]charKey{
	' ': {0x2c, false}, '\n': {0x28, false}, '\r': {0x28, false}, '\t': {0x2b, false},
	'-': {0x2d, false}, '_': {0x2d, true}, '=': {0x2e, false}, '+': {0x2e, true},
	'[': {0x2f, false}, '{': {0x2f, true}, ']': {0x30, false}, '}': {0x30, true},
	'\\': {0x31, false}, '|': {0x31, true}, ';': {0x33, false}, ':': {0x33, true},
	'\'': {0x34, false}, '"': {0x34, true}, '`': {0x35, false}, '~': {0x35, true},
	',': {0x36, false}, '<': {0x36, true}, '.': {0x37, false}, '>': {0x37, true},
	'/': {0x38, false}, '?': {0x38, true},
	'!': {0x1e, true}, '@': {0x1f, true}, '#': {0x20, true}, '$': {0x21, true}, '%': {0x22, true},
	'^': {0x23, true}, '&': {0x24, true}, '*': {0x25, true}, '(': {0x26, true}, ')': {0x27, true},
}

func charToUsage(r rune) (usage byte, shift bool, ok bool) {
	switch {
	case r >= 'a' && r <= 'z':
		return byte(0x04 + (r - 'a')), false, true
	case r >= 'A' && r <= 'Z':
		return byte(0x04 + (r - 'A')), true, true
	case r >= '1' && r <= '9':
		return byte(0x1e + (r - '1')), false, true
	case r == '0':
		return 0x27, false, true
	}
	if c, ok := charMap[r]; ok {
		return c.code, c.shift, true
	}
	return 0, false, false
}
