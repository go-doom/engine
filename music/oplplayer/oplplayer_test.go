// SPDX-License-Identifier: GPL-2.0-or-later
// Go port of chocolate-doom i_oplmusic.c (Simon Howard et al.).
// Ported for the go-doom/engine authors.

package oplplayer

import (
	"os"
	"strings"
	"testing"
)

const (
	genmidiPath = "../genmidi/testdata/GENMIDI.lmp"
	dintroPath  = "../midi/testdata/D_INTRO.lmp"
	goldenTrace = "testdata/regtrace_dintro.txt"

	traceSampleRate = 44100
	traceBudgetUs   = 3_000_000
)

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

// runTrace drives the scheduler in the simplified event-driven mode used by the
// C oracle harness: current_us jumps directly to each callback's scheduled
// time. This mirrors OPL_RunTrace in scratchpad/opltrace/oplstub.c.
func (p *Player) runTrace(budgetUs uint64) {
	for !p.queue.isEmpty() {
		t := p.queue.peek()
		if t >= budgetUs {
			break
		}
		p.currentUs = t
		e := p.queue.pop()
		p.invoke(e)
	}
}

// formatTrace renders the captured register-write trace in the same
// "T<us> R<reg> V<val>" line format the C harness emits.
func formatTrace(entries []traceEntry) string {
	var sb strings.Builder
	for _, e := range entries {
		sb.WriteByte('T')
		sb.WriteString(itoa(int(e.us)))
		sb.WriteString(" R")
		sb.WriteString(itoa(e.reg))
		sb.WriteString(" V")
		sb.WriteString(itoa(e.val))
		sb.WriteByte('\n')
	}
	return sb.String()
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// TestRegisterTraceOracle is the differential acceptance gate. It runs the Go
// player in the same synchronous, microsecond-driven scheduling mode as the C
// oracle harness (scratchpad/opltrace) and asserts the captured (us, reg, val)
// register-write trace is byte-identical to the golden trace produced by
// chocolate-doom's i_oplmusic.c for the same D_INTRO + GENMIDI + config
// (OPL2 mode, opl_doom_1_9, stereo-correct off, music volume 127).
func TestRegisterTraceOracle(t *testing.T) {
	p, err := newPlayer(readFile(t, genmidiPath), traceSampleRate, false, true)
	if err != nil {
		t.Fatalf("newPlayer: %v", err)
	}
	if err := p.RegisterSong(readFile(t, dintroPath)); err != nil {
		t.Fatalf("RegisterSong: %v", err)
	}

	p.Play(true) // looping, to exercise RestartSong within the budget
	p.runTrace(traceBudgetUs)

	got := formatTrace(*p.trace)
	want := string(readFile(t, goldenTrace))

	if got == want {
		return
	}

	// Report the first differing line to make failures actionable.
	gl := strings.Split(got, "\n")
	wl := strings.Split(want, "\n")
	n := len(gl)
	if len(wl) < n {
		n = len(wl)
	}
	for i := 0; i < n; i++ {
		if gl[i] != wl[i] {
			t.Fatalf("register trace mismatch at line %d:\n  go: %q\n   c: %q\n(go lines=%d, c lines=%d)",
				i+1, gl[i], wl[i], len(gl), len(wl))
		}
	}
	t.Fatalf("register trace length mismatch: go lines=%d, c lines=%d", len(gl), len(wl))
}

// TestPCMSmoke renders one second of audio through the public API and asserts
// the output is non-silent and deterministic across two identical runs.
func TestPCMSmoke(t *testing.T) {
	const rate = 44100

	render := func() []int16 {
		p, err := New(readFile(t, genmidiPath), rate, false)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if err := p.RegisterSong(readFile(t, dintroPath)); err != nil {
			t.Fatalf("RegisterSong: %v", err)
		}
		p.SetVolume(127)
		p.Play(true)
		if !p.IsPlaying() {
			t.Fatal("IsPlaying returned false after Play")
		}
		buf := make([]int16, rate*2) // 1 second, stereo
		frames := p.Read(buf)
		if frames != rate {
			t.Fatalf("Read returned %d frames, want %d", frames, rate)
		}
		return buf
	}

	a := render()

	nonzero := 0
	for _, s := range a {
		if s != 0 {
			nonzero++
		}
	}
	if nonzero == 0 {
		t.Fatal("rendered audio is entirely silent")
	}

	b := render()
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("audio not deterministic: sample %d differs (%d vs %d)", i, a[i], b[i])
		}
	}
}
