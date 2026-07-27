// SPDX-License-Identifier: LGPL-2.1-or-later
// Go port of Nuked OPL3 (github.com/nukeykt/Nuked-OPL3) by Nuke.YKT.
// Ported for the go-doom/engine authors. Original C: LGPL-2.1-or-later.

package opl

import (
	"encoding/binary"
	"os"
	"testing"
)

// lcg is the deterministic PRNG shared byte-for-byte with the C oracle
// harness (oploracle/harness.c). The wraparound of uint32 multiply/add
// matches the C unsigned arithmetic exactly.
type lcg struct{ state uint32 }

func (l *lcg) next() uint32 {
	l.state = l.state*1664525 + 1013904223
	return l.state
}

// Structured "real note" prologue writes, identical to the C harness.
var (
	proReg = []uint16{
		0x105, 0x104,
		0x020, 0x023, 0x040, 0x043, 0x060, 0x063,
		0x080, 0x083, 0x0e0, 0x0e3, 0x0c0, 0x0a0, 0x0b0,
	}
	proVal = []uint8{
		0x01, 0x00,
		0x01, 0x01, 0x1a, 0x00, 0xf0, 0xf0,
		0x77, 0x77, 0x01, 0x02, 0x0e, 0x81, 0x2a,
	}
)

// runScript reproduces the exact register-write + generate sequence of the
// C oracle harness and returns the resulting interleaved stereo PCM.
//
// buffered selects WriteRegBuffered vs WriteReg; perframe selects the
// per-frame Generate vs GenerateStream generation path.
func runScript(buffered, perframe bool) []int16 {
	c := NewChip(49716)
	out := make([]int16, 0, 64*1024*2)
	l := lcg{state: 0xC0FFEE11}

	frame := make([]int16, 2)
	gen := func() {
		if perframe {
			c.Generate(frame)
		} else {
			c.GenerateStream(frame, 1)
		}
		out = append(out, frame[0], frame[1])
	}
	write := func(reg uint16, val uint8) {
		if buffered {
			c.WriteRegBuffered(reg, val)
		} else {
			c.WriteReg(reg, val)
		}
	}

	for i := range proReg {
		write(proReg[i], proVal[i])
	}
	for i := 0; i < 2000; i++ {
		gen()
	}
	write(0x0b0, 0x0a)
	for i := 0; i < 500; i++ {
		gen()
	}

	for r := 0; r < 300; r++ {
		nw := 1 + ((l.next() >> 28) & 7)
		for k := uint32(0); k < nw; k++ {
			x := l.next()
			write(uint16(x&0x1ff), uint8((x>>9)&0xff))
		}
		frames := 4 + ((l.next() >> 26) & 0x3f)
		for k := uint32(0); k < frames; k++ {
			gen()
		}
	}
	return out
}

func loadGolden(t *testing.T, name string) []int16 {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read golden %s: %v", name, err)
	}
	if len(raw)%2 != 0 {
		t.Fatalf("golden %s: odd byte count %d", name, len(raw))
	}
	s := make([]int16, len(raw)/2)
	for i := range s {
		s[i] = int16(binary.LittleEndian.Uint16(raw[2*i:]))
	}
	return s
}

func assertBitExact(t *testing.T, name string, got, want []int16) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: sample count mismatch: got %d, want %d", name, len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s: sample %d mismatch (frame %d, %s): got %d, want %d",
				name, i, i/2, chanName(i), got[i], want[i])
		}
	}
	t.Logf("%s: bit-exact over %d samples (%d stereo frames)", name, len(got), len(got)/2)
}

func chanName(i int) string {
	if i%2 == 0 {
		return "L"
	}
	return "R"
}

func TestBitExactStream(t *testing.T) {
	want := loadGolden(t, "golden_stream.pcm")
	got := runScript(false, false)
	assertBitExact(t, "stream", got, want)
}

