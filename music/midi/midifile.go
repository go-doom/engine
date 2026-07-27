// SPDX-License-Identifier: GPL-2.0-or-later
// Go port of chocolate-doom midifile.c / GENMIDI handling (Simon Howard et al.).
// Ported for the go-doom/engine authors.

// Package midi parses Standard MIDI Files (type 0 and 1) into tracks of
// timestamped events. It is a faithful Go port of chocolate-doom's
// midifile.c, preserving its handling of variable-length quantities,
// running status, meta events and SysEx events.
package midi

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// EventType identifies the kind of a MIDI event. The values match the status
// nibble/byte used on the wire (see midifile.h).
type EventType uint8

const (
	NoteOff        EventType = 0x80
	NoteOn         EventType = 0x90
	Aftertouch     EventType = 0xA0
	Controller     EventType = 0xB0
	ProgramChange  EventType = 0xC0
	ChanAftertouch EventType = 0xD0
	PitchBend      EventType = 0xE0

	SysEx      EventType = 0xF0
	SysExSplit EventType = 0xF7
	Meta       EventType = 0xFF
)

// Meta event sub-types (the value stored in Event.MetaType when Type == Meta).
const (
	MetaSequenceNumber   = 0x00
	MetaText             = 0x01
	MetaCopyright        = 0x02
	MetaTrackName        = 0x03
	MetaInstrName        = 0x04
	MetaLyrics           = 0x05
	MetaMarker           = 0x06
	MetaCuePoint         = 0x07
	MetaChannelPrefix    = 0x20
	MetaEndOfTrack       = 0x2F
	MetaSetTempo         = 0x51
	MetaSMPTEOffset      = 0x54
	MetaTimeSignature    = 0x58
	MetaKeySignature     = 0x59
	MetaSequencerSpecial = 0x7F
)

// Event is a single MIDI event with the delta time that precedes it.
//
// For channel events (NoteOff..PitchBend) Channel, Param1 and (where
// applicable) Param2 are populated. For SysEx / SysExSplit events Data holds
// the raw payload. For Meta events MetaType identifies the sub-type and Data
// holds the payload.
type Event struct {
	DeltaTime uint32
	Type      EventType

	// Channel event data.
	Channel uint8
	Param1  uint8
	Param2  uint8

	// Meta event data.
	MetaType uint8

	// SysEx / Meta payload.
	Data []byte
}

// File is a parsed Standard MIDI File.
type File struct {
	// FormatType is the SMF format (0 or 1).
	FormatType uint16
	// TimeDivision is the raw time-division field from the header (ticks per
	// quarter note, or SMPTE-encoded when the high bit is set). Use
	// TimeDivisionTicks for the resolved tick value.
	TimeDivision uint16
	// Tracks holds the events of each track in file order.
	Tracks [][]Event
}

// Errors returned by Parse.
var (
	ErrShortHeader    = errors.New("midi: unexpected end of data in header")
	ErrBadHeaderID    = errors.New("midi: expected 'MThd' chunk header")
	ErrBadHeaderSize  = errors.New("midi: invalid MIDI header chunk size")
	ErrUnsupportedFmt = errors.New("midi: only type 0/1 MIDI files supported")
	ErrNoTracks       = errors.New("midi: file contains no tracks")
	ErrBadTrackID     = errors.New("midi: expected 'MTrk' chunk header")
	ErrShortTrack     = errors.New("midi: unexpected end of data in track")
	ErrVarLenTooLong  = errors.New("midi: variable-length value too long (max four bytes)")
)

const (
	headerChunkID = "MThd"
	trackChunkID  = "MTrk"
)

// reader is a bounded cursor over a byte slice.
type reader struct {
	data []byte
	pos  int
}

func (r *reader) remaining() int { return len(r.data) - r.pos }

// readByte reads a single byte, returning ErrShortTrack on EOF.
func (r *reader) readByte() (byte, error) {
	if r.pos >= len(r.data) {
		return 0, ErrShortTrack
	}
	b := r.data[r.pos]
	r.pos++
	return b, nil
}

