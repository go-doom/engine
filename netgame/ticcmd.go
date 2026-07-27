// SPDX-License-Identifier: BSD-3-Clause
// Copyright (c) the go-doom/engine authors.
// Classic DOOM netgame (doomcom/ticcmd lockstep) — pure-Go, CGO=0.

// Package netgame implements the classic DOOM netgame (multiplayer) protocol
// as a self-contained, testable pure-Go package: the doomcom / doomdata /
// ticcmd lockstep exchange modelled after id Software's d_net.c / i_net.c
// (the chocolate-doom-faithful behaviour).
//
// This is ORIGINAL Go work modelling a wire format — not a line-by-line copy
// of any GPL source — and is therefore licensed BSD-3-Clause.
//
// The three wire types are:
//
//   - Ticcmd   — one player's input for one tic (vanilla 8-byte layout).
//   - Doomdata — a network packet: a checksum/flags word, retransmitfrom,
//     starttic, player, numtics, then numtics Ticcmds.
//   - Doomcom  — the per-node game descriptor (numnodes, numplayers,
//     consoleplayer, ticdup, extratics, deathmatch, ...).
//
// A Transport seam abstracts the datagram layer so the deterministic lockstep
// loop (see net.go) runs fully in-process over an in-memory mesh for tests, or
// over real UDP sockets in production — all with CGO disabled.
package netgame

import (
	"encoding/binary"
	"errors"
)

// TicSize is the vanilla on-the-wire size of a single ticcmd, in bytes.
const TicSize = 8

// ErrTicSize is returned when decoding a ticcmd from a buffer whose length is
// not exactly TicSize bytes.
var ErrTicSize = errors.New("netgame: ticcmd buffer must be exactly 8 bytes")

// Ticcmd is one player's input for one tic. Its serialization is byte-identical
// to the engine's own saveg_write_ticcmd_t (vanilla 8-byte, little-endian):
//
//	forwardmove int8   (1)
//	sidemove    int8   (1)
//	angleturn   int16  (2, LE)
//	consistancy uint16 (2, LE)
//	chatchar    uint8  (1)
//	buttons     uint8  (1)
//
// Note the field ORDER on the wire differs from the Go struct field order:
// angleturn precedes consistancy on the wire, matching vanilla.
type Ticcmd struct {
	ForwardMove int8
	SideMove    int8
	AngleTurn   int16
	ChatChar    uint8
	Buttons     uint8
	Consistancy uint16
}

// MarshalBinary encodes the ticcmd into exactly 8 bytes using the vanilla
// little-endian layout. It never returns an error.
func (t Ticcmd) MarshalBinary() ([]byte, error) {
	b := make([]byte, TicSize)
	b[0] = byte(t.ForwardMove)
	b[1] = byte(t.SideMove)
	binary.LittleEndian.PutUint16(b[2:4], uint16(t.AngleTurn))
	binary.LittleEndian.PutUint16(b[4:6], t.Consistancy)
	b[6] = t.ChatChar
	b[7] = t.Buttons
	return b, nil
}

// UnmarshalBinary decodes an 8-byte vanilla ticcmd from b. It returns ErrTicSize
// if len(b) != TicSize.
func (t *Ticcmd) UnmarshalBinary(b []byte) error {
	if len(b) != TicSize {
		return ErrTicSize
	}
	t.ForwardMove = int8(b[0])
	t.SideMove = int8(b[1])
	t.AngleTurn = int16(binary.LittleEndian.Uint16(b[2:4]))
	t.Consistancy = binary.LittleEndian.Uint16(b[4:6])
	t.ChatChar = b[6]
	t.Buttons = b[7]
	return nil
}
