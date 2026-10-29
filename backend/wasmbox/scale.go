// Copyright (c) 2026 the go-doom/engine authors.
// SPDX-License-Identifier: GPL-2.0-only

package wasmbox

import "encoding/binary"

// Scaler resizes RGBA frames by nearest neighbour with tables computed once.
//
// Doom draws 320x200 but was shown on 4:3 displays, so its pixels are taller
// than wide; the client presents 640x480 (each column twice, each row 2.4
// times on average) so the picture has the proportions players know, and
// the compositor scale-fits that to whatever size the window is dragged to.
type Scaler struct {
	sw, sh, dw, dh int
	// srcRow[y] is the source row of destination row y.
	srcRow []int
	// srcCol[x] is the byte offset in a source row of destination column x.
	srcCol []int
}

// NewScaler returns a scaler from sw×sh to dw×dh. All sizes must be positive.
func NewScaler(sw, sh, dw, dh int) *Scaler {
	s := &Scaler{sw: sw, sh: sh, dw: dw, dh: dh, srcRow: make([]int, dh), srcCol: make([]int, dw)}
	for y := range s.srcRow {
		s.srcRow[y] = y * sh / dh
	}
	for x := range s.srcCol {
		s.srcCol[x] = (x * sw / dw) * 4
	}
	return s
}

// Scale writes src (sw*sh*4 bytes) scaled into dst (dw*dh*4 bytes). A
// destination row that repeats the previous source row is copied whole.
func (s *Scaler) Scale(dst, src []byte) {
	srcStride, dstStride := s.sw*4, s.dw*4
	_ = src[s.sh*srcStride-1]
	_ = dst[s.dh*dstStride-1]
	prev := -1
	for y, sy := range s.srcRow {
		d := dst[y*dstStride : (y+1)*dstStride]
		if sy == prev {
			copy(d, dst[(y-1)*dstStride:y*dstStride])
			continue
		}
		prev = sy
		row := src[sy*srcStride : (sy+1)*srcStride]
		for x, off := range s.srcCol {
			binary.LittleEndian.PutUint32(d[x*4:], binary.LittleEndian.Uint32(row[off:]))
		}
	}
}
