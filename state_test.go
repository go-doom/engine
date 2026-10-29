// Copyright (c) 2026 the go-doom/engine authors.
// SPDX-License-Identifier: GPL-2.0-only

package gore

import "testing"

func TestGameStateString(t *testing.T) {
	for s, want := range map[GameState]string{
		StateLevel: "level", StateIntermission: "intermission", StateFinale: "finale",
		StateDemoScreen: "demoscreen", GameState(42): "unknown",
	} {
		if got := s.String(); got != want {
			t.Errorf("%d: got %q, want %q", s, got, want)
		}
	}
}

func TestObserveReadsThePlayer(t *testing.T) {
	saved := players[0].Fmo
	savedState, savedMenu := gamestate, menuactive
	t.Cleanup(func() { players[0].Fmo, gamestate, menuactive = saved, savedState, savedMenu })

	players[0].Fmo = nil
	if s := Observe(); s.Health != 0 || s.X != 0 || s.Y != 0 {
		t.Fatalf("no player: %+v", s)
	}

	mo := &mobj_t{}
	mo.Fx, mo.Fy, mo.Fhealth = 1056<<16, -3616<<16, 100
	players[0].Fmo = mo
	gamestate, menuactive = gs_DEMOSCREEN, 1
	s := Observe()
	if s.X != 1056 || s.Y != -3616 || s.Health != 100 || s.State != StateDemoScreen || !s.MenuActive {
		t.Fatalf("got %+v", s)
	}
}
