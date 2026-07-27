// SPDX-License-Identifier: GPL-2.0-or-later
// Go port of chocolate-doom midifile.c / GENMIDI handling (Simon Howard et al.).
// Ported for the go-doom/engine authors.

package midi

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// buildHeader returns a 14-byte MThd chunk.
func buildHeader(format, numTracks, division uint16) []byte {
	h := make([]byte, 14)
	copy(h, headerChunkID)
	binary.BigEndian.PutUint32(h[4:8], 6)
	binary.BigEndian.PutUint16(h[8:10], format)
	binary.BigEndian.PutUint16(h[10:12], numTracks)
	binary.BigEndian.PutUint16(h[12:14], division)
	return h
}

// buildTrack wraps event bytes in an MTrk chunk.
func buildTrack(events []byte) []byte {
	t := make([]byte, 8+len(events))
	copy(t, trackChunkID)
	binary.BigEndian.PutUint32(t[4:8], uint32(len(events)))
	copy(t[8:], events)
	return t
}

var endOfTrack = []byte{0x00, 0xFF, 0x2F, 0x00}

func TestParseRealLump(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "D_INTRO.lmp"))
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}

	f, err := Parse(b)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f.FormatType != 1 {
		t.Errorf("FormatType = %d, want 1", f.FormatType)
	}
	if f.TimeDivision != 0x60 {
		t.Errorf("TimeDivision = %#x, want 0x60", f.TimeDivision)
	}
	if f.TimeDivisionTicks() != 96 {
		t.Errorf("TimeDivisionTicks = %d, want 96", f.TimeDivisionTicks())
	}
	if f.NumTracks() != 9 {
		t.Errorf("NumTracks = %d, want 9", f.NumTracks())
	}

	// Iterate the first track and confirm we reach END_OF_TRACK.
	it := f.IterateTrack(0)
	sawEOT := false
	count := 0
	for {
		ev, ok := it.Next()
		if !ok {
			break
		}
		count++
		if ev.Type == Meta && ev.MetaType == MetaEndOfTrack {
			sawEOT = true
		}
	}
	if !sawEOT {
		t.Errorf("track 0 never reached END_OF_TRACK")
	}
	if count == 0 {
		t.Errorf("track 0 had no events")
	}
}

func TestParseSyntheticEvents(t *testing.T) {
	events := []byte{
		0x00, 0x90, 0x3C, 0x40, // note on, ch0, note 60, vel 64
		0x10, 0x3C, 0x00, // running status: note on (vel 0)
		0x00, 0xC0, 0x05, // program change, ch0, prog 5
		0x00, 0xD0, 0x7F, // channel aftertouch (one param)
		0x00, 0xE0, 0x00, 0x40, // pitch bend (two param)
		0x00, 0xF0, 0x02, 0x11, 0x22, // sysex, len 2
		0x00, 0xFF, 0x51, 0x03, 0x07, 0xA1, 0x20, // set tempo
	}
	events = append(events, endOfTrack...)

	b := append(buildHeader(0, 1, 96), buildTrack(events)...)
	f, err := Parse(b)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	tr := f.Tracks[0]
	// note on, running-status note on, prog change, chan aftertouch,
	// pitch bend, sysex, set tempo, end of track = 8 events.
	if len(tr) != 8 {
		t.Fatalf("len(track) = %d, want 8", len(tr))
	}

	if tr[0].Type != NoteOn || tr[0].Channel != 0 || tr[0].Param1 != 60 || tr[0].Param2 != 64 {
		t.Errorf("event0 = %+v", tr[0])
	}
	// Running status: delta 0x10, reuses NoteOn.
	if tr[1].Type != NoteOn || tr[1].DeltaTime != 0x10 || tr[1].Param1 != 60 || tr[1].Param2 != 0 {
		t.Errorf("event1 (running status) = %+v", tr[1])
	}
	if tr[2].Type != ProgramChange || tr[2].Param1 != 5 {
		t.Errorf("event2 = %+v", tr[2])
	}
	if tr[3].Type != ChanAftertouch || tr[3].Param1 != 0x7F {
		t.Errorf("event3 = %+v", tr[3])
	}
	if tr[4].Type != PitchBend || tr[4].Param1 != 0x00 || tr[4].Param2 != 0x40 {
		t.Errorf("event4 = %+v", tr[4])
	}
	if tr[5].Type != SysEx || len(tr[5].Data) != 2 || tr[5].Data[0] != 0x11 || tr[5].Data[1] != 0x22 {
		t.Errorf("event5 (sysex) = %+v", tr[5])
	}
	if tr[6].Type != Meta || tr[6].MetaType != MetaSetTempo || len(tr[6].Data) != 3 {
		t.Errorf("event6 (set tempo) = %+v", tr[6])
	}
	if tr[7].Type != Meta || tr[7].MetaType != MetaEndOfTrack {
		t.Errorf("event7 = %+v", tr[7])
	}
}

