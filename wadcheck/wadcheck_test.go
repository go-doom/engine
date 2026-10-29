// Copyright (c) 2026 the go-doom/engine authors.
// SPDX-License-Identifier: GPL-2.0-only

package wadcheck

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

type lumpSpec struct {
	name string
	data []byte
}

// buildWAD assembles a well-formed WAD: header, lump bodies, directory.
func buildWAD(ident string, lumps []lumpSpec) []byte {
	var body bytes.Buffer
	type ent struct{ pos, size int32 }
	ents := make([]ent, len(lumps))
	for i, l := range lumps {
		ents[i] = ent{int32(HeaderSize + body.Len()), int32(len(l.data))}
		body.Write(l.data)
	}
	out := make([]byte, HeaderSize)
	copy(out, ident)
	binary.LittleEndian.PutUint32(out[4:], uint32(len(lumps)))
	binary.LittleEndian.PutUint32(out[8:], uint32(HeaderSize+body.Len()))
	out = append(out, body.Bytes()...)
	for i, l := range lumps {
		var e [DirEntrySize]byte
		binary.LittleEndian.PutUint32(e[0:], uint32(ents[i].pos))
		binary.LittleEndian.PutUint32(e[4:], uint32(ents[i].size))
		copy(e[8:], l.name)
		out = append(out, e[:]...)
	}
	return out
}

func validate(b []byte) ([]Lump, error) {
	return Validate(bytes.NewReader(b), int64(len(b)), DefaultLimits)
}

func TestValidateGood(t *testing.T) {
	w := buildWAD("IWAD", []lumpSpec{
		{"PLAYPAL", bytes.Repeat([]byte{7}, 768)},
		{"E1M1", nil}, // zero-size marker lumps are legal
		{"THINGS12", []byte{1, 2, 3}},
	})
	lumps, err := validate(w)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(lumps) != 3 || lumps[0].Name != "PLAYPAL" || lumps[0].Size != 768 || lumps[1].Size != 0 || lumps[2].Name != "THINGS12" {
		t.Fatalf("unexpected directory %+v", lumps)
	}
	if lumps[0].Pos != HeaderSize {
		t.Fatalf("PLAYPAL at %d, want %d", lumps[0].Pos, HeaderSize)
	}
}

func TestValidatePWADAndEmpty(t *testing.T) {
	if _, err := validate(buildWAD("PWAD", nil)); err != nil {
		t.Fatalf("empty PWAD: %v", err)
	}
}

// corrupt returns a copy of a good two-lump WAD with f applied.
func corrupt(f func(b []byte)) []byte {
	b := buildWAD("IWAD", []lumpSpec{{"A", []byte{1, 2, 3, 4}}, {"B", []byte{5, 6}}})
	f(b)
	return b
}

func TestValidateRejects(t *testing.T) {
	good := buildWAD("IWAD", []lumpSpec{{"A", []byte{1, 2, 3, 4}}, {"B", []byte{5, 6}}})
	dir := int(binary.LittleEndian.Uint32(good[8:]))
	put := func(b []byte, off int, v int32) { binary.LittleEndian.PutUint32(b[off:], uint32(v)) }
	cases := []struct {
		name string
		wad  []byte
		want error
	}{
		{"short", []byte("IWAD"), ErrTooSmall},
		{"ident", corrupt(func(b []byte) { copy(b, "JUNK") }), ErrIdent},
		{"negative count", corrupt(func(b []byte) { put(b, 4, -1) }), ErrLumpCount},
		{"huge count", corrupt(func(b []byte) { put(b, 4, 1<<30) }), ErrLumpCount},
		{"count over engine limit", corrupt(func(b []byte) { put(b, 4, EngineMaxLumps+1) }), ErrLumpCount},
		{"negative table", corrupt(func(b []byte) { put(b, 8, -16) }), ErrDirectory},
		{"table past end", corrupt(func(b []byte) { put(b, 8, int32(len(b))) }), ErrDirectory},
		{"count past end", corrupt(func(b []byte) { put(b, 4, 3) }), ErrDirectory},
		{"negative lump pos", corrupt(func(b []byte) { put(b, dir, -1) }), ErrLump},
		{"negative lump size", corrupt(func(b []byte) { put(b, dir+4, -1) }), ErrLump},
		{"lump past end", corrupt(func(b []byte) { put(b, dir+DirEntrySize+4, int32(len(b))) }), ErrLump},
		{"lump overflow", corrupt(func(b []byte) { put(b, dir, 1<<31-1); put(b, dir+4, 1<<31-1) }), ErrLump},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := validate(c.wad)
			if !errors.Is(err, c.want) {
				t.Fatalf("got %v, want %v", err, c.want)
			}
		})
	}
}

func TestValidateFileSizeLimit(t *testing.T) {
	w := buildWAD("IWAD", []lumpSpec{{"A", make([]byte, 100)}})
	_, err := Validate(bytes.NewReader(w), int64(len(w)), Limits{MaxLumps: 10, MaxFileSize: 64})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("got %v, want ErrTooLarge", err)
	}
}

func TestLumpNameFullWidth(t *testing.T) {
	if got := lumpName([]byte("ABCDEFGH")); got != "ABCDEFGH" {
		t.Fatalf("got %q", got)
	}
}

// FuzzValidate: whatever the bytes, Validate must not panic, and when it
// accepts a file every lump it returns must lie inside it.
func FuzzValidate(f *testing.F) {
	f.Add(buildWAD("IWAD", []lumpSpec{{"PLAYPAL", []byte{1, 2, 3}}, {"E1M1", nil}}))
	f.Add(buildWAD("PWAD", nil))
	f.Add([]byte("IWAD\xff\xff\xff\xff\x00\x00\x00\x00"))
	f.Fuzz(func(t *testing.T, b []byte) {
		lumps, err := Validate(bytes.NewReader(b), int64(len(b)), Limits{MaxLumps: 64, MaxFileSize: 1 << 20})
		if err != nil {
			return
		}
		for i, l := range lumps {
			if l.Pos < 0 || l.Size < 0 || l.Pos+l.Size > int64(len(b)) {
				t.Fatalf("accepted lump %d out of bounds: %+v in %d bytes", i, l, len(b))
			}
		}
	})
}
