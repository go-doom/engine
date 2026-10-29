// Copyright (c) 2026 the go-doom/engine authors.
// SPDX-License-Identifier: GPL-2.0-only

package wasmbox

import (
	"bytes"
	"encoding/binary"
	"testing"

	gore "github.com/go-doom/engine"
)

func TestDoomKey(t *testing.T) {
	cases := map[string]uint8{
		"ArrowUp": gore.KEY_UPARROW1, "ArrowDown": gore.KEY_DOWNARROW1,
		"ArrowLeft": gore.KEY_LEFTARROW1, "ArrowRight": gore.KEY_RIGHTARROW1,
		"ControlLeft": gore.KEY_FIRE1, "ControlRight": gore.KEY_FIRE1,
		"Space": gore.KEY_USE1, "Escape": gore.KEY_ESCAPE, "Enter": gore.KEY_ENTER,
		"Tab": gore.KEY_TAB, "Backspace": gore.KEY_BACKSPACE3, "Pause": gore.KEY_PAUSE1,
		"Comma": gore.KEY_STRAFE_L1, "Period": gore.KEY_STRAFE_R1,
		"Equal": gore.KEY_EQUALS1, "Minus": gore.KEY_MINUS1,
		"KeyA": 'a', "KeyZ": 'z', "KeyY": 'y', "Digit0": '0', "Digit9": '9',
		"F1": 0x80 + 0x3b, "F9": 0x80 + 0x43, "F10": 0x80 + 0x44, "F11": 0x80 + 0x57, "F12": 0x80 + 0x58,
		"ShiftLeft": 0x80 + 0x36, "AltRight": 0x80 + 0x38,
	}
	for code, want := range cases {
		got, ok := DoomKey(code)
		if !ok || got != want {
			t.Errorf("%s: got %#x/%v, want %#x", code, got, ok, want)
		}
	}
	for _, code := range []string{"", "Key", "Keya", "Digit", "DigitX", "F0", "F13", "MetaLeft", "Numpad5"} {
		if k, ok := DoomKey(code); ok {
			t.Errorf("%q mapped to %#x, want unmapped", code, k)
		}
	}
}

func TestKeyAndButtonEvents(t *testing.T) {
	ev, ok := KeyEvent("keydown", "ArrowUp")
	if !ok || ev.Type != gore.Ev_keydown || ev.Key != gore.KEY_UPARROW1 {
		t.Fatalf("keydown: %+v %v", ev, ok)
	}
	if ev, ok = KeyEvent("keyup", "KeyY"); !ok || ev.Type != gore.Ev_keyup || ev.Key != 'y' {
		t.Fatalf("keyup: %+v %v", ev, ok)
	}
	if _, ok = KeyEvent("wheel", "ArrowUp"); ok {
		t.Fatal("wheel accepted as a key")
	}
	if _, ok = KeyEvent("keydown", "MetaLeft"); ok {
		t.Fatal("unmapped key accepted")
	}
	if ev, ok = ButtonEvent("mousedown", 0); !ok || ev.Key != gore.KEY_FIRE1 || ev.Type != gore.Ev_keydown {
		t.Fatalf("left button: %+v %v", ev, ok)
	}
	if ev, ok = ButtonEvent("mouseup", 2); !ok || ev.Key != gore.KEY_USE1 || ev.Type != gore.Ev_keyup {
		t.Fatalf("right button: %+v %v", ev, ok)
	}
	if _, ok = ButtonEvent("mousedown", 1); ok {
		t.Fatal("middle button mapped")
	}
	if _, ok = ButtonEvent("mousemove", 0); ok {
		t.Fatal("mousemove mapped")
	}
}

func TestQueueFIFOAndBound(t *testing.T) {
	var q Queue
	var ev gore.DoomEvent
	if q.Pop(&ev) {
		t.Fatal("pop from empty queue")
	}
	for i := 0; i < MaxQueuedEvents+10; i++ {
		q.Push(gore.DoomEvent{Key: uint8(i)})
	}
	if q.Len() != MaxQueuedEvents {
		t.Fatalf("len %d, want %d", q.Len(), MaxQueuedEvents)
	}
	if !q.Pop(&ev) || ev.Key != 10 {
		t.Fatalf("oldest kept is %d, want 10 (the first 10 dropped)", ev.Key)
	}
}

