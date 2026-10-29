// Copyright (c) 2026 the go-doom/engine authors.
// SPDX-License-Identifier: GPL-2.0-only

// state.go is a read-only observation hook over the engine's globals, a
// sibling of seed.go that leaves the transpiled doom.go untouched. Hosts use
// it to report what the game is doing (in a log line, a test hook) without
// reaching into unexported state: the wasmbox client logs a line whenever
// the snapshot changes, and its browser tests assert on those lines that a
// key press really changed the game.

package gore

// GameState mirrors the engine's gamestate_t.
type GameState int32

// GameState values (gamestate_t in doomdef.h).
const (
	StateLevel        GameState = GameState(gs_LEVEL)
	StateIntermission GameState = GameState(gs_INTERMISSION)
	StateFinale       GameState = GameState(gs_FINALE)
	StateDemoScreen   GameState = GameState(gs_DEMOSCREEN)
)

// String names the state.
func (s GameState) String() string {
	switch s {
	case StateLevel:
		return "level"
	case StateIntermission:
		return "intermission"
	case StateFinale:
		return "finale"
	case StateDemoScreen:
		return "demoscreen"
	}
	return "unknown"
}

// Snapshot is the externally visible game state.
type Snapshot struct {
	State        GameState
	MenuActive   bool
	DemoPlayback bool
	Paused       bool
	Episode      int32
	Map          int32
	// Health is the console player's health; 0 when there is no player.
	Health int32
	// X and Y are the console player's position in map units (the engine's
	// 16.16 fixed point shifted down); 0 when there is no player.
	X, Y int32
}

// Observe returns the current game state. Call it from the goroutine that
// runs the engine (a frontend's DrawFrame), which is the only one that
// writes these globals.
func Observe() Snapshot {
	s := Snapshot{
		State:        GameState(gamestate),
		MenuActive:   menuactive != 0,
		DemoPlayback: demoplayback != 0,
		Paused:       paused != 0,
		Episode:      gameepisode,
		Map:          gamemap,
	}
	if consoleplayer >= 0 && int(consoleplayer) < len(players) {
		if mo := players[consoleplayer].Fmo; mo != nil {
			s.Health = mo.Fhealth
			s.X = int32(mo.Fx) >> 16
			s.Y = int32(mo.Fy) >> 16
		}
	}
	return s
}
