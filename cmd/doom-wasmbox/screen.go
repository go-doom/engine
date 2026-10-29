// Copyright (c) 2026 the go-doom/engine authors.
// SPDX-License-Identifier: GPL-2.0-only

package main

import (
	"fmt"

	gore "github.com/go-doom/engine"
)

// Surface size. Doom renders 320x200 for a 4:3 display; 640x480 keeps those
// proportions (see backend/wasmbox.Scaler) and the window is aspect-locked.
const (
	surfaceW   = 640
	surfaceH   = 480
	lockAspect = 4.0 / 3.0
	title      = "doom (wasm)"
)

// The WAD: Freedoom: Phase 1 0.13.0 (BSD-3-Clause, freedoom.github.io).
// freedoom-0.13.0.zip is listed in the release's signed CHECKSUM file as
// sha256 3f9b264f...9688f59; freedoom1.wad extracted from it hashes to
// wadSHA256. The client accepts no other bytes. Both can be overridden at
// link time (-X main.OCIReference=... -X main.WADSHA256=...).
var (
	// OCIReference is "<repo>:<tag>"; a leading '/' means the mirror is
	// served beside the page (resolved against the worker's site root).
	OCIReference = "/doom-assets:latest"
	// WADName is the file name inside the image, and the name the engine
	// is told to load (it recognises "freedoom1.wad" as Freedoom: Phase 1).
	WADName = "freedoom1.wad"
	// WADSHA256 pins the WAD's content.
	WADSHA256 = "7323bcc168c5a45ff10749b339960e98314740a734c30d4b9f3337001f9e703d"
)

// maxWADBytes bounds the download (the pinned WAD is 28,795,076 bytes).
const maxWADBytes = 64 << 20

// parseReference splits "/repo:tag" or "repo:tag" into its parts.
func parseReference(ref string) (repo, tag string, err error) {
	if len(ref) > 0 && ref[0] == '/' {
		ref = ref[1:]
	}
	for i := len(ref) - 1; i >= 0; i-- {
		if ref[i] == ':' {
			if i == 0 || i == len(ref)-1 {
				break
			}
			return ref[:i], ref[i+1:], nil
		}
		if ref[i] == '/' {
			break
		}
	}
	return "", "", fmt.Errorf("doom-wasmbox: reference %q is not <repo>:<tag>", ref)
}

// fill paints the whole frame one colour.
func fill(rgba []byte, r, g, b byte) {
	for i := 0; i+3 < len(rgba); i += 4 {
		rgba[i], rgba[i+1], rgba[i+2], rgba[i+3] = r, g, b, 255
	}
}

// rect paints a rectangle, clipped to the frame.
func rect(rgba []byte, w, h, x0, y0, x1, y1 int, r, g, b byte) {
	x0, y0 = max(x0, 0), max(y0, 0)
	x1, y1 = min(x1, w), min(y1, h)
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			i := (y*w + x) * 4
			rgba[i], rgba[i+1], rgba[i+2], rgba[i+3] = r, g, b, 255
		}
	}
}

// paintProgress draws the download screen: a dark frame with a bar filled
// got/total of its width. total <= 0 draws an empty bar.
func paintProgress(rgba []byte, w, h int, got, total int64) {
	fill(rgba, 24, 8, 8)
	bw, bh := w*3/4, max(h/20, 4)
	x0, y0 := (w-bw)/2, (h-bh)/2
	rect(rgba, w, h, x0-2, y0-2, x0+bw+2, y0+bh+2, 200, 200, 200)
	rect(rgba, w, h, x0, y0, x0+bw, y0+bh, 40, 16, 16)
	if total > 0 {
		done := int(int64(bw) * min(got, total) / total)
		rect(rgba, w, h, x0, y0, x0+done, y0+bh, 200, 32, 16)
	}
}

// paintFail draws the failure screen; the reason is in the worker console.
func paintFail(rgba []byte, w, h int) {
	fill(rgba, 96, 0, 0)
	rect(rgba, w, h, w/4, h/2-4, w*3/4, h/2+4, 255, 255, 255)
}

// stateLine renders a snapshot as the log line the browser tests read.
func stateLine(s gore.Snapshot) string {
	menu, demo := 0, 0
	if s.MenuActive {
		menu = 1
	}
	if s.DemoPlayback {
		demo = 1
	}
	return fmt.Sprintf("state=%s menu=%d demo=%d map=E%dM%d health=%d pos=%d,%d",
		s.State, menu, demo, s.Episode, s.Map, s.Health, s.X, s.Y)
}
