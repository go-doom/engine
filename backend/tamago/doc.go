// SPDX-License-Identifier: GPL-2.0-only

// Package tamago provides a [gore.DoomFrontend] implementation that drives
// DOOM output and input through cloud-boot's bare-metal virtio device tree.
//
// This package is a SCAFFOLD. None of the device wiring is implemented yet:
// the constructors return a frontend whose DrawFrame/PlaySound/GetEvent
// methods are no-ops, so the surrounding cloud-boot stack can compile and
// link against it before the real go-virtio/gpu, go-virtio/sound and
// go-virtio/input drivers land in their respective sibling repositories.
//
// The intended wiring once those repos ship is:
//
//   - DrawFrame(img *image.RGBA)   --> virtio-gpu RESOURCE_FLUSH of the
//     framebuffer scanout for the DOOM 320x200x32bpp surface (rescaled or
//     letter-boxed by the host scanout).
//   - PlaySound(name, ch, vol, sep)--> virtio-sound PCM_XFER stream chunk
//     emit; CacheSound copies the lump body into a host-pinned ring buffer
//     keyed by lump name.
//   - GetEvent(*DoomEvent)         --> virtio-input drain of the keyboard
//     event queue, with HID usage-id --> DOOM scancode translation handled
//     in this package.
//   - SetTitle(string)             --> currently dropped; could later be
//     emitted over virtio-console for the host operator to see.
//
// The frontend obeys TamaGo's no-CGO, no-syscall, no-signal-handling
// contract: it relies only on the standard library plus the published
// go-virtio device interfaces (which themselves are pure Go).
package tamago
