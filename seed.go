// Copyright (c) 2026 cloud-boot contributors
// SPDX-License-Identifier: GPL-2.0-only
//
// This file is part of go-doom/engine, a fork of github.com/AndreRenaud/gore
// (Pure-Go minimal Doom implementation, GPL-2.0). It is a NEW sibling file
// that EXPORTS deterministic-execution hooks the engine already implements
// internally, WITHOUT modifying the mechanically-transpiled doom.go. The
// rule "doom.go is not hand-edited" is preserved: this file only writes
// package-local variables and calls package-local functions.

package gore

// SeedRandom resets DOOM's two PRNG indices (p_random + m_random) to a
// known starting position. The DOOM PRNG is the canonical 256-entry table
// indexed by `prndindex` / `rndindex` (see doom.go around the p_Random /
// m_Random definitions); reset to a known index produces a fully
// deterministic random sequence for the rest of the engine run, which is
// exactly the precondition the provable-protocol test runner needs.
//
// Calling with seed == 0 reproduces the historical DOOM startup state
// (both indices == 0). Any 0..255 value is valid; values outside that
// range are taken modulo 256.
//
// SAFETY: must be called BEFORE Run, or from within the engine's main
// thread between tics. Concurrent invocation while the engine is reading
// the indices is undefined.
func SeedRandom(seed uint8) {
	prndindex = int32(seed)
	rndindex = int32(seed)
}

// SetDeterministicTics switches the engine's internal clock from
// wall-clock (time.Since(start_time)) to a frame-driven counter:
// every call to i_Sleep increments a synthetic tic counter, and every
// call to i_GetTicks reads that counter. The effect is a tic-locked
// run where one i_Sleep == one tic, regardless of host scheduling.
//
// This is the existing `dg_run_full_speed` knob that the engine's
// own TestDoomDemo + TestLoadSave already rely on. Exposing it here
// lets host-side test runners (and the harvest-reference tool) opt
// into the same determinism without reaching into unexported state.
//
// SAFETY: same caveats as SeedRandom — set before Run for a clean
// fully-deterministic execution.
func SetDeterministicTics(enable bool) {
	dg_run_full_speed = enable
}

// ResetClock zeroes the synthetic tic counter (dg_fake_tics) and the
// monotonic basetime so that the next i_GetTime returns 0. Combined
// with SetDeterministicTics(true) this gives the engine a clean
// "tic 0 at Run entry" starting condition, which is the second
// precondition for the provable protocol (alongside SeedRandom).
//
// Calling between two Run invocations is supported and recommended:
// without it the second run inherits the first run's basetime and
// the tic timeline jumps mid-execution.
func ResetClock() {
	dg_fake_tics = 0
	basetime = 0
	last_tick = 0
	firsttime = 1
}

// CurrentTic returns the engine's current tic counter as observed by
// i_GetTime. Useful for the harvest-reference tool to checkpoint
// framebuffers at exact tic boundaries: callers poll CurrentTic from
// the DrawFrame callback and snapshot when CurrentTic matches an
// expected checkpoint value.
//
// In deterministic mode this is exactly equal to the number of
// completed tics since ResetClock; in wall-clock mode it is the
// wall-clock-derived tic count and is therefore non-deterministic.
func CurrentTic() int32 {
	return i_GetTime()
}

// RandomState returns the current (prndindex, rndindex) pair. The
// harvest-reference tool records this value alongside the framebuffer
// hash at every checkpoint, so the test runner can detect PRNG drift
// independently of frame-pixel drift — useful when a frame mismatch
// could plausibly come from either a PRNG step or a missed input.
func RandomState() (prnd, mrnd int32) {
	return prndindex, rndindex
}
