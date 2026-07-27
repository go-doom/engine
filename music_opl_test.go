// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (c) the go-doom/engine authors.

package gore

import "testing"

// TestOPLMusicModuleWiring exercises the OPL music bridge without a loaded WAD.
// With no GENMIDI lump available, FInit installs no player, so every module
// entry point takes its nil-player path and ReadMusicPCM returns silence. This
// verifies the wiring is panic-free and anchors the bridge as reachable code.
func TestOPLMusicModuleWiring(t *testing.T) {
	// installMusicModule is set from music_opl.go's init().
	if installMusicModule == nil {
		t.Fatal("installMusicModule hook not registered")
	}
	installMusicModule()
	if music_module == nil {
		t.Fatal("music_module not installed")
	}

	// No GENMIDI lump is loaded in this bare test, so FInit installs no player.
	music_module.FInit()
	music_module.FSetMusicVolume(64)
	music_module.FPauseMusic()
	music_module.FResumeMusic()
	if h := music_module.FRegisterSong([]byte("not a song")); h != 0 {
		t.Errorf("FRegisterSong with no player: handle=%d, want 0", h)
	}
	if r := music_module.FPlaySong(0, 1); r != 0 {
		t.Errorf("FPlaySong with no player: r=%d, want 0", r)
	}
	music_module.FStopSong()
	music_module.FUnRegisterSong(0)
	music_module.FShutdown()

	// ReadMusicPCM returns silence (all zero) and len/2 frames when idle.
	buf := make([]int16, 64)
	for i := range buf {
		buf[i] = 123
	}
	frames := ReadMusicPCM(buf)
	if frames != len(buf)/2 {
		t.Errorf("ReadMusicPCM frames=%d, want %d", frames, len(buf)/2)
	}
	for i, v := range buf {
		if v != 0 {
			t.Fatalf("ReadMusicPCM not silent at %d: %d", i, v)
		}
	}

	if MusicSampleRate != 44100 {
		t.Errorf("MusicSampleRate=%d, want 44100", MusicSampleRate)
	}
}
