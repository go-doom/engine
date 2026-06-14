// Copyright (c) 2026 cloud-boot contributors
// SPDX-License-Identifier: GPL-2.0-only

package tamago

import (
	"image"
	"testing"
)

type stubFB struct {
	flushed int
	err     error
}

func (f *stubFB) Flush() error {
	f.flushed++
	return f.err
}

func TestNewGPUAdapter_Flip_Centers(t *testing.T) {
	const W, H = 640, 400
	pix := make([]byte, W*H*4)
	fb := &stubFB{}
	g := NewGPUAdapter(fb, pix, W, H)

	frame := image.NewRGBA(image.Rect(0, 0, 320, 200))
	// Set a known top-left pixel to test centering + RGBA→BGRA swap.
	frame.Pix[0] = 0x11 // R
	frame.Pix[1] = 0x22 // G
	frame.Pix[2] = 0x33 // B
	frame.Pix[3] = 0x44 // A

	if err := g.Flip(frame); err != nil {
		t.Fatalf("Flip: %v", err)
	}
	if fb.flushed != 1 {
		t.Fatalf("Flush count: got %d want 1", fb.flushed)
	}
	// R-doom1i: scale = min(640/320, 400/200) = 2 → DOOM 320×200 scales
	// to 640×400 = fills the entire framebuffer (xOff=yOff=0, no border).
	// The single top-left source pixel is replicated into the 2×2 dest
	// pixel block (0,0)..(1,1).
	dstStride := W * 4
	want := [4]byte{0x33, 0x22, 0x11, 0x44} // BGRA
	for dy := 0; dy < 2; dy++ {
		for dx := 0; dx < 2; dx++ {
			off := dy*dstStride + dx*4
			got := [4]byte{pix[off+0], pix[off+1], pix[off+2], pix[off+3]}
			if got != want {
				t.Fatalf("BGRA pixel at (%d,%d): got %v want %v (scale=2 replication)", dx, dy, got, want)
			}
		}
	}
}

func TestNewGPUAdapter_Flip_LargerThanFB_Clips(t *testing.T) {
	const W, H = 100, 100
	pix := make([]byte, W*H*4)
	fb := &stubFB{}
	g := NewGPUAdapter(fb, pix, W, H)
	frame := image.NewRGBA(image.Rect(0, 0, 320, 200)) // bigger than fb
	if err := g.Flip(frame); err != nil {
		t.Fatalf("Flip: %v", err)
	}
	if fb.flushed != 1 {
		t.Fatalf("Flush count: got %d want 1", fb.flushed)
	}
}

func TestGPUAdapter_NilGuards(t *testing.T) {
	var g *GPUAdapter
	if err := g.Flip(image.NewRGBA(image.Rect(0, 0, 320, 200))); err != nil {
		t.Fatalf("nil Flip: %v", err)
	}
	g = NewGPUAdapter(nil, nil, 0, 0)
	if err := g.Flip(image.NewRGBA(image.Rect(0, 0, 320, 200))); err != nil {
		t.Fatalf("empty Flip: %v", err)
	}
	g = NewGPUAdapter(&stubFB{}, make([]byte, 4), 1, 1)
	if err := g.Flip(nil); err != nil {
		t.Fatalf("nil frame Flip: %v", err)
	}
	// Zero-dim frame.
	zero := image.NewRGBA(image.Rect(0, 0, 0, 0))
	if err := g.Flip(zero); err != nil {
		t.Fatalf("zero frame Flip: %v", err)
	}
}
