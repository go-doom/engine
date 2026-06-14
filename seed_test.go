// Copyright (c) 2026 cloud-boot contributors
// SPDX-License-Identifier: GPL-2.0-only

package gore

import (
	"testing"
)

// TestSeedRandom_ResetsIndices proves SeedRandom sets BOTH PRNG
// indices to the requested value. This is the contract the provable
// test runner depends on — a single SeedRandom call must fully
// determine the engine's subsequent random sequence.
func TestSeedRandom_ResetsIndices(t *testing.T) {
	// Burn the indices to non-zero so we can detect the reset.
	prndindex = 99
	rndindex = 200

	SeedRandom(0)
	if prndindex != 0 || rndindex != 0 {
		t.Fatalf("SeedRandom(0): got (%d,%d) want (0,0)", prndindex, rndindex)
	}

	SeedRandom(42)
	if prndindex != 42 || rndindex != 42 {
		t.Fatalf("SeedRandom(42): got (%d,%d) want (42,42)", prndindex, rndindex)
	}

	// Round-trip via RandomState.
	gotP, gotM := RandomState()
	if gotP != 42 || gotM != 42 {
		t.Fatalf("RandomState: got (%d,%d) want (42,42)", gotP, gotM)
	}
}

// TestSeedRandom_DeterministicSequence proves that two runs with the
// same seed produce IDENTICAL p_Random output streams, which is the
// foundational property the harvest-reference tool relies on.
func TestSeedRandom_DeterministicSequence(t *testing.T) {
	SeedRandom(0)
	first := make([]int32, 16)
	for i := range first {
		first[i] = p_Random()
	}

	SeedRandom(0)
	second := make([]int32, 16)
	for i := range second {
		second[i] = p_Random()
	}

	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("p_Random sequence diverged at i=%d: first=%d second=%d",
				i, first[i], second[i])
		}
	}

	// Sanity: a different seed must produce a DIFFERENT sequence
	// (otherwise the seed isn't actually doing anything).
	SeedRandom(128)
	third := make([]int32, 16)
	for i := range third {
		third[i] = p_Random()
	}
	allEqual := true
	for i := range first {
		if first[i] != third[i] {
			allEqual = false
			break
		}
	}
	if allEqual {
		t.Fatalf("p_Random produced identical sequences for seed 0 and 128")
	}
}

func TestSetDeterministicTics_Toggles(t *testing.T) {
	defer func() { dg_run_full_speed = false }()

	SetDeterministicTics(true)
	if !dg_run_full_speed {
		t.Fatalf("SetDeterministicTics(true): dg_run_full_speed still false")
	}
	SetDeterministicTics(false)
	if dg_run_full_speed {
		t.Fatalf("SetDeterministicTics(false): dg_run_full_speed still true")
	}
}

// TestResetClock_ZerosFakeTics + adjacent state. We exercise the
// deterministic clock path because the wall-clock path depends on
// time.Now which is not test-friendly.
func TestResetClock_DeterministicMode(t *testing.T) {
	defer func() { dg_run_full_speed = false; dg_fake_tics = 0; basetime = 0; last_tick = 0; firsttime = 1 }()

	SetDeterministicTics(true)
	dg_fake_tics = 1000
	basetime = 12345
	last_tick = 99
	firsttime = 0

	ResetClock()

	if dg_fake_tics != 0 {
		t.Fatalf("ResetClock: dg_fake_tics=%d want 0", dg_fake_tics)
	}
	if basetime != 0 {
		t.Fatalf("ResetClock: basetime=%d want 0", basetime)
	}
	if last_tick != 0 {
		t.Fatalf("ResetClock: last_tick=%d want 0", last_tick)
	}
	if firsttime != 1 {
		t.Fatalf("ResetClock: firsttime=%d want 1", firsttime)
	}

	// After ResetClock + deterministic mode, i_GetTicks returns the
	// (now zero) fake tic counter directly. CurrentTic wraps i_GetTime
	// which does the milliseconds-to-tic conversion, but at fake_tics
	// == 0 the result is still 0.
	if got := CurrentTic(); got != 0 {
		t.Fatalf("CurrentTic immediately after ResetClock: got %d want 0", got)
	}
}

func TestRandomState_ReportsCurrent(t *testing.T) {
	SeedRandom(0)
	if p, m := RandomState(); p != 0 || m != 0 {
		t.Fatalf("RandomState after Seed(0): got (%d,%d) want (0,0)", p, m)
	}
	// Advance prndindex via p_Random and confirm RandomState observes
	// the advance.
	_ = p_Random()
	if p, _ := RandomState(); p != 1 {
		t.Fatalf("RandomState after one p_Random: prndindex=%d want 1", p)
	}
}
