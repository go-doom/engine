// Copyright (c) 2026 the go-doom/engine authors.
// SPDX-License-Identifier: GPL-2.0-only

//go:build js && wasm

package wasmbox

import (
	"github.com/wasmdesk/wasmbox/audioring"

	gore "github.com/go-doom/engine"
)

const (
	// ringRate is the relay stream rate: the OPL music module's native rate,
	// so the music reaches the page unresampled (the AudioWorklet resamples
	// to the context rate; effects are resampled by Mixer).
	ringRate = gore.MusicSampleRate
	// ringCapacity is the ring size in frames (a power of two): 93 ms.
	ringCapacity = 4096
	// ringTarget is how much the pump keeps queued: 46 ms, which rides out a
	// slow frame without the latency of a full ring.
	ringTarget = 2048
)

// audioSink is what Frontend needs from an audio back end.
type audioSink interface {
	state() string
	cache(name string, lump []byte)
	play(name string, channel, vol, sep int)
	pumpMusic()
}

// ringAudio relays the engine's sound to the wasmbox compositor page. A worker
// has no AudioContext, so the OPL music and the mixed sound effects are written
// as interleaved float32 PCM into a SharedArrayBuffer ring
// (github.com/wasmdesk/wasmbox/audioring) that the page plays from an
// AudioWorklet. pumpMusic, called once per frame, tops the ring up to ringTarget
// frames.
type ringAudio struct {
	w      *audioring.Writer
	mix    *Mixer
	sounds map[string]Sound
	pcm    []int16
	buf    []float32
	noWr   bool // test hook: __wasmboxNoAudioWrite disables the writer
}

func newRingAudio(windowID int, noWrite bool) *ringAudio {
	w, _, err := audioring.Open(audioring.Options{
		SampleRate: ringRate, Channels: 2, CapacityFrames: ringCapacity, WindowID: windowID,
	})
	if err != nil {
		return nil
	}
	return &ringAudio{
		w: w, mix: NewMixer(ringRate), sounds: map[string]Sound{},
		pcm: make([]int16, 2*musicChunk), buf: make([]float32, 2*musicChunk), noWr: noWrite,
	}
}

// state reports "running" once the page has played frames from the ring, and
// "relay" before that.
func (a *ringAudio) state() string {
	if a.w.Stats().Played > 0 {
		return "running"
	}
	return "relay"
}

func (a *ringAudio) cache(name string, lump []byte) {
	if s, ok := DecodeDMX(lump); ok {
		a.sounds[name] = s
	}
}

func (a *ringAudio) play(name string, channel, vol, sep int) {
	if s, ok := a.sounds[name]; ok {
		a.mix.Play(channel, s, vol, sep)
	}
}

func (a *ringAudio) pumpMusic() {
	for {
		free := ringTarget - a.w.Buffered()
		if free <= 0 {
			return
		}
		n := min(free, musicChunk)
		gore.ReadMusicPCM(a.pcm[:2*n])
		out := a.buf[:2*n]
		for i := range out {
			out[i] = float32(a.pcm[i]) / 32768
		}
		a.mix.Mix(out)
		if a.noWr {
			return
		}
		if a.w.Write(out) == 0 { // ring closed or full: try again next frame
			return
		}
	}
}
