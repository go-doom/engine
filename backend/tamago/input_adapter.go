// Copyright (c) 2026 cloud-boot contributors
// SPDX-License-Identifier: GPL-2.0-only
//
// This file is part of go-doom/engine, a fork of github.com/AndreRenaud/gore
// (Pure-Go minimal Doom implementation, GPL-2.0). The TamaGo frontend adapter
// itself is NEW code authored for cloud-boot and is released under the same
// license to preserve the engine's GPL boundary; cloud-boot's other components
// remain BSD-3-Clause.

package tamago

// virtioInputEvent mirrors the relevant fields of
// github.com/go-virtio/input.Event so this adapter can be written
// without a hard import of the go-virtio/input package — the real
// driver fills these fields verbatim from the wire-format
// virtio_input_event.
type virtioInputEvent struct {
	Type  uint16 // EV_* class (1 = EvKey)
	Code  uint16 // per-class subcode (Linux evdev code)
	Value uint32 // 0 = up, 1 = down, 2 = repeat
}

// virtioInputReader is the minimal subset of
// github.com/go-virtio/input.VirtioInput's read path the adapter needs.
// `blocking=false` returns ErrEventNotReady immediately when no event
// is queued; the adapter wraps that as a clean (false, nil) so the
// engine's GetEvent stays cheap on the no-input hot path.
type virtioInputReader interface {
	// ReadEventRaw returns the next event's (type, code, value) triple
	// plus a "have event" flag. The wrapper in inputReaderShim adapts
	// the concrete *input.VirtioInput's ReadEvent shape to this trio.
	ReadEventRaw() (uint16, uint16, uint32, bool)
}

// InputAdapter is the Input implementation that drains evdev-shaped
// virtio-input events and surfaces them as HID-usage KeyEvents the
// Frontend's existing hidUsageToDoomKey table understands.
//
// Two translation hops happen here:
//
//  1. evdev code → HID Keyboard usage ID. virtio-input delivers
//     Linux's input-event-codes.h encoding (EV_KEY + KeyUp=103, ...);
//     the Frontend's keymap consumes USB HID usage IDs (0x52 for Up,
//     ...). Mapping inline here keeps the Frontend abstraction the
//     same as it would be for a real USB HID device path.
//
//  2. EvKey value 0/1/2 → DOOM key-down / key-up. Auto-repeat is
//     surfaced as key-down (DOOM's engine handles auto-repeat on its
//     own via gametics).
//
// Non-EvKey events (mouse motion, SYN_REPORT, ...) are skipped — the
// MVP demo runs the engine keyboard-only.
type InputAdapter struct {
	in virtioInputReader
}

// NewInputAdapter wraps the supplied virtio-input device handle.
func NewInputAdapter(in virtioInputReader) *InputAdapter {
	return &InputAdapter{in: in}
}

// Poll drains zero or one virtio-input events and surfaces a single
// translated KeyEvent. Non-EvKey or unmapped events cause Poll to
// return (zero, false) so the engine treats them as a quiet poll.
//
// The MVP is intentionally one-event-per-call to match the existing
// Frontend.GetEvent contract; multi-event batching would require
// queuing on the adapter side and is out of scope for the first viral
// demo.
func (i *InputAdapter) Poll() (KeyEvent, bool) {
	if i == nil || i.in == nil {
		return KeyEvent{}, false
	}
	t, code, value, ok := i.in.ReadEventRaw()
	if !ok {
		return KeyEvent{}, false
	}
	// EV_KEY = 0x01. Other classes (EV_SYN, EV_REL, EV_MSC, ...) are
	// dropped silently.
	if t != 0x01 {
		return KeyEvent{}, false
	}
	hid, mapped := evdevCodeToHIDUsage(code)
	if !mapped {
		return KeyEvent{}, false
	}
	return KeyEvent{
		HIDUsage: hid,
		// Value 0 = up, anything else (1 = down, 2 = repeat) =
		// down. Repeat is folded into down because DOOM's engine
		// generates its own auto-repeat semantics via tic counters.
		Down: value != 0,
	}, true
}

// evdevCodeToHIDUsage maps the Linux input-event-codes.h KEY_* code the
// virtio-input device emits to the USB HID Keyboard usage ID the
// Frontend's keymap (see hidUsageToDoomKey in frontend.go) expects.
//
// Coverage is the minimal set the MVP DOOM bindings need: arrows,
// Enter, Escape, Space (use), Tab (automap), and Left/Right Ctrl
// (fire). Unmapped codes return (0, false); the adapter then drops
// the event silently.
func evdevCodeToHIDUsage(code uint16) (uint16, bool) {
	// evdev codes pulled from input-event-codes.h (Linux 6.x):
	//
	//	KEY_ESC       1
	//	KEY_TAB      15
	//	KEY_ENTER    28
	//	KEY_LEFTCTRL 29
	//	KEY_SPACE    57
	//	KEY_RIGHTCTRL 97
	//	KEY_UP      103
	//	KEY_LEFT    105
	//	KEY_RIGHT   106
	//	KEY_DOWN    108
	//
	// HID Keyboard usage IDs pulled from the HID Usage Tables:
	//
	//	Escape     0x29
	//	Tab        0x2B
	//	Enter      0x28
	//	Space      0x2C
	//	Left Ctrl  0xE0
	//	Right Ctrl 0xE4
	//	Right Arr  0x4F
	//	Left  Arr  0x50
	//	Down  Arr  0x51
	//	Up    Arr  0x52
	switch code {
	case 1: // KEY_ESC
		return 0x29, true
	case 15: // KEY_TAB
		return 0x2B, true
	case 28: // KEY_ENTER
		return 0x28, true
	case 29: // KEY_LEFTCTRL
		return 0xE0, true
	case 57: // KEY_SPACE
		return 0x2C, true
	case 97: // KEY_RIGHTCTRL
		return 0xE4, true
	case 103: // KEY_UP
		return 0x52, true
	case 105: // KEY_LEFT
		return 0x50, true
	case 106: // KEY_RIGHT
		return 0x4F, true
	case 108: // KEY_DOWN
		return 0x51, true
	}
	return 0, false
}

// Compile-time interface conformance assertion — *InputAdapter must
// satisfy the Input contract the Frontend expects.
var _ Input = (*InputAdapter)(nil)
