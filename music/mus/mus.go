// SPDX-License-Identifier: GPL-2.0-or-later
// Go port of chocolate-doom mus2mid.c (Ben Ryves 2006, Simon Howard,
// id Software). Converts a DMX MUS lump (the D_* music lumps) into a
// single-track type-0 Standard MIDI File, byte-for-byte identical to
// chocolate-doom's mus2mid(). Ported for the go-doom/engine authors.
//
// Pure Go, CGO=0. The output of Convert is intended to be handed to the
// MIDI-driven OPL player, exactly as chocolate-doom does.

// Package mus parses the DMX MUS music format used by DOOM's D_* lumps and
// converts it to a Standard MIDI File, matching chocolate-doom's mus2mid
// conversion byte-for-byte.
package mus

import (
	"encoding/binary"
	"errors"
)

// MUS event codes (high nibble group, bits 4-6 of the event descriptor).
const (
	musReleaseKey       = 0x00
	musPressKey         = 0x10
	musPitchWheel       = 0x20
	musSystemEvent      = 0x30
	musChangeController = 0x40
	musScoreEnd         = 0x60
)

// MIDI channel voice message status bytes.
const (
	midiReleaseKey       = 0x80
	midiPressKey         = 0x90
	midiChangeController = 0xB0
	midiChangePatch      = 0xC0
	midiPitchWheel       = 0xE0
)

const (
	numChannels        = 16
	midiPercussionChan = 9
	musPercussionChan  = 15
)

// controllerMap maps MUS controller numbers to MIDI controller numbers.
var controllerMap = [15]byte{
	0x00, 0x20, 0x01, 0x07, 0x0A, 0x0B, 0x5B, 0x5D,
	0x40, 0x43, 0x78, 0x7B, 0x7E, 0x7F, 0x79,
}

// Standard MIDI type-0 header + track header. The last 4 bytes are a
// placeholder for the track length, patched in after conversion.
var midiHeader = []byte{
	'M', 'T', 'h', 'd',
	0x00, 0x00, 0x00, 0x06,
	0x00, 0x00,
	0x00, 0x01,
	0x00, 0x46,
	'M', 'T', 'r', 'k',
	0x00, 0x00, 0x00, 0x00,
}

// Errors returned by Convert.
var (
	ErrShortHeader = errors.New("mus: truncated MUS header")
	ErrConvert     = errors.New("mus: malformed MUS score")
)

// Header is the parsed MUS lump header.
type Header struct {
	ID                [4]byte
	ScoreLength       uint16
	ScoreStart        uint16
	PrimaryChannels   uint16
	SecondaryChannels uint16
	InstrumentCount   uint16
}

// IsMUS reports whether b begins with the DMX MUS magic ("MUS\x1a").
func IsMUS(b []byte) bool {
	return len(b) >= 4 && b[0] == 'M' && b[1] == 'U' && b[2] == 'S' && b[3] == 0x1A
}

// ParseHeader reads the 14-byte MUS header from b.
func ParseHeader(b []byte) (Header, error) {
	var h Header
	if len(b) < 14 {
		return h, ErrShortHeader
	}
	copy(h.ID[:], b[0:4])
	h.ScoreLength = binary.LittleEndian.Uint16(b[4:6])
	h.ScoreStart = binary.LittleEndian.Uint16(b[6:8])
	h.PrimaryChannels = binary.LittleEndian.Uint16(b[8:10])
	h.SecondaryChannels = binary.LittleEndian.Uint16(b[10:12])
	h.InstrumentCount = binary.LittleEndian.Uint16(b[12:14])
	return h, nil
}

// converter holds the per-conversion mutable state, mirroring the static
// globals in mus2mid.c but scoped to a single Convert call.
type converter struct {
	out               []byte
	tracksize         uint32
	queuedtime        uint32
	channelVelocities [numChannels]byte
	channelMap        [numChannels]int
	mus               []byte
	pos               int
}

func (c *converter) readByte() (byte, bool) {
	if c.pos >= len(c.mus) {
		return 0, false
	}
	b := c.mus[c.pos]
	c.pos++
	return b, true
}

func (c *converter) writeByte(b byte) {
	c.out = append(c.out, b)
}

// writeTime emits a MIDI variable-length delta time and resets queuedtime.
func (c *converter) writeTime(t uint32) {
	buffer := t & 0x7F
	for t >>= 7; t != 0; t >>= 7 {
		buffer <<= 8
		buffer |= (t & 0x7F) | 0x80
	}
	for {
		c.writeByte(byte(buffer & 0xFF))
		c.tracksize++
		if buffer&0x80 != 0 {
			buffer >>= 8
		} else {
			c.queuedtime = 0
			return
		}
	}
}

func (c *converter) writeEndTrack() {
	c.writeTime(c.queuedtime)
	c.out = append(c.out, 0xFF, 0x2F, 0x00)
	c.tracksize += 3
}

func (c *converter) writePressKey(channel, key, velocity byte) {
	c.writeTime(c.queuedtime)
	c.writeByte(midiPressKey | channel)
	c.writeByte(key & 0x7F)
	c.writeByte(velocity & 0x7F)
	c.tracksize += 3
}

func (c *converter) writeReleaseKey(channel, key byte) {
	c.writeTime(c.queuedtime)
	c.writeByte(midiReleaseKey | channel)
	c.writeByte(key & 0x7F)
	c.writeByte(0)
	c.tracksize += 3
}

