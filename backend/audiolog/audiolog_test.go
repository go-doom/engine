// Copyright (c) 2026 cloud-boot contributors
// SPDX-License-Identifier: GPL-2.0-only

package audiolog

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
)

func TestRecorder_CacheAndPlay_AreRecordedWithTic(t *testing.T) {
	var tic int32 = 42
	r := New(func() int32 { return tic })
	r.Cache("dspistol", []byte("PCM"))
	tic = 70
	r.Play("dspistol", 1, 100, 64)
	got := r.Entries()
	if len(got) != 2 {
		t.Fatalf("got %d entries want 2: %+v", len(got), got)
	}
	wantHash := sha256.Sum256([]byte("PCM"))
	if got[0].Op != "cache" || got[0].Tic != 42 || got[0].SHA256 != hex.EncodeToString(wantHash[:]) {
		t.Fatalf("bad cache entry: %+v", got[0])
	}
	if got[1].Op != "play" || got[1].Tic != 70 || got[1].Channel != 1 || got[1].Vol != 100 || got[1].Sep != 64 {
		t.Fatalf("bad play entry: %+v", got[1])
	}
}

func TestRecorder_NilTicFn_YieldsZero(t *testing.T) {
	r := New(nil)
	r.Cache("dssgcock", []byte{1, 2, 3})
	r.Play("dssgcock", 0, 100, 64)
	for _, e := range r.Entries() {
		if e.Tic != 0 {
			t.Fatalf("nil ticFn entry should have Tic=0, got %+v", e)
		}
	}
}

func TestRecorder_Entries_DeterministicallySorted(t *testing.T) {
	r := New(func() int32 { return 0 })
	// Intentional reverse insertion order to confirm Entries() sorts.
	r.Play("z", 2, 100, 64)
	r.Play("a", 1, 100, 64)
	r.Cache("a", []byte("x"))
	r.Cache("b", []byte("y"))
	got := r.Entries()
	wantOrder := []string{"cache:a", "cache:b", "play:a", "play:z"}
	for i, e := range got {
		key := e.Op + ":" + e.Name
		if key != wantOrder[i] {
			t.Fatalf("entry %d: got %q want %q (all=%+v)", i, key, wantOrder[i], got)
		}
	}
}

func TestRecorder_Entries_SortByChannelWhenNameTies(t *testing.T) {
	r := New(func() int32 { return 0 })
	r.Play("dspistol", 5, 100, 64)
	r.Play("dspistol", 1, 100, 64)
	got := r.Entries()
	if got[0].Channel != 1 || got[1].Channel != 5 {
		t.Fatalf("channels not sorted: %+v", got)
	}
}

func TestRecorder_WriteJSON_RoundTrips(t *testing.T) {
	r := New(func() int32 { return 35 })
	r.Cache("dspistol", []byte("PCM"))
	r.Play("dspistol", 0, 100, 64)

	var buf bytes.Buffer
	if err := r.WriteJSON(&buf); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	if buf.Bytes()[buf.Len()-1] != '\n' {
		t.Fatalf("expected trailing newline")
	}
	var round []Entry
	if err := json.Unmarshal(buf.Bytes(), &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(round) != 2 {
		t.Fatalf("round-trip lost entries: %+v", round)
	}
}

func TestRecorder_WriteJSON_PropagatesWriterError(t *testing.T) {
	r := New(nil)
	r.Cache("d", []byte("x"))
	err := r.WriteJSON(errWriter{})
	if err == nil {
		t.Fatalf("expected writer error")
	}
}

type errWriter struct{}

func (errWriter) Write([]byte) (int, error) { return 0, errors.New("forced") }

func TestRecorder_Entries_IsSnapshot(t *testing.T) {
	r := New(nil)
	r.Cache("a", nil)
	snap := r.Entries()
	r.Cache("b", nil)
	if len(snap) != 1 {
		t.Fatalf("snapshot leaked mutation: %+v", snap)
	}
}
