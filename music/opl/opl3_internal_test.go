// SPDX-License-Identifier: LGPL-2.1-or-later
// Go port of Nuked OPL3 (github.com/nukeykt/Nuked-OPL3) by Nuke.YKT.
// Ported for the go-doom/engine authors. Original C: LGPL-2.1-or-later.

package opl

import "testing"

// TestResetMethod exercises the exported Reset method directly (as opposed to
// NewChip) and confirms it yields the same bit-exact output as the golden.
func TestResetMethod(t *testing.T) {
	want := loadGolden(t, "golden_stream.pcm")
	c := &Chip{}
	c.Reset(22050) // wrong rate + dirty state
	for i := 0; i < 40; i++ {
		c.WriteReg(uint16(0x20+i), uint8(i*7))
	}
	buf := make([]int16, 64)
	c.GenerateStream(buf, 32)

	// Now Reset to the oracle rate and replay the immediate-write script.
	c.Reset(49716)
	out := make([]int16, 0, len(want))
	l := lcg{state: 0xC0FFEE11}
	frame := make([]int16, 2)
	gen := func() {
		c.GenerateStream(frame, 1)
		out = append(out, frame[0], frame[1])
	}
	for i := range proReg {
		c.WriteReg(proReg[i], proVal[i])
	}
	for i := 0; i < 2000; i++ {
		gen()
	}
	c.WriteReg(0x0b0, 0x0a)
	for i := 0; i < 500; i++ {
		gen()
	}
	for r := 0; r < 300; r++ {
		nw := 1 + ((l.next() >> 28) & 7)
		for k := uint32(0); k < nw; k++ {
			x := l.next()
			c.WriteReg(uint16(x&0x1ff), uint8((x>>9)&0xff))
		}
		frames := 4 + ((l.next() >> 26) & 0x3f)
		for k := uint32(0); k < frames; k++ {
			gen()
		}
	}
	assertBitExact(t, "Reset-method replay", out, want)
}

// TestClipSample covers both saturation branches and the pass-through of
// OPL3_ClipSample.
func TestClipSample(t *testing.T) {
	cases := []struct {
		in   int32
		want int16
	}{
		{0, 0},
		{100, 100},
		{-100, -100},
		{32767, 32767},
		{-32768, -32768},
		{32768, 32767},   // positive saturation
		{999999, 32767},  // positive saturation
		{-32769, -32768}, // negative saturation
		{-999999, -32768},
	}
	for _, c := range cases {
		if got := opl3ClipSample(c.in); got != c.want {
			t.Errorf("opl3ClipSample(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestRhythmKeyPaths drives the percussion mode both fully on (KeyOn branches)
// and rhythm-enabled-but-drums-off (KeyOff branches), plus rhythm off.
func TestRhythmKeyPaths(t *testing.T) {
	c := NewChip(49716)
	c.WriteReg(0x105, 0x01)
	// Give the rhythm operators an audible envelope.
	for _, r := range []uint16{0x20, 0x23, 0x24, 0x25, 0x51, 0x52, 0x53, 0x54, 0x55} {
		c.WriteReg(r, 0x01)
	}
	for _, r := range []uint16{0x60, 0x63, 0x64, 0x65, 0x80, 0x83, 0x84, 0x85} {
		c.WriteReg(r, 0xf0)
	}
	buf := make([]int16, 256)
	c.WriteReg(0xbd, 0x3f) // rhythm on + all 5 drums keyed on
	c.GenerateStream(buf, 128)
	c.WriteReg(0xbd, 0x20) // rhythm still on, every drum keyed off
	c.GenerateStream(buf, 128)
	c.WriteReg(0xbd, 0x00) // rhythm off
	c.GenerateStream(buf, 128)
}

// TestSetupAlgReturnBit3 covers the defensive early return in
// opl3ChannelSetupAlg for a channel whose alg has bit 3 set (0x08). In normal
// register flow this channel is a 4-op member whose setup is driven through
// its pair, so this guard is only reachable defensively; exercise it directly.
func TestSetupAlgReturnBit3(t *testing.T) {
	c := NewChip(49716)
	ch := &c.channel[0]
	before := ch.out
	ch.alg = 0x08
	opl3ChannelSetupAlg(ch)
	if ch.out != before {
		t.Errorf("SetupAlg with alg&0x08 modified routing: %v -> %v", before, ch.out)
	}
}

// TestTremoloPosWrap forces the tremolopos == 210 wrap branch in the
// per-sample housekeeping.
func TestTremoloPosWrap(t *testing.T) {
	c := NewChip(49716)
	c.tremolopos = 209
	c.timer = 0x3f // (timer & 0x3f) == 0x3f triggers the increment
	buf := make([]int16, 2)
	c.Generate(buf)
	if c.tremolopos != 0 {
		t.Errorf("tremolopos did not wrap: got %d, want 0", c.tremolopos)
	}
}

// TestEgTimerWrap forces the 36-bit envelope-timer wrap branch, which is not
// reachable within a practical test run (it takes ~2^36 samples of real time)
// but is deterministic when the state is set directly.
func TestEgTimerWrap(t *testing.T) {
	c := NewChip(49716)
	c.egTimer = 0xfffffffff
	c.egTimerrem = 0
	c.egState = 1
	buf := make([]int16, 2)
	c.Generate(buf)
	if c.egTimer != 0 || c.egTimerrem != 1 {
		t.Errorf("egTimer wrap not taken: egTimer=%#x egTimerrem=%d", c.egTimer, c.egTimerrem)
	}
}

// TestWriteBufferedRingReuse floods the write buffer with more pending
// buffered writes than the ring can hold without generating, forcing the
// branch that flushes the slot being overwritten (wb.reg & 0x200 set).
func TestWriteBufferedRingReuse(t *testing.T) {
	c := NewChip(49716)
	c.WriteReg(0x105, 0x01)
	// writebufSize+64 buffered writes with no Generate in between: the ring
	// wraps and OPL3_WriteRegBuffered must flush the stale entry.
	for i := 0; i < writebufSize+64; i++ {
		c.WriteRegBuffered(0x040, uint8(i&0x3f))
	}
	// It must still generate without panicking and eventually drain.
	buf := make([]int16, 4)
	c.GenerateStream(buf, 2)
}
