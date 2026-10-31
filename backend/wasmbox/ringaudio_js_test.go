// Copyright (c) 2026 the go-doom/engine authors.
// SPDX-License-Identifier: GPL-2.0-only

//go:build js && wasm

package wasmbox

import (
	"encoding/binary"
	"syscall/js"
	"testing"
)

// dmx builds a DMX lump of n samples at rate, every sample 255 (full positive).
func dmx(rate, n int) []byte {
	b := make([]byte, 8+n)
	b[0] = 3
	binary.LittleEndian.PutUint16(b[2:], uint16(rate))
	binary.LittleEndian.PutUint32(b[4:], uint32(n))
	for i := 8; i < len(b); i++ {
		b[i] = 255
	}
	return b
}

func TestRingAudioRelay(t *testing.T) {
	g := js.Global()
	g.Call("eval", `globalThis.__msgs = []; globalThis.postMessage = function (m) { globalThis.__msgs.push(m); };`)
	defer g.Call("eval", `delete globalThis.postMessage; delete globalThis.__msgs;`)

	a := newRingAudio(5, false)
	if a == nil {
		t.Fatal("newRingAudio failed")
	}
	m := g.Get("__msgs").Index(0)
	if m.Get("type").String() != "audio_open" || m.Get("sampleRate").Int() != ringRate ||
		m.Get("channels").Int() != 2 || m.Get("capacityFrames").Int() != ringCapacity {
		t.Fatalf("bad audio_open: %v", m)
	}
	if a.state() != "relay" {
		t.Fatalf("state = %q before any playback", a.state())
	}
	a.cache("DSPISTOL", []byte{1, 2, 3}) // not a DMX lump: ignored
	if len(a.sounds) != 0 {
		t.Fatal("bad lump cached")
	}
	a.cache("DSPISTOL", dmx(11025, 400))
	a.play("DSNOSUCH", 0, 127, 127) // unknown sound: ignored
	a.play("DSPISTOL", 0, 127, 127)
	if a.mix.Active() != 1 {
		t.Fatal("sound not started")
	}
	a.pumpMusic()

	hdr := g.Get("Int32Array").New(m.Get("sab"), 0, 16)
	at := g.Get("Atomics")
	if w := at.Call("load", hdr, 0).Int(); w != ringTarget {
		t.Fatalf("pump queued %d frames, want %d", w, ringTarget)
	}
	data := g.Get("Float32Array").New(m.Get("sab"), 64)
	l, _ := Pan(127, 127)
	if got := data.Index(0).Float(); got <= 0 || got > float64(l) {
		t.Fatalf("effect not mixed into the stream: first sample %v", got)
	}
	a.pumpMusic() // already full: nothing more
	if w := at.Call("load", hdr, 0).Int(); w != ringTarget {
		t.Fatalf("second pump wrote: %d", w)
	}
	at.Call("store", hdr, 3, 100) // the page reports frames played
	if a.state() != "running" {
		t.Fatalf("state = %q after playback", a.state())
	}
	at.Call("store", hdr, 5, 1) // closed: Write refuses, pump must return
	at.Call("store", hdr, 1, ringTarget)
	a.pumpMusic()
}

func TestRingAudioNoWrite(t *testing.T) {
	g := js.Global()
	g.Call("eval", `globalThis.__msgs = []; globalThis.postMessage = function (m) { globalThis.__msgs.push(m); };`)
	defer g.Call("eval", `delete globalThis.postMessage; delete globalThis.__msgs;`)
	a := newRingAudio(1, true)
	a.pumpMusic()
	hdr := g.Get("Int32Array").New(g.Get("__msgs").Index(0).Get("sab"), 0, 16)
	if g.Get("Atomics").Call("load", hdr, 0).Int() != 0 {
		t.Fatal("noWrite wrote")
	}
}
