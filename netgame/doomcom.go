// SPDX-License-Identifier: BSD-3-Clause
// Copyright (c) the go-doom/engine authors.
// Classic DOOM netgame (doomcom/ticcmd lockstep) — pure-Go, CGO=0.

package netgame

// Doomcom is the per-node game descriptor negotiated during the handshake,
// modelled on vanilla doomcom_t. Every node ends up with the same NumNodes,
// NumPlayers, TicDup, ExtraTics and DeathMatch; only ConsolePlayer differs
// (each node's own arbitrated player index).
type Doomcom struct {
	NumNodes      int // total participating nodes (== NumPlayers here)
	NumPlayers    int // total players
	ConsolePlayer int // this node's player index (0-based, lowest-id-first)
	TicDup        int // run 1-in-N tics, duplicating (vanilla ticdup)
	ExtraTics     int // redundant tics sent per packet (vanilla extratics)
	DeathMatch    int // 0 = co-op, 1/2 = deathmatch modes
}

// Config holds the shared parameters that all nodes agree on during Handshake.
type Config struct {
	TicDup     int
	ExtraTics  int
	DeathMatch int
}
