// Copyright (c) 2026 cloud-boot contributors
// SPDX-License-Identifier: GPL-2.0-only
//
// This file is part of go-doom/engine, a fork of github.com/AndreRenaud/gore
// (Pure-Go minimal Doom implementation, GPL-2.0). The TamaGo frontend adapter
// itself is NEW code authored for cloud-boot and is released under the same
// license to preserve the engine's GPL boundary; cloud-boot's other components
// remain BSD-3-Clause.

package tamago

import (
	"image"
)

// virtioFramebuffer is the minimal subset of github.com/go-virtio/gpu's
// *Framebuffer the adapter needs. Defining it as a local interface keeps
// this file decoupled from a compile-time import of go-virtio/gpu — the
// concrete *gpu.Framebuffer satisfies it via duck typing, so cloud-boot's
// probe wires the real driver into [NewGPUAdapter] without godoom growing
// a virtio/gpu module dependency.
type virtioFramebuffer interface {
	// Flush pushes the current Pix bytes to the host scanout
	// (TRANSFER_TO_HOST_2D + RESOURCE_FLUSH in virtio-gpu terms).
	Flush() error
}

// GPUAdapter is the GPU implementation that wraps a virtio-gpu
// Framebuffer. The Framebuffer is acquired by the caller (typically the
// cloud-boot/tamago-uefi probe via go-virtio/gpu.OpenVirtioGPU +
// SetupFramebuffer) and the adapter owns nothing beyond it; lifetime
// management stays with the caller, who must keep the underlying
// VirtioGPU device alive for the duration of the DOOM run.
//
// The adapter performs the RGBA → BGRA byte swap DOOM's frame format
// requires for virtio-gpu's VIRTIO_GPU_FORMAT_B8G8R8A8_UNORM resource,
// and the destination-rectangle copy when the framebuffer is wider /
// taller than the 320×200 DOOM canvas (cloud-boot/tamago-uefi typically
// asks the device for a native 1024×768 scanout; the adapter centers
// the DOOM image inside it without a scale to keep the hot path cheap).
type GPUAdapter struct {
	fb     virtioFramebuffer
	pix    []byte // BGRA backing store, aliased onto fb.Pix
	width  int    // framebuffer width in pixels
	height int    // framebuffer height in pixels
	// xOff / yOff are the top-left of the DOOM canvas inside the
	// framebuffer. Zero when the framebuffer matches the DOOM canvas
	// exactly; positive when the framebuffer is bigger (we letterbox).
	xOff int
	yOff int
}

// NewGPUAdapter wraps the supplied virtio-gpu Framebuffer. `pix` MUST be
// the same byte slice as `fb.Pix` so the adapter can blit DOOM frames
// directly into the device-backed memory without a second copy; width and
// height are the framebuffer's dimensions in pixels.
//
// The caller (the cloud-boot probe) typically does:
//
//	g, _ := gpu.OpenVirtioGPU(transport)
//	displays, _ := g.DisplayInfo()
//	fb, _ := g.SetupFramebuffer(displays[0].ScanoutID, displays[0].Width, displays[0].Height)
//	adapter := tamago.NewGPUAdapter(fb, fb.Pix, int(fb.Width), int(fb.Height))
func NewGPUAdapter(fb virtioFramebuffer, pix []byte, width, height int) *GPUAdapter {
	return &GPUAdapter{
		fb:     fb,
		pix:    pix,
		width:  width,
		height: height,
	}
}

// Flip blits the DOOM RGBA frame into the BGRA framebuffer backing store
// and asks the device to push it to the scanout.
//
// Centers the DOOM image in the framebuffer on the first call (cached in
// xOff / yOff), then for every subsequent call performs only the
// per-pixel RGBA → BGRA copy in the live region. The unchanged border
// remains as the zero-initialised pages the PageAllocator handed out —
// the bare-metal DOOM demo doesn't need a per-frame border repaint.
func (g *GPUAdapter) Flip(frame *image.RGBA) error {
	if g == nil || g.fb == nil || frame == nil || len(g.pix) == 0 {
		return nil
	}
	b := frame.Bounds()
	fw := b.Dx()
	fh := b.Dy()
	if fw <= 0 || fh <= 0 || g.width <= 0 || g.height <= 0 {
		return nil
	}
	// R-doom1i (2026-06-14): integer nearest-neighbor up-scale the DOOM
	// canvas (typically 320×200) to fill as much of the framebuffer
	// (typically a host scanout size like 1280×800) as possible while
	// preserving aspect ratio. Previously we centered DOOM 1:1 in the
	// framebuffer with a large black border; the operator's reaction
	// "l'image ne fit pas la taille de la fenêtre QEMU" surfaced the
	// gap. With a 4× scale on a 1280×800 scanout, DOOM 320×200 fills
	// exactly the entire framebuffer (320×4=1280, 200×4=800).
	//
	// When the source frame is LARGER than the framebuffer (an edge
	// case the original test corpus exercises), we fall back to the
	// historical clip-and-center 1:1 behaviour: scale = 1, fw/fh
	// clamped to g.width/g.height. Otherwise scale is the maximum
	// integer factor that still fits both dimensions.
	scale := 1
	if fw <= g.width && fh <= g.height {
		scale = g.width / fw
		if sh := g.height / fh; sh < scale {
			scale = sh
		}
		if scale < 1 {
			scale = 1
		}
	} else {
		// Source larger than framebuffer — clip to FB dimensions, no scale.
		if fw > g.width {
			fw = g.width
		}
		if fh > g.height {
			fh = g.height
		}
	}
	dw := fw * scale
	dh := fh * scale
	g.xOff = (g.width - dw) / 2
	g.yOff = (g.height - dh) / 2
	srcStride := frame.Stride
	dstStride := g.width * 4
	for sy := 0; sy < fh; sy++ {
		srcRow := frame.Pix[sy*srcStride : sy*srcStride+fw*4]
		// Build one scaled destination row in a buffer, then copy `scale`
		// times into the framebuffer for the vertical replication.
		baseY := (g.yOff + sy*scale) * dstStride
		// First the inner pixel loop, writing scale-replicated horizontally.
		for sx := 0; sx < fw; sx++ {
			r := srcRow[sx*4+0]
			gn := srcRow[sx*4+1]
			bl := srcRow[sx*4+2]
			a := srcRow[sx*4+3]
			// virtio-gpu's VIRTIO_GPU_FORMAT_B8G8R8A8_UNORM = { B, G, R, A }.
			startCol := g.xOff + sx*scale
			for dx := 0; dx < scale; dx++ {
				off := baseY + (startCol+dx)*4
				g.pix[off+0] = bl
				g.pix[off+1] = gn
				g.pix[off+2] = r
				g.pix[off+3] = a
			}
		}
		// Vertical replication: copy the just-written row `scale-1` more
		// times beneath it. The first row was written by the inner loop.
		rowStart := baseY + g.xOff*4
		rowEnd := rowStart + dw*4
		src := g.pix[rowStart:rowEnd]
		for dy := 1; dy < scale; dy++ {
			dstStart := (g.yOff+sy*scale+dy)*dstStride + g.xOff*4
			copy(g.pix[dstStart:dstStart+dw*4], src)
		}
	}
	return g.fb.Flush()
}

// Compile-time interface conformance assertion — *GPUAdapter must
// satisfy the GPU contract the Frontend expects.
var _ GPU = (*GPUAdapter)(nil)