// readVariableLength reads a MIDI variable-length quantity (up to four bytes,
// seven significant bits each).
func (r *reader) readVariableLength() (uint32, error) {
	var result uint32
	for i := 0; i < 4; i++ {
		b, err := r.readByte()
		if err != nil {
			return 0, err
		}
		result <<= 7
		result |= uint32(b & 0x7f)
		if b&0x80 == 0 {
			return result, nil
		}
	}
	return 0, ErrVarLenTooLong
}

// readByteSequence reads num bytes into a fresh slice.
func (r *reader) readByteSequence(num uint32) ([]byte, error) {
	if uint64(num) > uint64(r.remaining()) {
		return nil, ErrShortTrack
	}
	out := make([]byte, num)
	copy(out, r.data[r.pos:r.pos+int(num)])
	r.pos += int(num)
	return out, nil
}

// Parse parses a Standard MIDI File from b.
func Parse(b []byte) (*File, error) {
	r := &reader{data: b}

	f := &File{}
	numTracks, err := readFileHeader(r, f)
	if err != nil {
		return nil, err
	}

	f.Tracks = make([][]Event, 0, numTracks)
	for i := 0; i < numTracks; i++ {
		track, err := readTrack(r)
		if err != nil {
			return nil, err
		}
		f.Tracks = append(f.Tracks, track)
	}

	return f, nil
}

// readFileHeader parses the MThd chunk and returns the declared track count.
func readFileHeader(r *reader, f *File) (int, error) {
	// midi_header_t: 4 id + 4 size + 2 format + 2 tracks + 2 division = 14.
	if r.remaining() < 14 {
		return 0, ErrShortHeader
	}
	id := string(r.data[r.pos : r.pos+4])
	if id != headerChunkID {
		return 0, fmt.Errorf("%w, got %q", ErrBadHeaderID, id)
	}
	chunkSize := binary.BigEndian.Uint32(r.data[r.pos+4 : r.pos+8])
	if chunkSize != 6 {
		return 0, fmt.Errorf("%w: chunk_size=%d", ErrBadHeaderSize, chunkSize)
	}
	f.FormatType = binary.BigEndian.Uint16(r.data[r.pos+8 : r.pos+10])
	numTracks := binary.BigEndian.Uint16(r.data[r.pos+10 : r.pos+12])
	f.TimeDivision = binary.BigEndian.Uint16(r.data[r.pos+12 : r.pos+14])
	r.pos += 14

	if f.FormatType != 0 && f.FormatType != 1 {
		return 0, ErrUnsupportedFmt
	}
	if numTracks < 1 {
		return 0, ErrNoTracks
	}
	return int(numTracks), nil
}

// readTrack parses one MTrk chunk, reading events until END_OF_TRACK.
func readTrack(r *reader) ([]Event, error) {
	if r.remaining() < 8 {
		return nil, ErrShortTrack
	}
	id := string(r.data[r.pos : r.pos+4])
	if id != trackChunkID {
		return nil, fmt.Errorf("%w, got %q", ErrBadTrackID, id)
	}
	// data_len is read (and swapped BE) by the C but the parser relies on the
	// END_OF_TRACK meta event to delimit the track, so we do the same.
	r.pos += 8

	var events []Event
	var lastEventType byte
	for {
		ev, err := readEvent(r, &lastEventType)
		if err != nil {
			return nil, err
		}
		events = append(events, ev)
		if ev.Type == Meta && ev.MetaType == MetaEndOfTrack {
			break
		}
	}
	return events, nil
}

// readEvent reads a single event, honouring running status via lastEventType.
func readEvent(r *reader, lastEventType *byte) (Event, error) {
	var ev Event

	delta, err := r.readVariableLength()
	if err != nil {
		return ev, err
	}
	ev.DeltaTime = delta

	eventType, err := r.readByte()
	if err != nil {
		return ev, err
	}

	// If the top bit is not set we are using running status: reuse the
	// previous event type and rewind so the byte is re-read as a parameter.
	if eventType&0x80 == 0 {
		eventType = *lastEventType
		r.pos--
	} else {
		*lastEventType = eventType
	}

	switch eventType & 0xf0 {
	case byte(NoteOff), byte(NoteOn), byte(Aftertouch), byte(Controller), byte(PitchBend):
		return readChannelEvent(r, &ev, eventType, true)
	case byte(ProgramChange), byte(ChanAftertouch):
		return readChannelEvent(r, &ev, eventType, false)
	}

	switch eventType {
	case byte(SysEx), byte(SysExSplit):
		return readSysExEvent(r, &ev, eventType)
	case byte(Meta):
		return readMetaEvent(r, &ev)
	}

	return ev, fmt.Errorf("midi: unknown MIDI event type: 0x%x", eventType)
}

