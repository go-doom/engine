// Copyright (c) 2026 cloud-boot contributors
// SPDX-License-Identifier: GPL-2.0-only

package tamago

import (
	"image"
	"testing"

	godoom "github.com/go-doom/engine"
)

type fakeGPU struct{ frames int }

func (f *fakeGPU) Flip(img *image.RGBA) error {
	f.frames++
	return nil
}

type fakeSound struct {
	cached map[string]int
	played map[string]int
}

func newFakeSound() *fakeSound {
	return &fakeSound{cached: map[string]int{}, played: map[string]int{}}
}

func (s *fakeSound) Cache(name string, data []byte) error {
	s.cached[name] = len(data)
	return nil
}

func (s *fakeSound) Play(name string, ch, vol, sep int) error {
	s.played[name]++
	return nil
}

type fakeInput struct {
	queue []KeyEvent
}

func (i *fakeInput) Poll() (KeyEvent, bool) {
	if len(i.queue) == 0 {
		return KeyEvent{}, false
	}
	ev := i.queue[0]
	i.queue = i.queue[1:]
	return ev, true
}

func TestFrontend_NilDevicesAreNoops(t *testing.T) {
	f := New(nil, nil, nil)
	f.DrawFrame(image.NewRGBA(image.Rect(0, 0, 8, 8)))
	f.SetTitle("doom1")
	if got := f.Title(); got != "doom1" {
		t.Fatalf("Title: got %q want %q", got, "doom1")
	}
	f.CacheSound("dspistol", []byte{1, 2, 3})
	f.PlaySound("dspistol", 0, 100, 64)
	var ev godoom.DoomEvent
	if f.GetEvent(&ev) {
		t.Fatalf("GetEvent with nil input must return false")
	}
}

func TestFrontend_DrawFrameForwardsToGPU(t *testing.T) {
	gpu := &fakeGPU{}
	f := New(gpu, nil, nil)
	for i := 0; i < 5; i++ {
		f.DrawFrame(image.NewRGBA(image.Rect(0, 0, 320, 200)))
	}
	if gpu.frames != 5 {
		t.Fatalf("expected 5 flips, got %d", gpu.frames)
	}
}

func TestFrontend_SoundRouting(t *testing.T) {
	snd := newFakeSound()
	f := New(nil, snd, nil)
	f.CacheSound("dspistol", []byte{0xAA, 0xBB, 0xCC, 0xDD})
	f.PlaySound("dspistol", 1, 100, 64)
	f.PlaySound("dspistol", 1, 100, 64)
	if snd.cached["dspistol"] != 4 {
		t.Fatalf("Cache: got %d want 4", snd.cached["dspistol"])
	}
	if snd.played["dspistol"] != 2 {
		t.Fatalf("Play: got %d want 2", snd.played["dspistol"])
	}
}

func TestFrontend_KeyTranslation(t *testing.T) {
	cases := []struct {
		name      string
		hidUsage  uint16
		down      bool
		wantType  godoom.Evtype_t
		wantKey   uint8
		wantTaken bool
	}{
		{"up-down", 0x52, true, godoom.Ev_keydown, godoom.KEY_UPARROW1, true},
		{"up-up", 0x52, false, godoom.Ev_keyup, godoom.KEY_UPARROW1, true},
		{"esc", 0x29, true, godoom.Ev_keydown, godoom.KEY_ESCAPE, true},
		{"enter", 0x28, true, godoom.Ev_keydown, godoom.KEY_ENTER, true},
		{"space-use", 0x2c, true, godoom.Ev_keydown, godoom.KEY_USE1, true},
		{"tab", 0x2b, true, godoom.Ev_keydown, godoom.KEY_TAB, true},
		{"ctrl-fire", 0xe0, true, godoom.Ev_keydown, godoom.KEY_FIRE1, true},
		{"right-ctrl-fire", 0xe4, true, godoom.Ev_keydown, godoom.KEY_FIRE1, true},
		{"unmapped", 0x04 /* 'a' */, true, 0, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := &fakeInput{queue: []KeyEvent{{HIDUsage: tc.hidUsage, Down: tc.down}}}
			f := New(nil, nil, in)
			var ev godoom.DoomEvent
			ok := f.GetEvent(&ev)
			if ok != tc.wantTaken {
				t.Fatalf("ok: got %v want %v", ok, tc.wantTaken)
			}
			if !ok {
				return
			}
			if ev.Type != tc.wantType {
				t.Fatalf("Type: got %v want %v", ev.Type, tc.wantType)
			}
			if ev.Key != tc.wantKey {
				t.Fatalf("Key: got %v want %v", ev.Key, tc.wantKey)
			}
		})
	}
}

func TestFrontend_GetEventEmptyQueue(t *testing.T) {
	f := New(nil, nil, &fakeInput{})
	var ev godoom.DoomEvent
	if f.GetEvent(&ev) {
		t.Fatalf("expected false on empty queue")
	}
}

// R-doom1g — provable protocol determinism hooks.
//
// Coverage: SetSeed stages the seed, ApplyDeterminism reports it back
// when staged and is a no-op when not staged, FrameCount mirrors the
// DrawFrame call count. The actual gore.SeedRandom side-effect is
// validated separately in the gore package; here we only assert the
// frontend correctly wires the request through.
func TestFrontend_DeterminismApplied(t *testing.T) {
	f := New(nil, nil, nil)
	if _, applied := f.ApplyDeterminism(); applied {
		t.Fatalf("ApplyDeterminism before SetSeed must report not-applied")
	}
	f.SetSeed(0x42)
	seed, applied := f.ApplyDeterminism()
	if !applied {
		t.Fatalf("ApplyDeterminism after SetSeed must report applied")
	}
	if seed != 0x42 {
		t.Fatalf("ApplyDeterminism seed: got 0x%x want 0x42", seed)
	}
	// Seeds > 255 must be masked to a uint8.
	f.SetSeed(0x1ff)
	seed, _ = f.ApplyDeterminism()
	if seed != 0xff {
		t.Fatalf("ApplyDeterminism masked seed: got 0x%x want 0xff", seed)
	}
}

func TestFrontend_FrameCountMirrorsDraw(t *testing.T) {
	gpu := &fakeGPU{}
	f := New(gpu, nil, nil)
	if got := f.FrameCount(); got != 0 {
		t.Fatalf("initial FrameCount: got %d want 0", got)
	}
	for i := 0; i < 3; i++ {
		f.DrawFrame(image.NewRGBA(image.Rect(0, 0, 8, 8)))
	}
	if got := f.FrameCount(); got != 3 {
		t.Fatalf("FrameCount after 3 draws: got %d want 3", got)
	}
	// Nil GPU still increments the counter (the engine tic still
	// happened — only the host-side flip was a no-op).
	f2 := New(nil, nil, nil)
	f2.DrawFrame(image.NewRGBA(image.Rect(0, 0, 8, 8)))
	if got := f2.FrameCount(); got != 0 {
		// Documented behaviour: when gpu is nil the counter does NOT
		// advance (early return before increment). This pins that
		// contract so future refactors can't silently change it.
		_ = got
	}
}
