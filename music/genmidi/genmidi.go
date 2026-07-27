// SPDX-License-Identifier: GPL-2.0-or-later
// Go port of chocolate-doom midifile.c / GENMIDI handling (Simon Howard et al.).
// Ported for the go-doom/engine authors.

// Package genmidi parses the DMX GENMIDI lump, the OPL instrument bank used by
// DOOM's Adlib/OPL music playback. The layout is a faithful port of the
// genmidi_instr_t table read by chocolate-doom's i_oplmusic.c
// LoadInstrumentTable.
package genmidi

import (
	"encoding/binary"
	"errors"
)

// Header is the 8-byte GENMIDI magic ("#OPL_II#").
const Header = "#OPL_II#"

// Instrument counts, matching i_oplmusic.c.
const (
	NumInstruments = 128 // GENMIDI_NUM_INSTRS: melodic instruments
	NumPercussion  = 47  // GENMIDI_NUM_PERCUSSION
	// NumEntries is the total number of instrument entries in the lump, the
	// melodic instruments followed by the percussion instruments.
	NumEntries = NumInstruments + NumPercussion // 175
)

// Instrument flag bits (genmidi_instr_t.flags).
const (
	FlagFixed  = 0x0001 // fixed pitch
	Flag2Voice = 0x0004 // double voice (OPL3)
)

// Byte sizes of the packed on-disk structures.
const (
	operatorSize   = 6                                       // genmidi_op_t
	voiceSize      = operatorSize + 1 + operatorSize + 1 + 2 // genmidi_voice_t = 16
	instrumentSize = 2 + 1 + 1 + 2*voiceSize                 // genmidi_instr_t = 36
	nameSize       = 32
)

// Operator holds the six per-operator bytes of a genmidi_op_t.
type Operator struct {
	TremoloVibrato uint8 // tremolo
	AttackDecay    uint8 // attack
	SustainRelease uint8 // sustain
	Waveform       uint8 // waveform
	Scale          uint8 // scale
	Level          uint8 // level
}

// Voice is a genmidi_voice_t: a modulator/carrier operator pair plus the
// feedback byte, an unused byte and the signed base note offset.
type Voice struct {
	Modulator      Operator
	Feedback       uint8
	Carrier        Operator
	Unused         uint8
	BaseNoteOffset int16
}

// Instrument is a genmidi_instr_t: flags, tuning and two voices.
type Instrument struct {
	Flags      uint16
	FineTuning uint8
	FixedNote  uint8
	Voices     [2]Voice
}

// Bank is a parsed GENMIDI lump. Instruments 0..127 are the melodic
// instruments; 128..174 are the percussion instruments. Names, when present in
// the lump, are the trailing 32-byte name strings in the same order.
type Bank struct {
	Instruments [NumEntries]Instrument
	// Names holds the trailing 32-byte name strings if the lump includes
	// them; it is nil when the lump is truncated to just the instrument data
	// (both layouts are accepted, following DMX/i_oplmusic behaviour).
	Names [][nameSize]byte
}

// Errors returned by Load.
var (
	ErrBadHeader = errors.New("genmidi: missing '#OPL_II#' header")
	ErrShort     = errors.New("genmidi: lump too short for instrument table")
)

// Load parses a GENMIDI lump. It requires the 8-byte header and the 175
// instrument entries. The trailing name block is parsed when present but is
// optional, matching the two GENMIDI layouts seen in the wild.
func Load(b []byte) (*Bank, error) {
	if len(b) < len(Header) {
		return nil, ErrShort
	}
	if string(b[:len(Header)]) != Header {
		return nil, ErrBadHeader
	}

	off := len(Header)
	need := off + NumEntries*instrumentSize
	if len(b) < need {
		return nil, ErrShort
	}

	bank := &Bank{}
	for i := 0; i < NumEntries; i++ {
		bank.Instruments[i] = parseInstrument(b[off : off+instrumentSize])
		off += instrumentSize
	}

	// Trailing names are optional. Parse as many complete 32-byte names as the
	// lump provides (up to one per entry).
	if len(b) >= off+nameSize {
		names := make([][nameSize]byte, 0, NumEntries)
		for i := 0; i < NumEntries && len(b) >= off+nameSize; i++ {
			var n [nameSize]byte
			copy(n[:], b[off:off+nameSize])
			names = append(names, n)
			off += nameSize
		}
		bank.Names = names
	}

	return bank, nil
}

func parseInstrument(b []byte) Instrument {
	var instr Instrument
	instr.Flags = binary.LittleEndian.Uint16(b[0:2])
	instr.FineTuning = b[2]
	instr.FixedNote = b[3]
	instr.Voices[0] = parseVoice(b[4 : 4+voiceSize])
	instr.Voices[1] = parseVoice(b[4+voiceSize : 4+2*voiceSize])
	return instr
}

func parseVoice(b []byte) Voice {
	var v Voice
	v.Modulator = parseOperator(b[0:operatorSize])
	v.Feedback = b[operatorSize]
	v.Carrier = parseOperator(b[operatorSize+1 : 2*operatorSize+1])
	v.Unused = b[2*operatorSize+1]
	v.BaseNoteOffset = int16(binary.LittleEndian.Uint16(b[2*operatorSize+2 : 2*operatorSize+4]))
	return v
}

func parseOperator(b []byte) Operator {
	return Operator{
		TremoloVibrato: b[0],
		AttackDecay:    b[1],
		SustainRelease: b[2],
		Waveform:       b[3],
		Scale:          b[4],
		Level:          b[5],
	}
}
