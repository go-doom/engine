// Copyright (c) 2026 the go-doom/engine authors.
// SPDX-License-Identifier: GPL-2.0-only

package wasmbox

import "encoding/binary"

// Sound is one decoded sound effect: mono samples in [-1, 1] at Rate Hz.
type Sound struct {
	Rate    int
	Samples []float32
}

// DecodeDMX decodes a DMX sound lump (the "DS*" lumps), following
// chocolate-doom's CacheSFX (src/i_sdlsound.c): a format word 0x0003, a
// 16-bit sample rate, a 32-bit sample count, then unsigned 8-bit samples of
// which DMX skips the first and last 16 bytes. Lumps that fail the same
// checks CacheSFX makes (shorter than the header, wrong format, a count
// beyond the lump, 48 samples or fewer) are rejected, as DMX does.
func DecodeDMX(lump []byte) (Sound, bool) {
	if len(lump) < 8 || lump[0] != 0x03 || lump[1] != 0x00 {
		return Sound{}, false
	}
	rate := int(binary.LittleEndian.Uint16(lump[2:4]))
	length := uint64(binary.LittleEndian.Uint32(lump[4:8]))
	if length > uint64(len(lump)-8) || length <= 48 || rate == 0 {
		return Sound{}, false
	}
	pcm := lump[8+16 : 8+length-16]
	out := make([]float32, len(pcm))
	for i, b := range pcm {
		out[i] = (float32(b) - 128) / 128
	}
	return Sound{Rate: rate, Samples: out}, true
}

// Pan converts the engine's volume (0..127) and stereo separation (0..254,
// 128 = centre) to left and right gains in [0, 1], with chocolate-doom's
// I_SDL_UpdateSoundParams formula: left = (254-sep)*vol/127, right =
// sep*vol/127, each clamped to 0..255.
func Pan(vol, sep int) (left, right float32) {
	l := (254 - sep) * vol / 127
	r := sep * vol / 127
	return float32(clamp255(l)) / 255, float32(clamp255(r)) / 255
}

func clamp255(v int) int {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
}
