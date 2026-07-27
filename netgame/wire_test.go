// SPDX-License-Identifier: BSD-3-Clause
// Copyright (c) the go-doom/engine authors.
// Classic DOOM netgame (doomcom/ticcmd lockstep) — pure-Go, CGO=0.

package netgame

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

// TestTiccmdByteFaithful is the byte-faithful oracle: a known ticcmd must
// marshal to the exact vanilla 8-byte little-endian layout.
func TestTiccmdByteFaithful(t *testing.T) {
	tc := Ticcmd{
		ForwardMove: 10,
		SideMove:    -1,
		AngleTurn:   0x1234,
		ChatChar:    0x7F,
		Buttons:     0x80,
		Consistancy: 0xBEEF,
	}
	want := []byte{0x0A, 0xFF, 0x34, 0x12, 0xEF, 0xBE, 0x7F, 0x80}
	got, err := tc.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("ticcmd bytes = % x, want % x", got, want)
	}
}

func TestTiccmdRoundTrip(t *testing.T) {
	cases := []Ticcmd{
		{},
		{ForwardMove: 127, SideMove: -128, AngleTurn: -1, ChatChar: 255, Buttons: 1, Consistancy: 65535},
		{ForwardMove: -50, SideMove: 50, AngleTurn: 12345, ChatChar: 'a', Buttons: 0x0F, Consistancy: 0x0102},
	}
	for i, in := range cases {
		b, err := in.MarshalBinary()
		if err != nil {
			t.Fatalf("case %d marshal: %v", i, err)
		}
		if len(b) != TicSize {
			t.Fatalf("case %d len = %d, want %d", i, len(b), TicSize)
		}
		var out Ticcmd
		if err := out.UnmarshalBinary(b); err != nil {
			t.Fatalf("case %d unmarshal: %v", i, err)
		}
		if out != in {
			t.Fatalf("case %d round-trip: got %+v want %+v", i, out, in)
		}
	}
}

func TestTiccmdUnmarshalBadSize(t *testing.T) {
	var tc Ticcmd
	for _, n := range []int{0, 7, 9} {
		if err := tc.UnmarshalBinary(make([]byte, n)); !errors.Is(err, ErrTicSize) {
			t.Fatalf("len %d: err = %v, want ErrTicSize", n, err)
		}
	}
}

func TestDoomdataRoundTrip(t *testing.T) {
	in := &Doomdata{
		Flags:          NCMD_EXIT,
		RetransmitFrom: 3,
		StartTic:       17,
		Player:         2,
		Cmds: []Ticcmd{
			{ForwardMove: 1, SideMove: 2, AngleTurn: 3, Consistancy: 4},
			{ForwardMove: -5, SideMove: -6, AngleTurn: -7, Buttons: 8, Consistancy: 9},
		},
	}
	b, err := in.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if in.NumTics != 2 {
		t.Fatalf("Encode should set NumTics=2, got %d", in.NumTics)
	}
	out, err := DecodeDoomdata(b)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if out.Flags != NCMD_EXIT || out.RetransmitFrom != 3 || out.StartTic != 17 ||
		out.Player != 2 || out.NumTics != 2 {
		t.Fatalf("header mismatch: %+v", out)
	}
	for i := range in.Cmds {
		if out.Cmds[i] != in.Cmds[i] {
			t.Fatalf("cmd %d mismatch: got %+v want %+v", i, out.Cmds[i], in.Cmds[i])
		}
	}
}

func TestDoomdataEmpty(t *testing.T) {
	in := &Doomdata{Flags: NCMD_RETRANSMIT, RetransmitFrom: 42}
	b, err := in.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	out, err := DecodeDoomdata(b)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if out.Flags != NCMD_RETRANSMIT || out.RetransmitFrom != 42 || len(out.Cmds) != 0 {
		t.Fatalf("empty round-trip wrong: %+v", out)
	}
}

func TestDoomdataChecksumCorruption(t *testing.T) {
	in := &Doomdata{StartTic: 5, Player: 1, Cmds: []Ticcmd{{ForwardMove: 9}}}
	b, err := in.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	// Corrupt a payload byte (a cmd byte) without fixing the checksum.
	b[len(b)-1] ^= 0xFF
	if _, err := DecodeDoomdata(b); !errors.Is(err, ErrChecksum) {
		t.Fatalf("corrupted packet err = %v, want ErrChecksum", err)
	}
}

func TestDoomdataDecodeShortAndMisaligned(t *testing.T) {
	if _, err := DecodeDoomdata(make([]byte, 7)); !errors.Is(err, ErrShort) {
		t.Fatalf("short: %v, want ErrShort", err)
	}
	// 4-byte header word + 5-byte body: body-4 not a multiple of 8.
	if _, err := DecodeDoomdata(make([]byte, 9)); !errors.Is(err, ErrShort) {
		t.Fatalf("misaligned: %v, want ErrShort", err)
	}
}

func TestDoomdataDecodeTicCountMismatch(t *testing.T) {
	// Craft a buffer whose payload holds two cmds worth of bytes but whose
	// numtics field claims 1, with a VALID checksum, to reach ErrTicCount.
	body := make([]byte, 4+2*TicSize)
	body[0] = 0 // retransmitfrom
	body[1] = 0 // starttic
	body[2] = 0 // player
	body[3] = 1 // numtics = 1 (lie: payload actually holds 2 cmds)
	word := checksum(body) & NCMD_CHECKSUM
	buf := make([]byte, 4+len(body))
	binary.LittleEndian.PutUint32(buf[0:4], word)
	copy(buf[4:], body)
	if _, err := DecodeDoomdata(buf); !errors.Is(err, ErrTicCount) {
		t.Fatalf("err = %v, want ErrTicCount", err)
	}
}

func TestDoomdataEncodeTooManyTics(t *testing.T) {
	in := &Doomdata{Cmds: make([]Ticcmd, 256)}
	if _, err := in.Encode(); !errors.Is(err, ErrTicCount) {
		t.Fatalf("err = %v, want ErrTicCount", err)
	}
}