func TestParseSysExSplit(t *testing.T) {
	events := []byte{0x00, 0xF7, 0x01, 0x55}
	events = append(events, endOfTrack...)
	b := append(buildHeader(0, 1, 96), buildTrack(events)...)
	f, err := Parse(b)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f.Tracks[0][0].Type != SysExSplit || f.Tracks[0][0].Data[0] != 0x55 {
		t.Errorf("split sysex parsed wrong: %+v", f.Tracks[0][0])
	}
}

func TestParseAftertouchAndController(t *testing.T) {
	events := []byte{
		0x00, 0xA0, 0x3C, 0x10, // aftertouch
		0x00, 0xB0, 0x07, 0x64, // controller (volume)
	}
	events = append(events, endOfTrack...)
	b := append(buildHeader(0, 1, 96), buildTrack(events)...)
	f, err := Parse(b)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f.Tracks[0][0].Type != Aftertouch || f.Tracks[0][1].Type != Controller {
		t.Errorf("aftertouch/controller parsed wrong")
	}
}

func TestVariableLengthMultiByte(t *testing.T) {
	// Three-byte VLQ 0xBF 0xFF 0x7F = 0x3F<<14 | 0x7F<<7 | 0x7F = 0x0FFFFF.
	events := []byte{0xBF, 0xFF, 0x7F, 0x90, 0x3C, 0x40}
	events = append(events, endOfTrack...)
	b := append(buildHeader(0, 1, 96), buildTrack(events)...)
	f, err := Parse(b)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f.Tracks[0][0].DeltaTime != 0x0FFFFF {
		t.Errorf("DeltaTime = %#x, want 0x0FFFFF", f.Tracks[0][0].DeltaTime)
	}
}

func TestSMPTETimeDivision(t *testing.T) {
	// SMPTE-encoded division 0xE728 (int16 = -6360). Faithful to
	// MIDI_GetFileTimeDivision: -(result/256) * (result & 0xFF) with C-style
	// truncating division = -(-24) * 40 = 960.
	f := &File{TimeDivision: 0xE728}
	if got := f.TimeDivisionTicks(); got != 960 {
		t.Errorf("SMPTE TimeDivisionTicks = %d, want 960", got)
	}
}

func TestIterator(t *testing.T) {
	events := []byte{
		0x05, 0x90, 0x3C, 0x40,
	}
	events = append(events, endOfTrack...)
	b := append(buildHeader(0, 1, 96), buildTrack(events)...)
	f, err := Parse(b)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	it := f.IterateTrack(0)
	if it.DeltaTime() != 0x05 {
		t.Errorf("DeltaTime = %d, want 5", it.DeltaTime())
	}
	ev, ok := it.Next()
	if !ok || ev.Type != NoteOn {
		t.Fatalf("first Next: ok=%v ev=%+v", ok, ev)
	}

	it.SetLoopPoint()
	ev, ok = it.Next() // end of track
	if !ok || ev.MetaType != MetaEndOfTrack {
		t.Fatalf("second Next: ok=%v ev=%+v", ok, ev)
	}
	// Exhausted.
	if _, ok = it.Next(); ok {
		t.Errorf("expected iterator exhausted")
	}
	if it.DeltaTime() != 0 {
		t.Errorf("DeltaTime at end = %d, want 0", it.DeltaTime())
	}

	it.RestartAtLoopPoint()
	ev, ok = it.Next()
	if !ok || ev.MetaType != MetaEndOfTrack {
		t.Errorf("after RestartAtLoopPoint: ok=%v ev=%+v", ok, ev)
	}

	it.Restart()
	if it.DeltaTime() != 0x05 {
		t.Errorf("after Restart DeltaTime = %d, want 5", it.DeltaTime())
	}
}