func (c *converter) writePitchWheel(channel byte, wheel int16) {
	c.writeTime(c.queuedtime)
	c.writeByte(midiPitchWheel | channel)
	c.writeByte(byte(wheel) & 0x7F)
	c.writeByte(byte(wheel>>7) & 0x7F)
	c.tracksize += 3
}

func (c *converter) writeChangePatch(channel, patch byte) {
	c.writeTime(c.queuedtime)
	c.writeByte(midiChangePatch | channel)
	c.writeByte(patch & 0x7F)
	c.tracksize += 2
}

func (c *converter) writeChangeControllerValued(channel, control, value byte) {
	c.writeTime(c.queuedtime)
	c.writeByte(midiChangeController | channel)
	c.writeByte(control & 0x7F)
	// Quirk in vanilla DOOM: MUS controller values should be 7-bit. Clamp
	// an out-of-range 8-bit value to 0x7F so MIDI players do not complain.
	working := value
	if working&0x80 != 0 {
		working = 0x7F
	}
	c.writeByte(working)
	c.tracksize += 3
}

func (c *converter) writeChangeControllerValueless(channel, control byte) {
	c.writeChangeControllerValued(channel, control, 0)
}

// allocateMIDIChannel returns the next free MIDI channel, skipping the
// percussion channel (9).
func (c *converter) allocateMIDIChannel() int {
	max := -1
	for i := 0; i < numChannels; i++ {
		if c.channelMap[i] > max {
			max = c.channelMap[i]
		}
	}
	result := max + 1
	if result == midiPercussionChan {
		result++
	}
	return result
}

// getMIDIChannel maps a MUS channel to a MIDI channel, allocating on first
// use and emitting an all-notes-off on that first use (the D_DDTBLU fix).
func (c *converter) getMIDIChannel(musChannel int) int {
	if musChannel == musPercussionChan {
		return midiPercussionChan
	}
	if c.channelMap[musChannel] == -1 {
		c.channelMap[musChannel] = c.allocateMIDIChannel()
		c.writeChangeControllerValueless(byte(c.channelMap[musChannel]), 0x7B)
	}
	return c.channelMap[musChannel]
}

// Convert converts a MUS lump to a type-0 MIDI file, byte-for-byte identical
// to chocolate-doom's mus2mid(). It does not check the MUS magic (matching
// chocolate-doom's default build), only the header length.
func Convert(mus []byte) ([]byte, error) {
	hdr, err := ParseHeader(mus)
	if err != nil {
		return nil, err
	}

	c := &converter{
		out: make([]byte, 0, len(mus)*2+len(midiHeader)),
		mus: mus,
		pos: int(hdr.ScoreStart),
	}
	for i := range c.channelVelocities {
		c.channelVelocities[i] = 127
	}
	for i := range c.channelMap {
		c.channelMap[i] = -1
	}
	if c.pos > len(mus) {
		return nil, ErrConvert
	}

	c.out = append(c.out, midiHeader...)
	c.tracksize = 0

	hitScoreEnd := false
	for !hitScoreEnd {
		for !hitScoreEnd {
			eventDescriptor, ok := c.readByte()
			if !ok {
				return nil, ErrConvert
			}
			channel := c.getMIDIChannel(int(eventDescriptor & 0x0F))
			event := int(eventDescriptor & 0x70)

			switch event {
			case musReleaseKey:
				key, ok := c.readByte()
				if !ok {
					return nil, ErrConvert
				}
				c.writeReleaseKey(byte(channel), key)

			case musPressKey:
				key, ok := c.readByte()
				if !ok {
					return nil, ErrConvert
				}
				if key&0x80 != 0 {
					vel, ok := c.readByte()
					if !ok {
						return nil, ErrConvert
					}
					c.channelVelocities[channel] = vel & 0x7F
				}
				c.writePressKey(byte(channel), key, c.channelVelocities[channel])

			case musPitchWheel:
				key, ok := c.readByte()
				if !ok {
					// Matches mus2mid.c: a truncated pitch event breaks
					// the inner loop rather than erroring.
					break
				}
				c.writePitchWheel(byte(channel), int16(uint16(key)*64))

			case musSystemEvent:
				controllerNumber, ok := c.readByte()
				if !ok {
					return nil, ErrConvert
				}
				if controllerNumber < 10 || controllerNumber > 14 {
					return nil, ErrConvert
				}
				c.writeChangeControllerValueless(byte(channel), controllerMap[controllerNumber])

			case musChangeController:
				controllerNumber, ok := c.readByte()
				if !ok {
					return nil, ErrConvert
				}
				controllerValue, ok := c.readByte()
				if !ok {
					return nil, ErrConvert
				}
				if controllerNumber == 0 {
					c.writeChangePatch(byte(channel), controllerValue)
				} else {
					if controllerNumber < 1 || controllerNumber > 9 {
						return nil, ErrConvert
					}
					c.writeChangeControllerValued(byte(channel),
						controllerMap[controllerNumber], controllerValue)
				}

			case musScoreEnd:
				hitScoreEnd = true

			default:
				return nil, ErrConvert
			}

			if eventDescriptor&0x80 != 0 {
				break
			}
		}

		if !hitScoreEnd {
			var timedelay uint32
			for {
				working, ok := c.readByte()
				if !ok {
					return nil, ErrConvert
				}
				timedelay = timedelay*128 + uint32(working&0x7F)
				if working&0x80 == 0 {
					break
				}
			}
			c.queuedtime += timedelay
		}
	}

	c.writeEndTrack()

	// Patch the track size big-endian at offset 18.
	binary.BigEndian.PutUint32(c.out[18:22], c.tracksize)
	return c.out, nil
}
