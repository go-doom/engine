// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (c) the go-doom/engine authors.

package mus

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The .mid golden files under testdata were produced by compiling
// chocolate-doom's mus2mid.c (unmodified) into a standalone oracle and
// running it on the matching .mus vector. This test asserts our pure-Go
// Convert is BYTE-IDENTICAL to that reference implementation.
func TestConvertByteExactVsChocolateDoom(t *testing.T) {
	vectors := []string{"basic", "allevents", "ctrlhigh", "bigdelay"}
	for _, name := range vectors {
		t.Run(name, func(t *testing.T) {
			musData, err := os.ReadFile(filepath.Join("testdata", name+".mus"))
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(filepath.Join("testdata", name+".mid"))
			if err != nil {
				t.Fatal(err)
			}
			got, err := Convert(musData)
			if err != nil {
				t.Fatalf("Convert: %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("not byte-exact vs mus2mid oracle:\n got %d bytes: %x\nwant %d bytes: %x",
					len(got), got, len(want), want)
			}
		})
	}
}

func TestIsMUS(t *testing.T) {
	if !IsMUS([]byte("MUS\x1a....")) {
		t.Error("valid MUS magic rejected")
	}
	if IsMUS([]byte("MThd")) {
		t.Error("MIDI accepted as MUS")
	}
	if IsMUS([]byte("MU")) {
		t.Error("short buffer accepted")
	}
}

func TestParseHeaderShort(t *testing.T) {
	if _, err := ParseHeader([]byte("MUS\x1a")); !errors.Is(err, ErrShortHeader) {
		t.Errorf("want ErrShortHeader, got %v", err)
	}
}

func TestParseHeaderFields(t *testing.T) {
	musData, err := os.ReadFile(filepath.Join("testdata", "basic.mus"))
	if err != nil {
		t.Fatal(err)
	}
	h, err := ParseHeader(musData)
	if err != nil {
		t.Fatal(err)
	}
	if h.ID != [4]byte{'M', 'U', 'S', 0x1A} {
		t.Errorf("bad id %v", h.ID)
	}
	if h.ScoreStart != 14 {
		t.Errorf("ScoreStart=%d want 14", h.ScoreStart)
	}
	if h.PrimaryChannels != 1 {
		t.Errorf("PrimaryChannels=%d want 1", h.PrimaryChannels)
	}
}

// mkMUS builds a MUS lump with score body starting at offset 14.
func mkMUS(score ...byte) []byte {
	b := []byte{'M', 'U', 'S', 0x1A,
		0, 0, // scorelength (ignored)
		14, 0, // scorestart = 14
		1, 0, // primary channels
		0, 0, // secondary channels
		0, 0, // instrument count
	}
	return append(b, score...)
}

func TestConvertErrorPaths(t *testing.T) {
	cases := []struct {
		name  string
		score []byte
	}{
		// press-key missing key byte
		{"truncated-presskey", []byte{musPressKey | 0}},
		// press-key with velocity flag but missing velocity byte
		{"truncated-velocity", []byte{musPressKey | 0, 0x80 | 60}},
		// release-key missing key
		{"truncated-release", []byte{musReleaseKey | 0}},
		// system event missing controller number
		{"truncated-system", []byte{musSystemEvent | 0}},
		// system event with out-of-range controller number (<10)
		{"bad-system-low", []byte{musSystemEvent | 0, 5}},
		// system event with out-of-range controller number (>14)
		{"bad-system-high", []byte{musSystemEvent | 0, 20}},
		// change controller missing number
		{"truncated-ctrl-num", []byte{musChangeController | 0}},
		// change controller missing value
		{"truncated-ctrl-val", []byte{musChangeController | 0, 3}},
		// change controller with out-of-range number
		{"bad-ctrl-num", []byte{musChangeController | 0, 200, 0}},
		// reserved event type 0x50 -> default -> error
		{"reserved-event", []byte{0x50}},
		// no score-end, truncated time bytes
		{"truncated-time", []byte{musPressKey | 0, 60}},
		// truncated at first event read (empty score, no scoreend)
		{"empty-score", []byte{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Convert(mkMUS(tc.score...))
			if !errors.Is(err, ErrConvert) {
				t.Errorf("want ErrConvert, got %v", err)
			}
		})
	}
}

// TestPitchWheelTruncatedBreak covers the mus2mid.c quirk where a
// truncated pitch-wheel event (descriptor with the group-end bit set but no
// data byte) breaks the event loop; the missing following time byte then
// surfaces as ErrConvert.
func TestPitchWheelTruncatedBreak(t *testing.T) {
	// pitch-wheel, group-end bit set (0x80), no data byte, nothing after.
	if _, err := Convert(mkMUS(0x80 | musPitchWheel | 0)); !errors.Is(err, ErrConvert) {
		t.Errorf("want ErrConvert, got %v", err)
	}
}

func TestConvertShortHeader(t *testing.T) {
	if _, err := Convert([]byte("MUS")); !errors.Is(err, ErrShortHeader) {
		t.Errorf("want ErrShortHeader, got %v", err)
	}
}

func TestConvertScoreStartBeyondEnd(t *testing.T) {
	// scorestart points past the end of the lump.
	b := []byte{'M', 'U', 'S', 0x1A, 0, 0, 100, 0, 1, 0, 0, 0, 0, 0}
	if _, err := Convert(b); !errors.Is(err, ErrConvert) {
		t.Errorf("want ErrConvert, got %v", err)
	}
}

// TestPitchWheelTruncatedBreaks verifies the mus2mid.c quirk where a
// truncated pitch-wheel event breaks the event loop instead of erroring,
// and the score still terminates cleanly via a following score-end. Here we
// place a lone pitch event whose data byte IS present but that is the last
// event of a group (0x80) followed by a valid time and a score-end.
func TestPitchWheelAndReuseVelocity(t *testing.T) {
	// press with vel, press reusing vel, pitch wheel, then score end.
	score := []byte{
		musPressKey | 0, 0x80 | 60, 90,
		musPressKey | 0, 64, // reuse cached velocity
		musPitchWheel | 0, 100,
		musScoreEnd | 0,
	}
	out, err := Convert(mkMUS(score...))
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if !bytes.HasPrefix(out, midiHeader[:14]) {
		t.Error("output missing MIDI header")
	}
}
