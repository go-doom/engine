// Copyright (c) 2026 cloud-boot contributors
// SPDX-License-Identifier: GPL-2.0-only
//
// Package audiolog records the deterministic sequence of CacheSound +
// PlaySound calls the gore engine emits while running. The recorded
// log is the GATE C-1 oracle of the DOOM provable-test protocol
// (R-doom1h): given a fixed WAD + seed + script, the engine's audio
// event stream is a pure function of game state, so two harvests must
// produce byte-identical audio_log.json files.
//
// This package is GPL-2.0 (matching the gore engine boundary) because
// it is consumed by harvest-reference, which links gore directly. It
// does not import gore itself; it is a passive recorder over the
// frontend's audio surface.

package audiolog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"sort"
	"sync"
)

// Entry is one row of audio_log.json — either a Cache or a Play call.
type Entry struct {
	Tic     int32  `json:"tic"`
	Op      string `json:"op"` // "cache" or "play"
	Name    string `json:"name"`
	Channel int    `json:"channel,omitempty"`
	Vol     int    `json:"vol,omitempty"`
	Sep     int    `json:"sep,omitempty"`
	// SHA256 is hex(sha256(data)) for "cache" entries, empty for "play".
	// Recording the lump hash (not the raw bytes) keeps the oracle small
	// while still detecting WAD-level audio drift.
	SHA256 string `json:"sha256,omitempty"`
}

// Recorder collects audio events and emits them as audio_log.json.
//
// The recorder is goroutine-safe — gore.Run drives DrawFrame, CacheSound,
// PlaySound from a single goroutine today, but the locking shields the
// (rare) case where a future frontend records concurrently with a JSON
// flush.
type Recorder struct {
	mu      sync.Mutex
	entries []Entry
	// ticFn returns the engine's CURRENT tic. Injected so the recorder
	// does not have to import gore; the harvester wires it to
	// godoom.CurrentTic.
	ticFn func() int32
}

// New builds a recorder. ticFn MUST return the current engine tic; if
// nil, every entry's Tic is 0 (useful for unit tests only).
func New(ticFn func() int32) *Recorder {
	return &Recorder{ticFn: ticFn}
}

// Cache records a CacheSound call. data is hashed; the raw bytes are
// not retained.
func (r *Recorder) Cache(name string, data []byte) {
	h := sha256.Sum256(data)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, Entry{
		Tic:    r.currentTic(),
		Op:     "cache",
		Name:   name,
		SHA256: hex.EncodeToString(h[:]),
	})
}

// Play records a PlaySound call.
func (r *Recorder) Play(name string, channel, vol, sep int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, Entry{
		Tic:     r.currentTic(),
		Op:      "play",
		Name:    name,
		Channel: channel,
		Vol:     vol,
		Sep:     sep,
	})
}

// Entries returns a snapshot copy of the recorded events, sorted by
// (tic, op, name, channel) so two runs with the same logical event
// stream serialise identically regardless of insertion order races.
func (r *Recorder) Entries() []Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Entry, len(r.entries))
	copy(out, r.entries)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Tic != b.Tic {
			return a.Tic < b.Tic
		}
		if a.Op != b.Op {
			return a.Op < b.Op
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Channel < b.Channel
	})
	return out
}

// WriteJSON serialises the log as indented JSON with a trailing
// newline — matches the manifest.json format used by the rest of the
// oracle so diffs read cleanly. []Entry only contains primitive fields,
// so json.MarshalIndent itself cannot fail; the only error surface is
// the writer.
func (r *Recorder) WriteJSON(w io.Writer) error {
	entries := r.Entries()
	blob, _ := json.MarshalIndent(entries, "", "  ")
	blob = append(blob, '\n')
	_, err := w.Write(blob)
	return err
}

// currentTic must be called with r.mu held.
func (r *Recorder) currentTic() int32 {
	if r.ticFn == nil {
		return 0
	}
	return r.ticFn()
}
