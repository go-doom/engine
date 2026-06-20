// Copyright (c) 2026 cloud-boot contributors
// SPDX-License-Identifier: GPL-2.0-only
//
// This file is part of go-doom/engine, a fork of github.com/AndreRenaud/gore
// (Pure-Go minimal Doom implementation, GPL-2.0). The TamaGo frontend adapter
// itself is NEW code authored for cloud-boot and is released under the same
// license to preserve the engine's GPL boundary; cloud-boot's other components
// remain BSD-3-Clause.

package tamago

// virtioSound is the minimal subset of github.com/go-virtio/sound's
// *VirtioSound the adapter needs. Defining it as a local interface keeps
// this file decoupled from a compile-time import of go-virtio/sound — the
// concrete *sound.VirtioSound satisfies it via duck typing.
type virtioSound interface {
	// Write enqueues one PCM transfer onto the device's tx virtqueue
	// and busy-polls for completion. `frames` is raw S16_LE samples.
	Write(streamID uint32, frames []byte) (int, error)
}

// SoundAdapter is the Sound implementation that wraps a virtio-sound
// device. The device must already be brought up + the target stream
// PCMSetParams/PCMPrepare/PCMStart'd by the caller — the adapter only
// runs the per-SFX format conversion and txq Write.
//
// Format bridge: DOOM dmx lumps (the SFX format in DOOM's IWAD) are
// stored as 11025 Hz mono 8-bit unsigned PCM, with an 8-byte header
// (le16 format=3, le16 rate, le32 numSamples). virtio-sound on the
// MVP single-jack baseline accepts only PCMFmtS16 (16-bit signed
// little-endian). We expand u8 → s16 at Cache time and keep the
// re-sampled bytes ready for Play.
//
// The MVP does NOT mix concurrent channels — Play just writes the
// cached samples to the device's single stream. That matches DOOM's
// behaviour when the SDL backend is missing channels too; the first
// in-flight sound wins and the rest queue serially. A proper mixer is
// follow-up work; the demo's goal is engine-runs proof, not audio
// fidelity.
type SoundAdapter struct {
	snd      virtioSound
	streamID uint32
	cache    map[string][]byte
}

// NewSoundAdapter returns an adapter targeting the supplied virtio-sound
// device and stream ID. The caller is responsible for transitioning the
// stream to RUNNING (PCMSetParams → PCMPrepare → PCMStart) before the
// first Play call.
func NewSoundAdapter(snd virtioSound, streamID uint32) *SoundAdapter {
	return &SoundAdapter{
		snd:      snd,
		streamID: streamID,
		cache:    make(map[string][]byte),
	}
}

// Cache converts a DOOM dmx PCM lump (u8 11025 Hz mono, 8-byte header)
// to S16_LE samples and stores them under `name`.
//
// If the slice is shorter than the 8-byte header, Cache stashes an
// empty entry — Play(name) becomes a no-op. The driver also tolerates
// nil entirely.
func (s *SoundAdapter) Cache(name string, data []byte) error {
	if s == nil {
		return nil
	}
	pcm := convertDMXToS16LE(data)
	s.cache[name] = pcm
	return nil
}

// Play writes the cached PCM for `name` to the virtio-sound device.
// Unknown names are silent (no error).
//
// `vol` and `sep` are DOOM's 0..127 mixer values; the MVP path ignores
// both, leaving global volume to the device's default and stereo
// separation at center. A proper attenuation pass is follow-up work.
func (s *SoundAdapter) Play(name string, channel, vol, sep int) error {
	_ = channel
	_ = vol
	_ = sep
	if s == nil || s.snd == nil {
		return nil
	}
	pcm := s.cache[name]
	if len(pcm) == 0 {
		return nil
	}
	_, err := s.snd.Write(s.streamID, pcm)
	return err
}

// convertDMXToS16LE expands a DOOM dmx u8 PCM lump to S16_LE samples.
//
// dmx lump layout (id Software's 1993 format):
//
//	le16 format    = 3            // signed-id for "DMX"
//	le16 rate      = 11025        // samples per second
//	le32 numSamples
//	byte[numSamples] u8 PCM
//
// Older WADs sometimes also include a 16-byte trailing padding region
// after the samples, which we ignore.
//
// Returns nil if data is malformed or empty.
func convertDMXToS16LE(data []byte) []byte {
	const headerSize = 8
	if len(data) < headerSize {
		return nil
	}
	numSamples := uint32(data[4]) | uint32(data[5])<<8 |
		uint32(data[6])<<16 | uint32(data[7])<<24
	avail := uint32(len(data) - headerSize)
	if numSamples > avail {
		numSamples = avail
	}
	if numSamples == 0 {
		return nil
	}
	out := make([]byte, int(numSamples)*2)
	src := data[headerSize : headerSize+int(numSamples)]
	for i, b := range src {
		// u8 PCM: range 0..255, midpoint 128.
		// s16 PCM: range -32768..32767, midpoint 0.
		// Map: s16 = (u8 - 128) << 8.
		v := int16(int32(b)-128) << 8
		out[2*i+0] = byte(uint16(v) & 0xFF)
		out[2*i+1] = byte(uint16(v) >> 8)
	}
	return out
}

// Compile-time interface conformance assertion — *SoundAdapter must
// satisfy the Sound contract the Frontend expects.
var _ Sound = (*SoundAdapter)(nil)