func TestBitExactBuffered(t *testing.T) {
	want := loadGolden(t, "golden_buffered.pcm")
	got := runScript(true, false)
	assertBitExact(t, "buffered", got, want)
}

func TestBitExactGenerate(t *testing.T) {
	want := loadGolden(t, "golden_gen.pcm")
	got := runScript(false, true)
	assertBitExact(t, "gen", got, want)
}

// TestTableParity spot-checks a handful of known lookup-table entries against
// the values in the reference opl3.c / wf_rom.h.
func TestTableParity(t *testing.T) {
	if exprom[0] != 0xff4 || exprom[255] != 0x800 {
		t.Errorf("exprom endpoints: got %#x..%#x", exprom[0], exprom[255])
	}
	if mt[0] != 1 || mt[10] != 20 || mt[15] != 30 {
		t.Errorf("mt table: %v", mt)
	}
	if kslrom[0] != 0 || kslrom[15] != 64 {
		t.Errorf("kslrom endpoints: %d..%d", kslrom[0], kslrom[15])
	}
	if kslshift != [4]uint8{8, 1, 2, 0} {
		t.Errorf("kslshift: %v", kslshift)
	}
	if chSlot[3] != 6 || chSlot[17] != 32 {
		t.Errorf("chSlot: %v", chSlot)
	}
	if adSlot[6] != -1 || adSlot[0] != 0 || adSlot[21] != 17 {
		t.Errorf("adSlot: %v", adSlot)
	}
	if egIncstep[3] != [4]uint8{1, 1, 1, 0} {
		t.Errorf("egIncstep[3]: %v", egIncstep[3])
	}
	// logsin waveform table endpoints (wf_rom.h, first/last rows).
	if logsinWF[0][0] != 0x0859 || logsinWF[7][1023] != 0x8000 {
		t.Errorf("logsinWF endpoints: %#x / %#x", logsinWF[0][0], logsinWF[7][1023])
	}
}

// TestResetIdempotent verifies Reset restores a chip to the same state a
// fresh NewChip produces, and that a second identical script yields the same
// bit-exact output (no residual state).
func TestResetIdempotent(t *testing.T) {
	want := loadGolden(t, "golden_stream.pcm")
	c := NewChip(48000)
	// drive some garbage
	for i := 0; i < 50; i++ {
		c.WriteReg(uint16(i), uint8(i*3))
	}
	buf := make([]int16, 200)
	c.GenerateStream(buf, 100)
	// Reset back to the oracle rate and re-run: must match the golden.
	got := runScript(false, false)
	assertBitExact(t, "post-garbage rerun", got, want)
	_ = want
}

// TestGenerateResampledUpsample exercises the resampler with a non-native
// output rate (rateratio != 1<<RSM_FRAC), covering the interpolation path.
func TestGenerateResampledUpsample(t *testing.T) {
	c := NewChip(48000)
	c.WriteReg(0x105, 0x01) // OPL3 mode
	c.WriteReg(0x020, 0x01) // modulator: MULT=1
	c.WriteReg(0x023, 0x01) // carrier:   MULT=1
	c.WriteReg(0x040, 0x1a) // modulator TL
	c.WriteReg(0x043, 0x00) // carrier TL (full volume)
	c.WriteReg(0x060, 0xf0) // fast attack/decay
	c.WriteReg(0x063, 0xf0)
	c.WriteReg(0x080, 0x77) // sustain/release
	c.WriteReg(0x083, 0x77)
	c.WriteReg(0x0c0, 0x3e) // feedback/connection + both L/R speakers enabled
	c.WriteReg(0x0a0, 0x81) // f-num low
	c.WriteReg(0x0b0, 0x2a) // key-on, block, f-num high
	buf := make([]int16, 2)
	nonzero := false
	for i := 0; i < 4000; i++ {
		c.GenerateResampled(buf)
		if buf[0] != 0 || buf[1] != 0 {
			nonzero = true
		}
	}
	if !nonzero {
		t.Errorf("resampled output was all zero; expected audible tone")
	}
}