func TestIterateTrackPanic(t *testing.T) {
	f := &File{Tracks: make([][]Event, 1)}
	defer func() {
		if recover() == nil {
			t.Errorf("expected panic for out-of-range track")
		}
	}()
	f.IterateTrack(5)
}

func TestParseHeaderErrors(t *testing.T) {
	valid := buildHeader(0, 1, 96)

	tests := []struct {
		name string
		b    []byte
		want error
	}{
		{"short", valid[:10], ErrShortHeader},
		{"bad id", func() []byte { b := clone(valid); b[0] = 'X'; return b }(), ErrBadHeaderID},
		{"bad size", func() []byte { b := clone(valid); binary.BigEndian.PutUint32(b[4:8], 7); return b }(), ErrBadHeaderSize},
		{"bad format", func() []byte { b := clone(valid); binary.BigEndian.PutUint16(b[8:10], 2); return b }(), ErrUnsupportedFmt},
		{"no tracks", func() []byte { b := clone(valid); binary.BigEndian.PutUint16(b[10:12], 0); return b }(), ErrNoTracks},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.b)
			if !errorIs(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestParseTrackErrors(t *testing.T) {
	hdr := buildHeader(0, 1, 96)

	t.Run("short track header", func(t *testing.T) {
		b := append(clone(hdr), []byte{'M', 'T'}...)
		if _, err := Parse(b); err != ErrShortTrack {
			t.Fatalf("err = %v, want ErrShortTrack", err)
		}
	})

	t.Run("bad track id", func(t *testing.T) {
		b := append(clone(hdr), buildTrack(endOfTrack)...)
		b[len(hdr)] = 'X'
		if _, err := Parse(b); !errorIs(err, ErrBadTrackID) {
			t.Fatalf("err = %v, want ErrBadTrackID", err)
		}
	})

	t.Run("truncated before end of track", func(t *testing.T) {
		// Track that starts an event but runs out of data.
		b := append(clone(hdr), buildTrack([]byte{0x00, 0x90, 0x3C})...)
		if _, err := Parse(b); err != ErrShortTrack {
			t.Fatalf("err = %v, want ErrShortTrack", err)
		}
	})

	t.Run("unknown event type", func(t *testing.T) {
		b := append(clone(hdr), buildTrack([]byte{0x00, 0xF5, 0x00})...)
		if _, err := Parse(b); err == nil {
			t.Fatalf("expected error for unknown event type")
		}
	})
}

func TestReadEventErrorPaths(t *testing.T) {
	hdr := buildHeader(0, 1, 96)

	cases := map[string][]byte{
		"delta eof":          {},                             // no bytes at all
		"event type eof":     {0x00},                         // delta only
		"channel param1 eof": {0x00, 0x90},                   // status but no params
		"channel param2 eof": {0x00, 0x90, 0x3C},             // missing param2
		"sysex length eof":   {0x00, 0xF0},                   // no varlen
		"sysex data eof":     {0x00, 0xF0, 0x04, 0x11},       // len 4, only 1 byte
		"meta type eof":      {0x00, 0xFF},                   // no meta type
		"meta length eof":    {0x00, 0xFF, 0x51},             // no varlen
		"meta data eof":      {0x00, 0xFF, 0x51, 0x04},       // len 4, no data
		"varlen too long":    {0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, // 5 continuation bytes
	}
	for name, ev := range cases {
		t.Run(name, func(t *testing.T) {
			b := append(clone(hdr), buildTrack(ev)...)
			if _, err := Parse(b); err == nil {
				t.Fatalf("expected error for %q", name)
			}
		})
	}
}

func clone(b []byte) []byte {
	c := make([]byte, len(b))
	copy(c, b)
	return c
}

// errorIs reports whether err wraps target (small helper to avoid importing
// errors just for this in the test).
func errorIs(err, target error) bool {
	for err != nil {
		if err == target {
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
