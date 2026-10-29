// Copyright (c) 2026 the go-doom/engine authors.
// SPDX-License-Identifier: GPL-2.0-only

// Package wadcheck validates the header and lump directory of a WAD file
// before the engine reads it.
//
// The engine's loader (w_AddFile in doom.go, transpiled from
// chocolate-doom's w_wad.c) trusts the file: a negative or huge lump count
// reaches make() and panics or allocates gigabytes, a directory past the end
// of the file or a lump whose offset+size runs off the end is only noticed
// when a later ReadAt fails and log.Fatalf ends the process, and more than
// 4095 lumps panics extendLumpInfo (lumpinfo is a fixed [4096] array). A WAD
// that reaches the engine from the network is untrusted input, so the host
// checks it here first and refuses it with an error instead.
//
// Layout (all integers little-endian), as read by w_AddFile:
//
//	header (12 bytes):   identification [4]byte "IWAD" | "PWAD"
//	                     numlumps       int32
//	                     infotableofs   int32
//	directory (numlumps × 16 bytes at infotableofs):
//	                     filepos int32, size int32, name [8]byte
//
// wadcheck bounds the directory only. What is inside a lump (map geometry,
// patches, sounds) is still parsed by the engine without bounds checks; the
// wasmbox client therefore also pins the WAD's sha256, so only the one
// audited Freedoom release ever reaches that code.
package wadcheck

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// HeaderSize is the size of the WAD header in bytes.
const HeaderSize = 12

// DirEntrySize is the size of one lump directory entry in bytes.
const DirEntrySize = 16

// EngineMaxLumps is the most lumps the engine can hold: lumpinfo is a
// [4096]lumpinfo_t array and extendLumpInfo panics when the new count
// reaches its length.
const EngineMaxLumps = 4095

// Limits bounds what Validate accepts.
type Limits struct {
	// MaxLumps is the largest accepted lump count.
	MaxLumps int
	// MaxFileSize is the largest accepted file size in bytes.
	MaxFileSize int64
}

// DefaultLimits fits the engine: at most EngineMaxLumps lumps and 256 MiB,
// ample for an IWAD (Freedoom 0.13.0's freedoom1.wad is 28,795,076 bytes).
var DefaultLimits = Limits{MaxLumps: EngineMaxLumps, MaxFileSize: 256 << 20}

// Lump is one validated directory entry.
type Lump struct {
	Name string
	Pos  int64
	Size int64
}

// Sentinel errors, for errors.Is.
var (
	ErrTooSmall  = errors.New("wadcheck: file smaller than the 12-byte header")
	ErrTooLarge  = errors.New("wadcheck: file larger than the limit")
	ErrIdent     = errors.New("wadcheck: identification is neither IWAD nor PWAD")
	ErrLumpCount = errors.New("wadcheck: lump count out of range")
	ErrDirectory = errors.New("wadcheck: directory outside the file")
	ErrLump      = errors.New("wadcheck: lump outside the file")
)

// Validate reads the header and directory of the WAD of the given size from
// r and checks every offset and size against the file. It returns the
// directory on success. It never allocates more than the directory itself,
// which MaxLumps bounds.
func Validate(r io.ReaderAt, size int64, lim Limits) ([]Lump, error) {
	if size < HeaderSize {
		return nil, ErrTooSmall
	}
	if lim.MaxFileSize > 0 && size > lim.MaxFileSize {
		return nil, fmt.Errorf("%w: %d > %d bytes", ErrTooLarge, size, lim.MaxFileSize)
	}
	var hdr [HeaderSize]byte
	if _, err := r.ReadAt(hdr[:], 0); err != nil {
		return nil, fmt.Errorf("wadcheck: read header: %w", err)
	}
	ident := string(hdr[0:4])
	if ident != "IWAD" && ident != "PWAD" {
		return nil, fmt.Errorf("%w: %q", ErrIdent, ident)
	}
	numLumps := int64(int32(binary.LittleEndian.Uint32(hdr[4:8])))
	tableOfs := int64(int32(binary.LittleEndian.Uint32(hdr[8:12])))
	if numLumps < 0 || numLumps > int64(lim.MaxLumps) {
		return nil, fmt.Errorf("%w: %d (limit %d)", ErrLumpCount, numLumps, lim.MaxLumps)
	}
	// Both terms are bounded (numLumps <= MaxLumps, tableOfs < 2^31), so the
	// int64 sum cannot overflow.
	if tableOfs < 0 || tableOfs+numLumps*DirEntrySize > size {
		return nil, fmt.Errorf("%w: %d entries at offset %d in a %d-byte file", ErrDirectory, numLumps, tableOfs, size)
	}
	dir := make([]byte, numLumps*DirEntrySize)
	if len(dir) > 0 {
		if _, err := r.ReadAt(dir, tableOfs); err != nil {
			return nil, fmt.Errorf("wadcheck: read directory: %w", err)
		}
	}
	lumps := make([]Lump, numLumps)
	for i := range lumps {
		e := dir[i*DirEntrySize : (i+1)*DirEntrySize]
		pos := int64(int32(binary.LittleEndian.Uint32(e[0:4])))
		n := int64(int32(binary.LittleEndian.Uint32(e[4:8])))
		name := lumpName(e[8:16])
		if pos < 0 || n < 0 || pos+n > size {
			return nil, fmt.Errorf("%w: lump %d %q at %d, %d bytes, in a %d-byte file", ErrLump, i, name, pos, n, size)
		}
		lumps[i] = Lump{Name: name, Pos: pos, Size: n}
	}
	return lumps, nil
}

// lumpName returns the NUL-terminated lump name of an 8-byte field.
func lumpName(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}
