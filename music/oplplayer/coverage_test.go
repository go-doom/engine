// SPDX-License-Identifier: GPL-2.0-or-later
// Go port of chocolate-doom i_oplmusic.c (Simon Howard et al.).
// Ported for the go-doom/engine authors.

package oplplayer

import (
	"os"
	"testing"

	"github.com/go-doom/engine/music/midi"
)

func newTestPlayer(t *testing.T, opl3 bool) *Player {
	t.Helper()
	gm, err := os.ReadFile(genmidiPath)
	if err != nil {
		t.Fatalf("read genmidi: %v", err)
	}
	p, err := New(gm, 44100, opl3)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.RegisterSong(mustRead(t, dintroPath)); err != nil {
		t.Fatalf("RegisterSong: %v", err)
	}
	p.Play(true)
	return p
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

// TestNewErrors covers the GENMIDI load error path.
func TestNewErrors(t *testing.T) {
	if _, err := New([]byte("not a genmidi lump"), 44100, false); err == nil {
		t.Fatal("expected error for bad GENMIDI lump")
	}
}

// TestRegisterSongMUS covers the MUS-conversion branch and the parse-error path.
func TestRegisterSongMUS(t *testing.T) {
	gm := mustRead(t, genmidiPath)
	p, err := New(gm, 44100, false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	musData := mustRead(t, "../mus/testdata/basic.mus")
	if err := p.RegisterSong(musData); err != nil {
		t.Fatalf("RegisterSong(MUS): %v", err)
	}

	if err := p.RegisterSong([]byte("MThd garbage that is not valid")); err == nil {
		t.Fatal("expected parse error for malformed MIDI")
	}
}

// TestEventHandlersOPL3 exercises every channel/meta event handler in OPL3
// mode, including pan handling (setChannelPan/setVoicePan), pitch bend, volume,
// program change, percussion (in range and out of range), the volume-zero
// note-off shortcut, all-notes-off and a tempo change.
func TestEventHandlersOPL3(t *testing.T) {
	p := newTestPlayer(t, true)

	// Program change to an instrument whose feedback is non-modulated, so the
	// carrier/modulator volume branch in setVoiceVolume is exercised.
	prog := findFeedbackInstrument(p)
	p.processEvent(&midi.Event{Type: midi.ProgramChange, Channel: 0, Param1: uint8(prog)})

	p.processEvent(&midi.Event{Type: midi.NoteOn, Channel: 0, Param1: 60, Param2: 100})
	p.processEvent(&midi.Event{Type: midi.Controller, Channel: 0, Param1: 0x0A, Param2: 120}) // pan right
	p.processEvent(&midi.Event{Type: midi.Controller, Channel: 0, Param1: 0x0A, Param2: 10})  // pan left
	p.processEvent(&midi.Event{Type: midi.Controller, Channel: 0, Param1: 0x0A, Param2: 64})  // pan centre
	p.processEvent(&midi.Event{Type: midi.Controller, Channel: 0, Param1: 0x07, Param2: 40})  // volume
	p.processEvent(&midi.Event{Type: midi.PitchBend, Channel: 0, Param2: 96})
	p.processEvent(&midi.Event{Type: midi.NoteOff, Channel: 0, Param1: 60})

	// Channel 15 <-> 9 swap on a controller event.
	p.processEvent(&midi.Event{Type: midi.Controller, Channel: 15, Param1: 0x07, Param2: 90})

	// Percussion: in range, out of range (ignored), and via the 9<->15 swap.
	p.processEvent(&midi.Event{Type: midi.NoteOn, Channel: 9, Param1: 38, Param2: 110})
	p.processEvent(&midi.Event{Type: midi.NoteOn, Channel: 9, Param1: 20, Param2: 110}) // key < 35: ignored
	p.processEvent(&midi.Event{Type: midi.NoteOn, Channel: 9, Param1: 90, Param2: 110}) // key > 81: ignored

	// A note-on with velocity 0 is a note-off.
	p.processEvent(&midi.Event{Type: midi.NoteOn, Channel: 0, Param1: 64, Param2: 60})
	p.processEvent(&midi.Event{Type: midi.NoteOn, Channel: 0, Param1: 64, Param2: 0})

	// All notes off.
	p.processEvent(&midi.Event{Type: midi.NoteOn, Channel: 0, Param1: 67, Param2: 80})
	p.processEvent(&midi.Event{Type: midi.Controller, Channel: 0, Param1: 0x7B})

	// Tempo change (adjusts queued callbacks via float32 rescaling).
	p.processEvent(&midi.Event{Type: midi.Meta, MetaType: midi.MetaSetTempo, Data: []byte{0x07, 0xA1, 0x20}})
	// Unknown/ignored controller and meta types.
	p.processEvent(&midi.Event{Type: midi.Controller, Channel: 0, Param1: 0x01, Param2: 10})
	p.processEvent(&midi.Event{Type: midi.Meta, MetaType: midi.MetaTrackName, Data: []byte("x")})
	// SysEx events are ignored.
	p.processEvent(&midi.Event{Type: midi.SysEx})
}

// findFeedbackInstrument returns a melodic instrument index whose primary voice
// uses non-modulated feedback with a modulator level below maximum, so that the
// extra volume-register write in setVoiceVolume is taken.
func findFeedbackInstrument(p *Player) int {
	for i := 0; i < 128; i++ {
		v := p.bank.Instruments[i].Voices[0]
		if v.Feedback&0x01 != 0 && v.Modulator.Level != 0x3f {
			return i
		}
	}
	return 0
}

// TestVoiceReplacementDoom19 forces the OPL2 (9 voice) freelist to empty so
// ReplaceExistingVoice runs.
func TestVoiceReplacementDoom19(t *testing.T) {
	p := newTestPlayer(t, false)
	for key := 40; key < 60; key++ { // more than 9 simultaneous notes
		p.processEvent(&midi.Event{Type: midi.NoteOn, Channel: 0, Param1: uint8(key), Param2: 90})
	}
	// getFreeVoice returning nil: force the freelist empty and try one more.
	p.freeNum = 0
	p.voiceKeyOn(&p.channels[0], 0, 0, 60, 60, 90)
}

// TestVoiceReplacementDoom1 covers the Doom 1 v1.666 driver quirks, including
// the double-voice release recursion.
func TestVoiceReplacementDoom1(t *testing.T) {
	p := newTestPlayer(t, true)
	p.oplDrvVer = drvDoom1_1_666

	dv := findDoubleVoiceInstrument(p)
	p.processEvent(&midi.Event{Type: midi.ProgramChange, Channel: 1, Param1: uint8(dv)})
	for key := 30; key < 60; key++ {
		p.processEvent(&midi.Event{Type: midi.NoteOn, Channel: 1, Param1: uint8(key), Param2: 90})
	}
	// Release them (double-voice release recursion for drv < 1.9).
	for key := 30; key < 60; key++ {
		p.processEvent(&midi.Event{Type: midi.NoteOff, Channel: 1, Param1: uint8(key)})
	}

	// Non-OPL3 forces voicenum to 1 in the Doom 1 path.
	p.opl3mode = false
	p.processEvent(&midi.Event{Type: midi.NoteOn, Channel: 1, Param1: 61, Param2: 90})
}

// TestVoiceReplacementDoom2 covers the Doom 2 v1.666 driver replacement path.
func TestVoiceReplacementDoom2(t *testing.T) {
	p := newTestPlayer(t, true)
	p.oplDrvVer = drvDoom2_1_666

	dv := findDoubleVoiceInstrument(p)
	p.processEvent(&midi.Event{Type: midi.ProgramChange, Channel: 2, Param1: uint8(dv)})
	for key := 30; key < 65; key++ {
		p.processEvent(&midi.Event{Type: midi.NoteOn, Channel: 2, Param1: uint8(key), Param2: 90})
	}
}

func findDoubleVoiceInstrument(p *Player) int {
	for i := 0; i < 128; i++ {
		if p.bank.Instruments[i].Flags&0x0004 != 0 {
			return i
		}
	}
	return 0
}

// TestPauseResumeStopVolume covers the transport controls and volume handling.
func TestPauseResumeStopVolume(t *testing.T) {
	p := newTestPlayer(t, false)

	// Play a couple of notes (main + percussion) so Pause has voices to key off.
	p.processEvent(&midi.Event{Type: midi.NoteOn, Channel: 0, Param1: 60, Param2: 100})
	p.processEvent(&midi.Event{Type: midi.NoteOn, Channel: 9, Param1: 40, Param2: 100})

	p.SetVolume(90) // change volume: exercises setMusicVolume channel loop
	p.SetVolume(90) // no change: early return
	p.SetVolume(64)

	p.Pause()
	buf := make([]int16, 256)
	p.Read(buf) // paused render path (advanceTime pause branch)
	p.Resume()

	p.Stop()
	if p.IsPlaying() {
		t.Fatal("IsPlaying true after Stop")
	}
}

// TestRestartSong covers restartSong directly and the queue clear/peek paths.
func TestRestartSong(t *testing.T) {
	p := newTestPlayer(t, false)
	p.restartSong()
	if p.runningTracks != p.numTracks {
		t.Fatalf("runningTracks=%d, want %d", p.runningTracks, p.numTracks)
	}
}

// TestLoopViaTrace runs the scheduler long enough for the song to reach
// END_OF_TRACK and loop, exercising the end-of-track restart callback.
func TestLoopViaTrace(t *testing.T) {
	p, err := newPlayer(mustRead(t, genmidiPath), 44100, false, false)
	if err != nil {
		t.Fatalf("newPlayer: %v", err)
	}
	if err := p.RegisterSong(mustRead(t, dintroPath)); err != nil {
		t.Fatalf("RegisterSong: %v", err)
	}
	p.Play(true)
	// D_INTRO is short; 120 simulated seconds is several loops.
	p.runTrace(120_000_000)
}

// TestChannelIndexDefensive covers the not-found branch of channelIndex.
func TestChannelIndexDefensive(t *testing.T) {
	p := newTestPlayer(t, false)
	if got := p.channelIndex(&channelData{}); got != -1 {
		t.Fatalf("channelIndex(foreign) = %d, want -1", got)
	}
}

// TestEmptyQueuePeek covers peek on an empty queue.
func TestEmptyQueuePeek(t *testing.T) {
	q := newQueue()
	if q.peek() != 0 {
		t.Fatal("empty queue peek should be 0")
	}
	if !q.isEmpty() {
		t.Fatal("new queue should be empty")
	}
}

// TestPlayWithoutSong covers the early return in Play when no song is loaded.
func TestPlayWithoutSong(t *testing.T) {
	p, err := New(mustRead(t, genmidiPath), 44100, false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	p.Play(true) // no RegisterSong: should be a no-op
	if p.IsPlaying() {
		t.Fatal("IsPlaying true without a registered song")
	}
}

// TestQueueOverflow covers the max-callbacks drop path in the queue.
func TestQueueOverflow(t *testing.T) {
	q := newQueue()
	for i := 0; i < maxOPLQueue+5; i++ {
		q.push(queueEntry{time: uint64(i)})
	}
	if q.numEntries != maxOPLQueue {
		t.Fatalf("numEntries=%d, want %d", q.numEntries, maxOPLQueue)
	}
}

// TestReleaseVoiceCrash covers the "Doom 2 1.666 OPL crash emulation" branch
// where the release index is out of range.
func TestReleaseVoiceCrash(t *testing.T) {
	p := newTestPlayer(t, false)
	p.processEvent(&midi.Event{Type: midi.NoteOn, Channel: 0, Param1: 60, Param2: 90})
	p.releaseVoice(p.allocedNum + 5)
	if p.allocedNum != 0 || p.freeNum != 0 {
		t.Fatalf("crash emulation should zero counts, got alloced=%d free=%d", p.allocedNum, p.freeNum)
	}
}

// TestFrequencyForVoiceExtremes covers the note-wrap, octave clamp, negative
// frequency-index clamp, fixed-note and fine-tuning branches of
// frequencyForVoice.
func TestFrequencyForVoiceExtremes(t *testing.T) {
	p := newTestPlayer(t, false)
	v := &p.voiceStore[0]
	v.channel = &p.channels[0]
	v.currentInstr = 0
	v.currentInstrVoice = 0

	// note > 95 wrap and high octave clamp.
	v.note = 200
	p.channels[0].bend = 300
	_ = p.frequencyForVoice(v)

	// Second-voice fine-tuning with a negative frequency index (clamped to 0).
	v.currentInstrVoice = 1
	v.note = 0
	p.channels[0].bend = -500
	_ = p.frequencyForVoice(v)

	// Fixed-note instrument: the base-note offset is not applied.
	fixed := -1
	for i := 0; i < len(p.bank.Instruments); i++ {
		if p.bank.Instruments[i].Flags&0x0001 != 0 {
			fixed = i
			break
		}
	}
	if fixed >= 0 {
		v.currentInstr = fixed
		v.currentInstrVoice = 0
		v.note = 60
		p.channels[0].bend = 0
		_ = p.frequencyForVoice(v)
	}
}

// TestSetChannelVolumeClip covers the start-volume clip branch.
func TestSetChannelVolumeClip(t *testing.T) {
	p := newTestPlayer(t, false)
	p.currentMusicVolume = 127
	p.startMusicVolume = 40
	p.processEvent(&midi.Event{Type: midi.NoteOn, Channel: 0, Param1: 60, Param2: 90})
	// Controller volume 100 with clipStart -> clamped to startMusicVolume.
	p.processEvent(&midi.Event{Type: midi.Controller, Channel: 0, Param1: 0x07, Param2: 100})
}

// TestReplaceDoom1MultiChannel forces ReplaceExistingVoiceDoom1 to pick a
// higher-numbered channel's voice.
func TestReplaceDoom1MultiChannel(t *testing.T) {
	p := newTestPlayer(t, false)
	p.oplDrvVer = drvDoom1_1_666
	p.opl3mode = false
	// Fill voices across ascending channels so the highest channel is stolen.
	for ch := 0; ch < 6; ch++ {
		p.processEvent(&midi.Event{Type: midi.NoteOn, Channel: uint8(ch), Param1: uint8(50 + ch), Param2: 90})
		p.processEvent(&midi.Event{Type: midi.NoteOn, Channel: uint8(ch), Param1: uint8(60 + ch), Param2: 90})
	}
}

// TestTrackTimerExhausted covers the !ok early return when the iterator is
// already past the end of the track.
func TestTrackTimerExhausted(t *testing.T) {
	p := newTestPlayer(t, false)
	it := p.tracks[0].iter
	for {
		if _, ok := it.Next(); !ok {
			break
		}
	}
	p.trackTimerCallback(0) // iterator exhausted -> no-op
}

// TestRegisterSongBadMUS covers the mus.Convert error branch.
func TestRegisterSongBadMUS(t *testing.T) {
	p := newTestPlayer(t, false)
	// Valid MUS magic but a truncated/garbage body so Convert fails.
	bad := append([]byte("MUS\x1a"), make([]byte, 8)...)
	if err := p.RegisterSong(bad); err == nil {
		t.Fatal("expected mus.Convert error for malformed MUS")
	}
}

// TestStereoCorrectPan covers the opl_stereo_correct pan-reversal branch.
func TestStereoCorrectPan(t *testing.T) {
	p := newTestPlayer(t, true)
	p.stereoCorrect = true
	p.processEvent(&midi.Event{Type: midi.NoteOn, Channel: 0, Param1: 60, Param2: 100})
	p.processEvent(&midi.Event{Type: midi.Controller, Channel: 0, Param1: 0x0A, Param2: 100})
	// Same resulting pan again -> the channel.pan == regPan (no-change) branch.
	p.processEvent(&midi.Event{Type: midi.Controller, Channel: 0, Param1: 0x0A, Param2: 100})
}

// TestReplaceDoom2Exact fills exactly numOplVoices-1 voices with single-voice
// notes, then triggers a double-voice note so the second
// ReplaceExistingVoiceDoom2 call (allocedNum == numOplVoices-1 && double) runs.
func TestReplaceDoom2Exact(t *testing.T) {
	p := newTestPlayer(t, true) // OPL3: 18 voices
	p.oplDrvVer = drvDoom2_1_666

	single := findSingleVoiceInstrument(p)
	p.processEvent(&midi.Event{Type: midi.ProgramChange, Channel: 3, Param1: uint8(single)})
	// Fill 17 single voices.
	for i := 0; i < 17; i++ {
		p.processEvent(&midi.Event{Type: midi.NoteOn, Channel: 3, Param1: uint8(40 + i), Param2: 90})
	}
	if p.allocedNum != p.numOplVoices-1 {
		t.Fatalf("setup: allocedNum=%d, want %d", p.allocedNum, p.numOplVoices-1)
	}
	// Now a double-voice note on the same channel.
	dv := findDoubleVoiceInstrument(p)
	p.processEvent(&midi.Event{Type: midi.ProgramChange, Channel: 3, Param1: uint8(dv)})
	p.processEvent(&midi.Event{Type: midi.NoteOn, Channel: 3, Param1: 80, Param2: 90})
}

func findSingleVoiceInstrument(p *Player) int {
	for i := 0; i < 128; i++ {
		if p.bank.Instruments[i].Flags&0x0004 == 0 {
			return i
		}
	}
	return 0
}
