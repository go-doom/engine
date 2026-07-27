// SPDX-License-Identifier: BSD-3-Clause
// Copyright (c) the go-doom/engine authors.
// Classic DOOM netgame (doomcom/ticcmd lockstep) — pure-Go, CGO=0.

package netgame

import (
	"encoding/binary"
	"hash/fnv"
)

// Game is the pluggable deterministic world model advanced by the lockstep
// loop. Step advances the state by exactly one tic using every player's ticcmd
// for that tic (indexed by player). Hash returns a 64-bit digest of the current
// state (the state BEFORE the next tic runs); it must be a pure function of the
// tics applied so far, so that two nodes fed identical ticcmd streams always
// report identical hashes. The low 16 bits of Hash are used as the vanilla
// consistancy value.
type Game interface {
	Step(cmds []Ticcmd)
	Hash() uint64
}

// fnvOffset64 is the FNV-1a 64-bit offset basis, used as the default seed.
const fnvOffset64 = 1469598103934665603

// HashGame is a tiny deterministic reference "game": its entire state is a
// rolling 64-bit hash into which every player's ticcmd is mixed each tic. It is
// used by the tests as the differential oracle for lockstep determinism, and
// makes a fine stand-in wherever a real world simulation is not needed.
//
// The consistancy field of each ticcmd is deliberately excluded from the state
// evolution: consistancy is a CHECK on the state, not an INPUT to it.
type HashGame struct {
	state uint64
}

// NewHashGame returns a HashGame seeded with seed (0 maps to the FNV offset
// basis so the zero value is still a valid, non-degenerate seed).
func NewHashGame(seed uint64) *HashGame {
	if seed == 0 {
		seed = fnvOffset64
	}
	return &HashGame{state: seed}
}

// Hash returns the current rolling state.
func (g *HashGame) Hash() uint64 { return g.state }

// Step folds every player's ticcmd for this tic into the rolling state.
func (g *HashGame) Step(cmds []Ticcmd) {
	h := fnv.New64a()
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], g.state)
	_, _ = h.Write(buf[:])
	for i := range cmds {
		b, _ := cmds[i].MarshalBinary()
		// Zero the consistancy bytes (offsets 4,5): the check field must not
		// influence the simulated state.
		b[4], b[5] = 0, 0
		_, _ = h.Write(b)
	}
	g.state = h.Sum64()
}