func dmxLump(rate uint16, samples []byte) []byte {
	b := make([]byte, 8, 8+len(samples))
	b[0] = 3
	binary.LittleEndian.PutUint16(b[2:], rate)
	binary.LittleEndian.PutUint32(b[4:], uint32(len(samples)))
	return append(b, samples...)
}

func TestDecodeDMX(t *testing.T) {
	pcm := make([]byte, 100)
	for i := range pcm {
		pcm[i] = byte(i + 100)
	}
	s, ok := DecodeDMX(dmxLump(11025, pcm))
	if !ok || s.Rate != 11025 {
		t.Fatalf("decode: %v rate %d", ok, s.Rate)
	}
	// DMX skips 16 bytes at each end: 100 - 32 = 68 samples, first = pcm[16].
	if len(s.Samples) != 68 {
		t.Fatalf("%d samples, want 68", len(s.Samples))
	}
	if want := (float32(pcm[16]) - 128) / 128; s.Samples[0] != want {
		t.Fatalf("first sample %v, want %v", s.Samples[0], want)
	}
	for _, bad := range [][]byte{
		nil,
		{3, 0, 0x11, 0x2b},               // shorter than the header
		dmxLump(11025, pcm)[:50],         // count beyond the lump
		dmxLump(11025, make([]byte, 48)), // 48 samples: DMX drops it
		append([]byte{2, 0}, dmxLump(11025, pcm)[2:]...), // wrong format
		dmxLump(0, pcm), // zero rate
	} {
		if _, ok := DecodeDMX(bad); ok {
			t.Errorf("accepted %d-byte bad lump", len(bad))
		}
	}
}

func TestPan(t *testing.T) {
	l, r := Pan(127, 128)
	if l < 0.49 || l > 0.51 || r < 0.49 || r > 0.51 {
		t.Fatalf("centre: %v %v", l, r)
	}
	if l, r = Pan(127, 0); l != 254.0/255 || r != 0 {
		t.Fatalf("hard left: %v %v", l, r)
	}
	if l, r = Pan(127, 254); l != 0 || r != 254.0/255 {
		t.Fatalf("hard right: %v %v", l, r)
	}
	if l, r = Pan(0, 128); l != 0 || r != 0 {
		t.Fatalf("silent: %v %v", l, r)
	}
	if l, _ = Pan(127, -300); l != 1 {
		t.Fatalf("clamp: %v", l)
	}
}

func TestScaler(t *testing.T) {
	const sw, sh, dw, dh = 4, 2, 8, 5
	src := make([]byte, sw*sh*4)
	for i := 0; i < sw*sh; i++ {
		binary.LittleEndian.PutUint32(src[i*4:], uint32(i+1))
	}
	dst := make([]byte, dw*dh*4)
	NewScaler(sw, sh, dw, dh).Scale(dst, src)
	at := func(x, y int) uint32 { return binary.LittleEndian.Uint32(dst[(y*dw+x)*4:]) }
	for y := 0; y < dh; y++ {
		for x := 0; x < dw; x++ {
			want := uint32((y*sh/dh)*sw + x*sw/dw + 1)
			if got := at(x, y); got != want {
				t.Fatalf("(%d,%d) = %d, want %d", x, y, got, want)
			}
		}
	}
}

func TestScalerDoomGeometry(t *testing.T) {
	src := bytes.Repeat([]byte{1, 2, 3, 255}, 320*200)
	dst := make([]byte, 640*480*4)
	NewScaler(320, 200, 640, 480).Scale(dst, src)
	if !bytes.Equal(dst, bytes.Repeat([]byte{1, 2, 3, 255}, 640*480)) {
		t.Fatal("uniform frame not preserved")
	}
}

func BenchmarkScale320x200To640x480(b *testing.B) {
	src := make([]byte, 320*200*4)
	dst := make([]byte, 640*480*4)
	s := NewScaler(320, 200, 640, 480)
	b.SetBytes(int64(len(dst)))
	for b.Loop() {
		s.Scale(dst, src)
	}
}