// readChannelEvent reads a one- or two-parameter channel voice event.
func readChannelEvent(r *reader, ev *Event, eventType byte, twoParam bool) (Event, error) {
	ev.Type = EventType(eventType & 0xf0)
	ev.Channel = eventType & 0x0f

	b, err := r.readByte()
	if err != nil {
		return *ev, err
	}
	ev.Param1 = b

	if twoParam {
		b, err = r.readByte()
		if err != nil {
			return *ev, err
		}
		ev.Param2 = b
	}
	return *ev, nil
}

// readSysExEvent reads a SysEx (0xF0) or split SysEx (0xF7) event.
func readSysExEvent(r *reader, ev *Event, eventType byte) (Event, error) {
	ev.Type = EventType(eventType)
	length, err := r.readVariableLength()
	if err != nil {
		return *ev, err
	}
	data, err := r.readByteSequence(length)
	if err != nil {
		return *ev, err
	}
	ev.Data = data
	return *ev, nil
}

// readMetaEvent reads a meta event (0xFF).
func readMetaEvent(r *reader, ev *Event) (Event, error) {
	ev.Type = Meta
	b, err := r.readByte()
	if err != nil {
		return *ev, err
	}
	ev.MetaType = b

	length, err := r.readVariableLength()
	if err != nil {
		return *ev, err
	}
	data, err := r.readByteSequence(length)
	if err != nil {
		return *ev, err
	}
	ev.Data = data
	return *ev, nil
}

// NumTracks returns the number of tracks in the file.
func (f *File) NumTracks() int { return len(f.Tracks) }

// TimeDivisionTicks resolves the header time-division field to a positive tick
// value, handling SMPTE-encoded (negative) divisions like
// MIDI_GetFileTimeDivision.
func (f *File) TimeDivisionTicks() int {
	result := int16(f.TimeDivision)
	if result < 0 {
		return int(-(result / 256)) * int(result&0xFF)
	}
	return int(result)
}

// TrackIterator iterates the events of a single track, mirroring
// midi_track_iter_t / MIDI_GetNextEvent semantics.
type TrackIterator struct {
	events    []Event
	position  int
	loopPoint int
}

// IterateTrack returns an iterator over the events of the given track. It
// panics if track is out of range, matching the C assert.
func (f *File) IterateTrack(track int) *TrackIterator {
	if track < 0 || track >= len(f.Tracks) {
		panic(fmt.Sprintf("midi: track %d out of range (%d tracks)", track, len(f.Tracks)))
	}
	return &TrackIterator{events: f.Tracks[track]}
}

// DeltaTime returns the delta time of the next event, or 0 at end of track.
func (it *TrackIterator) DeltaTime() uint32 {
	if it.position < len(it.events) {
		return it.events[it.position].DeltaTime
	}
	return 0
}

// Next returns the next event and advances the iterator. The bool is false
// once the end of the track is reached.
func (it *TrackIterator) Next() (*Event, bool) {
	if it.position < len(it.events) {
		ev := &it.events[it.position]
		it.position++
		return ev, true
	}
	return nil, false
}

// Restart resets the iterator to the beginning of the track.
func (it *TrackIterator) Restart() {
	it.position = 0
	it.loopPoint = 0
}

// SetLoopPoint marks the current position as the loop point.
func (it *TrackIterator) SetLoopPoint() { it.loopPoint = it.position }

// RestartAtLoopPoint rewinds the iterator to the saved loop point.
func (it *TrackIterator) RestartAtLoopPoint() { it.position = it.loopPoint }
