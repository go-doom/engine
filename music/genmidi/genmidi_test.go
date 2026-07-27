// SPDX-License-Identifier: GPL-2.0-or-later
// Go port of chocolate-doom midifile.c / GENMIDI handling (Simon Howard et al.).
// Ported for the go-doom/engine authors.

package genmidi

import (
	"os"
	"path/filepath"
	"testing"
)

func loadTestBank(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "GENMIDI.lmp"))
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}
	return b
}

func TestLoadRealLump(t *testing.T) {
	b := loadTestBank(t)
	if len(b) != 11908 {
		t.Fatalf("unexpected lump size %d", len(b))
	}

	bank, err := Load(b)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// 175 instrument entries (128 melodic + 47 percussion).
	if NumEntries != 175 {
		t.Fatalf("NumEntries constant = %d, want 175", NumEntries)
	}
	if got := len(bank.Instruments); got != NumEntries {
		t.Fatalf("len(Instruments) = %d, want %d", got, NumEntries)
	}

	// Spot-check the first instrument (Acoustic Grand Piano).
	in := bank.Instruments[0]
	if in.Flags != 0x0004 {
		t.Errorf("Flags = %#x, want 0x0004", in.Flags)
	}
	if in.FineTuning != 0x82 {
		t.Errorf("FineTuning = %#x, want 0x82", in.FineTuning)
	}
	if in.FixedNote != 0x00 {
		t.Errorf("FixedNote = %#x, want 0x00", in.FixedNote)
	}

	m := in.Voices[0].Modulator
	wantMod := Operator{0x33, 0xe1, 0x23, 0x02, 0x80, 0x25}
	if m != wantMod {
		t.Errorf("Voice0 modulator = %+v, want %+v", m, wantMod)
	}
	if in.Voices[0].Feedback != 0x0e {
		t.Errorf("Voice0 feedback = %#x, want 0x0e", in.Voices[0].Feedback)
	}
	c := in.Voices[0].Carrier
	wantCar := Operator{0x31, 0xf1, 0xf4, 0x04, 0x00, 0x09}
	if c != wantCar {
		t.Errorf("Voice0 carrier = %+v, want %+v", c, wantCar)
	}
	if in.Voices[0].BaseNoteOffset != -12 {
		t.Errorf("Voice0 base note offset = %d, want -12", in.Voices[0].BaseNoteOffset)
	}
	if in.Voices[1].BaseNoteOffset != -12 {
		t.Errorf("Voice1 base note offset = %d, want -12", in.Voices[1].BaseNoteOffset)
	}

	// The 2VOICE flag is set on this instrument.
	if in.Flags&Flag2Voice == 0 {
		t.Errorf("expected Flag2Voice set on instrument 0")
	}

	// Names present: first name is "Acoustic Grand Piano".
	if len(bank.Names) != NumEntries {
		t.Fatalf("len(Names) = %d, want %d", len(bank.Names), NumEntries)
	}
	name := nameString(bank.Names[0])
	if name != "Acoustic Grand Piano" {
		t.Errorf("Names[0] = %q, want %q", name, "Acoustic Grand Piano")
	}
}

func nameString(n [nameSize]byte) string {
	end := 0
	for end < len(n) && n[end] != 0 {
		end++
	}
	return string(n[:end])
}

func TestLoadWithoutNames(t *testing.T) {
	full := loadTestBank(t)
	// Truncate to header + instrument table only (no trailing names).
	trimmed := make([]byte, len(Header)+NumEntries*instrumentSize)
	copy(trimmed, full)

	bank, err := Load(trimmed)
	if err != nil {
		t.Fatalf("Load (no names): %v", err)
	}
	if bank.Names != nil {
		t.Errorf("expected nil Names for name-less lump, got %d", len(bank.Names))
	}
	if bank.Instruments[0].Flags != 0x0004 {
		t.Errorf("instrument 0 flags wrong after trim")
	}
}

func TestLoadPartialNames(t *testing.T) {
	full := loadTestBank(t)
	// Keep the instrument table plus exactly two complete names.
	base := len(Header) + NumEntries*instrumentSize
	trimmed := make([]byte, base+2*nameSize)
	copy(trimmed, full)

	bank, err := Load(trimmed)
	if err != nil {
		t.Fatalf("Load (partial names): %v", err)
	}
	if len(bank.Names) != 2 {
		t.Fatalf("len(Names) = %d, want 2", len(bank.Names))
	}
}

func TestLoadErrors(t *testing.T) {
	full := loadTestBank(t)

	t.Run("too short for header", func(t *testing.T) {
		if _, err := Load([]byte("#OPL")); err != ErrShort {
			t.Fatalf("err = %v, want ErrShort", err)
		}
	})

	t.Run("bad header", func(t *testing.T) {
		bad := make([]byte, len(full))
		copy(bad, full)
		bad[0] = 'X'
		if _, err := Load(bad); err != ErrBadHeader {
			t.Fatalf("err = %v, want ErrBadHeader", err)
		}
	})

	t.Run("short instrument table", func(t *testing.T) {
		short := make([]byte, len(Header)+10)
		copy(short, full)
		if _, err := Load(short); err != ErrShort {
			t.Fatalf("err = %v, want ErrShort", err)
		}
	})
}
