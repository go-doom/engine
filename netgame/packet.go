// SPDX-License-Identifier: BSD-3-Clause
// Copyright (c) the go-doom/engine authors.
// Classic DOOM netgame (doomcom/ticcmd lockstep) — pure-Go, CGO=0.

package netgame

import (
	"encoding/binary"
	"errors"
)

// Command flags packed into the high bits of the doomdata checksum word,
// matching vanilla d_net.c. The low 28 bits (NCMD_CHECKSUM) hold the running
// checksum; the top 4 bits carry the command flags.
const (
	NCMD_EXIT       uint32 = 0x80000000 // player is leaving the game
	NCMD_RETRANSMIT uint32 = 0x40000000 // this is a retransmit request
	NCMD_SETUP      uint32 = 0x20000000 // node discovery / handshake
	NCMD_KILL       uint32 = 0x10000000 // kill the game
	NCMD_CHECKSUM   uint32 = 0x0FFFFFFF // mask for the running checksum
)

// ncmdFlags is the mask of all flag bits (the complement of NCMD_CHECKSUM).
const ncmdFlags = ^NCMD_CHECKSUM

// checksumSeed is vanilla DOOM's NetbufferChecksum seed.
const checksumSeed uint32 = 0x1234567

// Packet errors.
var (
	// ErrShort is returned when a buffer is too small or misaligned to hold a
	// valid doomdata packet.
	ErrShort = errors.New("netgame: packet too short or misaligned")
	// ErrChecksum is returned when a decoded packet's stored checksum does not
	// match the recomputed checksum.
	ErrChecksum = errors.New("netgame: packet checksum mismatch")
	// ErrTicCount is returned when numtics does not agree with the payload
	// length, or when more than 255 ticcmds are supplied to Encode.
	ErrTicCount = errors.New("netgame: ticcmd count out of range")
)

// Doomdata is the classic network packet (vanilla doomdata_t). On the wire it
// is a little-endian uint32 checksum/flags word followed by the four header
// bytes (retransmitfrom, starttic, player, numtics) and numtics ticcmds.
//
// The Flags field holds the command bits (NCMD_EXIT/RETRANSMIT/SETUP/KILL); the
// running checksum in the low 28 bits is computed by Encode and verified by
// DecodeDoomdata, so callers never manage it directly.
type Doomdata struct {
	Flags          uint32 // NCMD_* command bits (checksum bits are ignored here)
	RetransmitFrom uint8  // only meaningful with NCMD_RETRANSMIT
	StartTic       uint8  // tic number of Cmds[0] (low 8 bits, vanilla)
	Player         uint8  // which player these commands belong to
	NumTics        uint8  // set from len(Cmds) by Encode
	Cmds           []Ticcmd
}

// checksum computes the vanilla running sum over body, treated as little-endian
// uint32 words. body must be a multiple of 4 bytes long (the caller guarantees
// this: 4 header bytes + 8 bytes per ticcmd). The result is not yet masked.
func checksum(body []byte) uint32 {
	c := checksumSeed
	for i := 0; i+4 <= len(body); i += 4 {
		w := binary.LittleEndian.Uint32(body[i : i+4])
		c += w * uint32(i/4+1)
	}
	return c
}

// Encode serializes the packet, computing and embedding the checksum exactly as
// vanilla did (running sum over every word from retransmitfrom onward, masked
// with NCMD_CHECKSUM and OR'd with the command flags). NumTics is set from
// len(Cmds). It returns ErrTicCount if there are more than 255 ticcmds.
func (d *Doomdata) Encode() ([]byte, error) {
	if len(d.Cmds) > 255 {
		return nil, ErrTicCount
	}
	d.NumTics = uint8(len(d.Cmds))

	body := make([]byte, 0, 4+TicSize*len(d.Cmds))
	body = append(body, d.RetransmitFrom, d.StartTic, d.Player, d.NumTics)
	for i := range d.Cmds {
		cb, _ := d.Cmds[i].MarshalBinary()
		body = append(body, cb...)
	}

	sum := checksum(body) & NCMD_CHECKSUM
	word := (d.Flags & ncmdFlags) | sum

	out := make([]byte, 4+len(body))
	binary.LittleEndian.PutUint32(out[0:4], word)
	copy(out[4:], body)
	return out, nil
}

// DecodeDoomdata parses a packet produced by Encode. It verifies the checksum
// (returning ErrChecksum on mismatch), rejects short/misaligned buffers
// (ErrShort), and validates numtics against the payload length (ErrTicCount).
func DecodeDoomdata(b []byte) (*Doomdata, error) {
	if len(b) < 8 { // 4-byte checksum word + at least the 4 header bytes
		return nil, ErrShort
	}
	body := b[4:]
	if len(body) < 4 || (len(body)-4)%TicSize != 0 {
		return nil, ErrShort
	}

	word := binary.LittleEndian.Uint32(b[0:4])
	stored := word & NCMD_CHECKSUM
	if checksum(body)&NCMD_CHECKSUM != stored {
		return nil, ErrChecksum
	}

	d := &Doomdata{
		Flags:          word & ncmdFlags,
		RetransmitFrom: body[0],
		StartTic:       body[1],
		Player:         body[2],
		NumTics:        body[3],
	}
	n := int(d.NumTics)
	if 4+TicSize*n != len(body) {
		return nil, ErrTicCount
	}
	d.Cmds = make([]Ticcmd, n)
	for i := 0; i < n; i++ {
		off := 4 + TicSize*i
		if err := d.Cmds[i].UnmarshalBinary(body[off : off+TicSize]); err != nil {
			return nil, err
		}
	}
	return d, nil
}
