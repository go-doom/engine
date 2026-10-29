// Copyright (c) 2026 the go-doom/engine authors.
// SPDX-License-Identifier: GPL-2.0-only

package wasmbox

// Doom key codes from doomgeneric's doomkeys.h. The ones the engine exports
// (KEY_ENTER, KEY_FIRE1, ...) are used from gore where they exist; the rest
// are spelled here with the header's own expressions.
const (
	keyRightArrow = 0xae
	keyLeftArrow  = 0xac
	keyUpArrow    = 0xad
	keyDownArrow  = 0xaf
	keyStrafeL    = 0xa0
	keyStrafeR    = 0xa1
	keyUse        = 0xa2
	keyFire       = 0xa3
	keyEscape     = 27
	keyEnter      = 13
	keyTab        = 9
	keyBackspace  = 0x7f
	keyPause      = 0xff
	keyEquals     = 0x3d
	keyMinus      = 0x2d
	keyRShift     = 0x80 + 0x36
	keyRAlt       = 0x80 + 0x38
	keyF1         = 0x80 + 0x3b
	keyF11        = 0x80 + 0x57
	keyF12        = 0x80 + 0x58
)

// named maps DOM KeyboardEvent.code values (layout-independent physical
// keys, https://www.w3.org/TR/uievents-code/) to Doom keys.
var named = map[string]uint8{
	"ArrowUp":      keyUpArrow,
	"ArrowDown":    keyDownArrow,
	"ArrowLeft":    keyLeftArrow,
	"ArrowRight":   keyRightArrow,
	"ControlLeft":  keyFire,
	"ControlRight": keyFire,
	"Space":        keyUse,
	"Escape":       keyEscape,
	"Enter":        keyEnter,
	"NumpadEnter":  keyEnter,
	"Tab":          keyTab,
	"Backspace":    keyBackspace,
	"Pause":        keyPause,
	"ShiftLeft":    keyRShift,
	"ShiftRight":   keyRShift,
	"AltLeft":      keyRAlt,
	"AltRight":     keyRAlt,
	"Comma":        keyStrafeL,
	"Period":       keyStrafeR,
	"Equal":        keyEquals,
	"Minus":        keyMinus,
	"F11":          keyF11,
	"F12":          keyF12,
}

// DoomKey maps a DOM KeyboardEvent.code to the engine's key code. Letters
// and digits map to their lowercase ASCII, which is what the engine expects
// for menus, cheats and savegame names. The second result is false for keys
// Doom has no use for.
func DoomKey(code string) (uint8, bool) {
	if k, ok := named[code]; ok {
		return k, true
	}
	if len(code) == 4 && code[:3] == "Key" && code[3] >= 'A' && code[3] <= 'Z' {
		return code[3] - 'A' + 'a', true
	}
	if len(code) == 6 && code[:5] == "Digit" && code[5] >= '0' && code[5] <= '9' {
		return code[5], true
	}
	if len(code) == 2 && code[0] == 'F' && code[1] >= '1' && code[1] <= '9' {
		return keyF1 + (code[1] - '1'), true
	}
	if code == "F10" {
		return keyF1 + 9, true
	}
	return 0, false
}
