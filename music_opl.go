// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (c) the go-doom/engine authors.
//
// music_opl.go installs the pure-Go OPL2/OPL3 music module: it synthesises the
// DMX MUS / MIDI music lumps through a Yamaha YMF262 emulator driving the DMX
// GENMIDI instrument bank, exactly like chocolate-doom's OPL music path, with
// CGO disabled. PCM is pulled by the host frontend through ReadMusicPCM.

package gore

import (
	"sync"

	"github.com/go-doom/engine/music/oplplayer"
)

// MusicSampleRate is the stereo sample rate (Hz) at which the OPL music module
// renders. The host frontend should pull ReadMusicPCM at this rate.
const MusicSampleRate = 44100

// oplMusicOPL3 selects OPL3 (18-voice) synthesis when true, matching
// chocolate-doom's DMXOPTION "-opl3" extension; OPL2 (9-voice) otherwise.
var oplMusicOPL3 = true

var (
	musicMu   sync.Mutex
	oplPlayer *oplplayer.Player
)

func init() {
	installMusicModule = installOPLMusicModule
}

// installOPLMusicModule is invoked by the engine's initMusicModule() (when
// music is enabled) to make the OPL synth the active music_module.
func installOPLMusicModule() {
	music_module = &oplMusicModule
}

var oplMusicModule = music_module_t{
	Fnum_sound_devices: 2,
	FInit:              oplMusicInit,
	FShutdown:          oplMusicShutdown,
	FSetMusicVolume:    oplMusicSetVolume,
	FPauseMusic:        oplMusicPause,
	FResumeMusic:       oplMusicResume,
	FRegisterSong:      oplMusicRegisterSong,
	FUnRegisterSong:    oplMusicUnRegisterSong,
	FPlaySong:          oplMusicPlaySong,
	FStopSong:          oplMusicStopSong,
	FPoll:              nil,
}

func oplMusicInit() {
	musicMu.Lock()
	defer musicMu.Unlock()
	lump := w_CheckNumForName("GENMIDI")
	if lump < 0 {
		return // no GENMIDI bank -> music stays silent, engine runs on
	}
	p, err := oplplayer.New(w_CacheLumpNumBytes(lump), MusicSampleRate, oplMusicOPL3)
	if err != nil {
		return
	}
	oplPlayer = p
}

func oplMusicShutdown() {
	musicMu.Lock()
	defer musicMu.Unlock()
	if oplPlayer != nil {
		oplPlayer.Stop()
		oplPlayer = nil
	}
}

func oplMusicSetVolume(volume int32) {
	musicMu.Lock()
	defer musicMu.Unlock()
	if oplPlayer != nil {
		oplPlayer.SetVolume(int(volume))
	}
}

func oplMusicPause() {
	musicMu.Lock()
	defer musicMu.Unlock()
	if oplPlayer != nil {
		oplPlayer.Pause()
	}
}

func oplMusicResume() {
	musicMu.Lock()
	defer musicMu.Unlock()
	if oplPlayer != nil {
		oplPlayer.Resume()
	}
}

// oplMusicRegisterSong parses a MUS or MIDI lump and prepares it for playback.
// It returns a non-zero handle on success (the OPL player holds one song at a
// time, matching DMX), or 0 on failure.
func oplMusicRegisterSong(data []byte) uintptr {
	musicMu.Lock()
	defer musicMu.Unlock()
	if oplPlayer == nil {
		return 0
	}
	if err := oplPlayer.RegisterSong(data); err != nil {
		return 0
	}
	return 1
}

func oplMusicUnRegisterSong(handle uintptr) {
	musicMu.Lock()
	defer musicMu.Unlock()
	if oplPlayer != nil {
		oplPlayer.Stop()
	}
}

func oplMusicPlaySong(handle uintptr, looping boolean) boolean {
	musicMu.Lock()
	defer musicMu.Unlock()
	if oplPlayer == nil {
		return 0
	}
	oplPlayer.Play(looping != 0)
	return 1
}

func oplMusicStopSong() {
	musicMu.Lock()
	defer musicMu.Unlock()
	if oplPlayer != nil {
		oplPlayer.Stop()
	}
}

// ReadMusicPCM renders interleaved stereo int16 music PCM into buf (len must be
// even), advancing the OPL player's clock, and returns the number of stereo
// FRAMES written. It is safe to call from a host audio-callback goroutine. If no
// song is playing it zero-fills buf and returns len(buf)/2. This is the seam by
// which a frontend mixes music alongside SFX, mirroring chocolate-doom's OPL
// audio callback.
func ReadMusicPCM(buf []int16) int {
	musicMu.Lock()
	defer musicMu.Unlock()
	if oplPlayer == nil {
		for i := range buf {
			buf[i] = 0
		}
		return len(buf) / 2
	}
	return oplPlayer.Read(buf)
}
