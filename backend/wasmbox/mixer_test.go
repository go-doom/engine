// Copyright (c) 2026 the go-doom/engine authors.
// SPDX-License-Identifier: GPL-2.0-only

package wasmbox

import "testing"

func TestMixerPlayAndFinish(t *testing.T) {
	m := NewMixer(100)
	m.Play(0, Sound{Rate: 100, Samples: []float32{0.5, 0.5, 0.5, 0.5}}, 127, 127) // centre: l=r
	if m.Active() != 1 {
		t.Fatal("voice not started")
	}
	dst := make([]float32, 2*6)
	m.Mix(dst)
	l, r := Pan(127, 127)
	for f := 0; f < 4; f++ {
		if dst[2*f] != 0.5*l || dst[2*f+1] != 0.5*r {
			t.Fatalf("frame %d = %v,%v", f, dst[2*f], dst[2*f+1])
		}
	}
	if dst[8] != 0 || dst[9] != 0 || dst[10] != 0 || dst[11] != 0 {
		t.Fatalf("frames after the end must stay silent: %v", dst[8:])
	}
	if m.Active() != 0 {
		t.Fatal("finished voice not dropped")
	}
}

func TestMixerResamplesLinearly(t *testing.T) {
	m := NewMixer(200) // source at 100 Hz: each source sample spans two output frames
	m.Play(1, Sound{Rate: 100, Samples: []float32{0, 1}}, 127, 127)
	l, _ := Pan(127, 127)
	dst := make([]float32, 2*3)
	m.Mix(dst)
	if dst[0] != 0 || dst[2] != 0.5*l || dst[4] != l {
		t.Fatalf("interpolation: %v", dst)
	}
}

func TestMixerChannelReplacedAndPanned(t *testing.T) {
	m := NewMixer(100)
	m.Play(3, Sound{Rate: 100, Samples: []float32{1, 1}}, 127, 0) // hard left
	m.Play(3, Sound{Rate: 100, Samples: []float32{1, 1}}, 127, 254) // replaces: hard right
	if m.Active() != 1 {
		t.Fatalf("active = %d", m.Active())
	}
	dst := make([]float32, 2)
	m.Mix(dst)
	if dst[0] >= dst[1] {
		t.Fatalf("expected right-heavy, got %v", dst)
	}
}

func TestMixerRejectsEmptyAndClamps(t *testing.T) {
	m := NewMixer(100)
	m.Play(0, Sound{Rate: 100}, 127, 127)
	m.Play(0, Sound{Rate: 0, Samples: []float32{1}}, 127, 127)
	if m.Active() != 0 {
		t.Fatal("empty or rateless sound accepted")
	}
	m.Play(0, Sound{Rate: 100, Samples: []float32{1, 1}}, 127, 127)
	m.Play(1, Sound{Rate: 100, Samples: []float32{-1, -1}}, 127, 127)
	m.Play(2, Sound{Rate: 100, Samples: []float32{1, 1}}, 127, 127)
	m.Play(4, Sound{Rate: 100, Samples: []float32{1, 1}}, 127, 127)
	dst := []float32{0.9, 0.9, -0.9, -0.9, 7}
	m.Mix(dst)
	for i, v := range dst[:4] {
		if v > 1 || v < -1 {
			t.Fatalf("sample %d = %v out of range", i, v)
		}
	}
	if dst[4] != 7 {
		t.Fatal("trailing odd sample must be left alone")
	}
}
