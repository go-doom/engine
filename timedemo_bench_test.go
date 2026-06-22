// Copyright (c) 1993-1996 id Software, Inc.
// Copyright (c) 2026 the go-doom/engine authors.
// SPDX-License-Identifier: GPL-2.0-or-later

package gore

import (
	"image"
	"os"
	"testing"
	"time"
)

// benchFrontend is a headless, zero-cost DoomFrontend used by the
// performance-parity timedemo benchmark. It records every rendered
// frame (DrawFrame) and the wall-clock window over which they arrived
// so we can derive ms/frame and FPS for comparison against
// chocolate-doom's `-timedemo demo1` (which reports the same
// gametics/realtics/FPS figure). It never copies the framebuffer or
// touches I/O so the measured time is dominated by the engine's
// software renderer (r_RenderPlayerView -> r_DrawColumn / r_DrawSpan),
// matching the C engine's headless dummy-video path.
type benchFrontend struct {
	frames    int64
	firstAt   time.Time
	lastAt    time.Time
	stopAfter int64 // Stop() after this many rendered frames
	checksum  uint64
	firstTic  int32 // gametic at first counted frame
	lastTic   int32 // gametic at last counted frame
	ticsSeen  map[int32]struct{}
}

func (b *benchFrontend) DrawFrame(img *image.RGBA) {
	// Count ONLY frames where the engine ran the 3D world renderer
	// (r_RenderPlayerView): gamestate gs_LEVEL during demo playback.
	// The title/credit pages (gs_DEMOSCREEN) are a flat patch blit and
	// must not dilute the frame-time, just as chocolate-doom's
	// `-timedemo demo1` times only the demo's gameplay frames.
	if gamestate != gs_LEVEL || demoplayback == 0 || gametic == 0 {
		return
	}
	// Only count the FIRST redraw of each distinct gametic: at full
	// speed the loop may redraw an unchanged world several times per
	// advanced demo tic; those duplicate cache-hot redraws would
	// understate per-demo-frame cost. chocolate-doom renders exactly
	// once per gametic, so we mirror that by timing only fresh tics.
	if b.ticsSeen == nil {
		b.ticsSeen = make(map[int32]struct{}, 16384)
	}
	if _, seen := b.ticsSeen[gametic]; seen {
		return
	}
	now := time.Now()
	if b.frames == 0 {
		b.firstAt = now
		b.firstTic = gametic
	}
	b.frames++
	b.lastAt = now
	b.lastTic = gametic
	b.ticsSeen[gametic] = struct{}{}
	// Touch a handful of pixels so the renderer's output cannot be
	// dead-code-eliminated, without paying a full-frame copy.
	if len(img.Pix) >= 256 {
		for i := 0; i < 256; i += 16 {
			b.checksum += uint64(img.Pix[i])
		}
	}
	if b.stopAfter > 0 && b.frames >= b.stopAfter {
		Stop()
	}
}

func (b *benchFrontend) SetTitle(string)                 {}
func (b *benchFrontend) CacheSound(string, []byte)       {}
func (b *benchFrontend) PlaySound(string, int, int, int) {}
func (b *benchFrontend) GetEvent(ev *DoomEvent) bool     { return false }

// BenchmarkTimedemoDemo1 runs the built-in DEMO1 playback at full speed
// (no frame-rate cap) headless, and reports the wall-clock frame time of
// the software renderer. This is the full-engine parity benchmark vs
// chocolate-doom 3.1.1 `-timedemo demo1` on the same host.
//
// It is NOT a normal go-benchmark loop: the engine keeps global state
// across a Run() and cannot be re-entered, so we run the demo exactly
// once for a fixed frame budget (b.N is ignored; invoke with
// -benchtime=1x). The reported custom metrics are the headline numbers:
//
//	ms/frame  -- wall ms per rendered software frame
//	fps       -- rendered software frames per wall second
//
// Skips cleanly if the shareware/Freedoom IWAD is not present (CI
// without the WAD), so it never breaks the test gate.
func BenchmarkTimedemoDemo1(b *testing.B) {
	wad := findBenchWAD()
	if wad == "" {
		b.Skip("no IWAD found (set DOOM_BENCH_WAD or place doom1.wad); skipping timedemo benchmark")
	}

	// Full speed: never sleep, advance the fake tic clock once per frame
	// so the demo plays as fast as the renderer can produce frames --
	// the same intent as chocolate-doom's -timedemo (uncapped).
	dg_run_full_speed = true

	fe := &benchFrontend{stopAfter: 5026}

	// Safety net: if the demo somehow never reaches the frame budget,
	// bound the whole run so the benchmark can never hang.
	go func() {
		time.Sleep(60 * time.Second)
		Stop()
	}()

	start := time.Now()
	Run(fe, []string{"-iwad", wad, "-nosound", "-nomusic"})
	wall := fe.lastAt.Sub(fe.firstAt)
	if fe.frames < 2 || wall <= 0 {
		b.Fatalf("timedemo produced too few frames: frames=%d wall=%v", fe.frames, wall)
	}

	distinctTics := int64(len(fe.ticsSeen))
	msPerFrame := float64(wall.Nanoseconds()) / 1e6 / float64(fe.frames)
	fps := float64(fe.frames) / wall.Seconds()
	// Per-distinct-gametic timing: directly comparable to chocolate-doom
	// -timedemo, which times distinct demo gametics (one rendered view
	// per advanced tic), not raw redraw count.
	msPerTic := float64(wall.Nanoseconds()) / 1e6 / float64(distinctTics)
	ticFPS := float64(distinctTics) / wall.Seconds()
	b.ReportMetric(msPerTic, "ms/demoframe")
	b.ReportMetric(ticFPS, "demofps")
	b.ReportMetric(msPerFrame, "ms/redraw")
	b.ReportMetric(fps, "redrawfps")
	b.Logf("rendered %d redraws over %d distinct gametics (gametic %d..%d) in %v (startup excluded: %v)",
		fe.frames, distinctTics, fe.firstTic, fe.lastTic, wall, time.Since(start)-wall)
	b.Logf("  per distinct demo gametic: %.4f ms (%.1f demo-fps); per redraw: %.4f ms (%.1f fps); checksum=%d",
		msPerTic, ticFPS, msPerFrame, fps, fe.checksum)
}

// findBenchWAD locates an IWAD for the benchmark: explicit env override
// first, then the repo's embedwad copy, then the cwd.
func findBenchWAD() string {
	if p := os.Getenv("DOOM_BENCH_WAD"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	for _, c := range []string{"embedwad/doom1.wad", "doom1.wad", "freedoom1.wad", "embedwad/freedoom1.wad"} {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}
