// Copyright (c) 2026 the go-doom/engine authors.
// SPDX-License-Identifier: GPL-2.0-only

//go:build js && wasm

package wasmbox

import (
	"syscall/js"
	"unsafe"

	gore "github.com/go-doom/engine"
)

// musicLead is how far ahead of the AudioContext clock music is scheduled.
const musicLead = 0.2 // seconds

// musicChunk is the number of stereo frames synthesised per scheduled buffer.
const musicChunk = 2048

// webAudio plays the engine's sound effects and OPL music through a
// worker-scope AudioContext, the same route go-quake1's wasmbox backend
// takes. Browsers create the context suspended until the page has had a
// user gesture; it is resumed whenever it is found suspended. When the
// worker has no AudioContext at all, newWebAudio returns nil and the game
// runs silent.
type webAudio struct {
	ctx      js.Value
	dest     js.Value
	sounds   map[string]js.Value // name -> AudioBuffer
	channels map[int]js.Value    // engine channel -> playing source
	playhead float64
	pcm      []int16
	left     []float32
	right    []float32
}

func newWebAudio() *webAudio {
	ctor := js.Global().Get("AudioContext")
	if !ctor.Truthy() {
		ctor = js.Global().Get("webkitAudioContext")
	}
	if !ctor.Truthy() {
		return nil
	}
	ctx := ctor.New()
	a := &webAudio{
		ctx:      ctx,
		dest:     ctx.Get("destination"),
		sounds:   map[string]js.Value{},
		channels: map[int]js.Value{},
		pcm:      make([]int16, 2*musicChunk),
		left:     make([]float32, musicChunk),
		right:    make([]float32, musicChunk),
	}
	a.resume()
	return a
}

func (a *webAudio) state() string {
	if s := a.ctx.Get("state"); s.Type() == js.TypeString {
		return s.String()
	}
	return ""
}

func (a *webAudio) resume() {
	if a.ctx.Get("resume").Type() == js.TypeFunction {
		a.ctx.Call("resume")
	}
}

func (a *webAudio) cache(name string, lump []byte) {
	s, ok := DecodeDMX(lump)
	if !ok {
		return
	}
	buf := a.ctx.Call("createBuffer", 1, len(s.Samples), s.Rate)
	buf.Call("copyToChannel", float32Array(s.Samples), 0)
	a.sounds[name] = buf
}

func (a *webAudio) play(name string, channel, vol, sep int) {
	buf, ok := a.sounds[name]
	if !ok {
		return
	}
	if a.state() == "suspended" {
		a.resume()
	}
	if old, ok := a.channels[channel]; ok {
		old.Call("stop")
		delete(a.channels, channel)
	}
	l, r := Pan(vol, sep)
	src := a.ctx.Call("createBufferSource")
	src.Set("buffer", buf)
	merger := a.ctx.Call("createChannelMerger", 2)
	gl := a.ctx.Call("createGain")
	gl.Get("gain").Set("value", l)
	gr := a.ctx.Call("createGain")
	gr.Get("gain").Set("value", r)
	src.Call("connect", gl)
	src.Call("connect", gr)
	gl.Call("connect", merger, 0, 0)
	gr.Call("connect", merger, 0, 1)
	merger.Call("connect", a.dest)
	src.Call("start")
	a.channels[channel] = src
}

// pumpMusic keeps musicLead seconds of OPL music scheduled. It does nothing
// while the context is not running, so a page nobody has clicked does not
// pay for synthesis it cannot play.
func (a *webAudio) pumpMusic() {
	if a.state() != "running" {
		return
	}
	now := a.ctx.Get("currentTime").Float()
	if a.playhead < now {
		a.playhead = now
	}
	for a.playhead < now+musicLead {
		n := gore.ReadMusicPCM(a.pcm)
		if n <= 0 {
			return
		}
		for i := 0; i < n; i++ {
			a.left[i] = float32(a.pcm[2*i]) / 32768
			a.right[i] = float32(a.pcm[2*i+1]) / 32768
		}
		buf := a.ctx.Call("createBuffer", 2, n, gore.MusicSampleRate)
		buf.Call("copyToChannel", float32Array(a.left[:n]), 0)
		buf.Call("copyToChannel", float32Array(a.right[:n]), 1)
		src := a.ctx.Call("createBufferSource")
		src.Set("buffer", buf)
		src.Call("connect", a.dest)
		src.Call("start", a.playhead)
		a.playhead += float64(n) / gore.MusicSampleRate
	}
}

// float32Array copies samples into a new JS Float32Array (wasm memory is
// little-endian, as Float32Array is on every platform browsers run on).
func float32Array(s []float32) js.Value {
	u8 := js.Global().Get("Uint8Array").New(4 * len(s))
	if len(s) > 0 {
		js.CopyBytesToJS(u8, unsafe.Slice((*byte)(unsafe.Pointer(&s[0])), 4*len(s)))
	}
	return js.Global().Get("Float32Array").New(u8.Get("buffer"))
}
