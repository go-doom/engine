// Copyright (c) 2026 the go-doom/engine authors.
// SPDX-License-Identifier: GPL-2.0-only

// Package wasmbox is a gore.DoomFrontend for the wasmbox desktop
// (github.com/wasmdesk/wasmbox): the engine runs in a Web Worker, presents
// each frame by copying it into a SharedArrayBuffer and posting a `commit`
// to the compositor, and receives the keys of its focused window as `input`
// messages. The wire contract is wasmbox's docs/protocol.md; the shape of
// this package follows go-quake1/engine's backend/wasmbox.
//
// The pure parts (key map, DMX sound decoding, frame scaling, the event
// queue) build and test on every platform; the browser glue is in the
// js && wasm files.
package wasmbox

import (
	"sync"

	gore "github.com/go-doom/engine"
)

// MaxQueuedEvents bounds the input queue: a worker that stalls (a long
// level load) must not let a held key or a stream of mouse messages grow it
// without limit. The oldest events are dropped first.
const MaxQueuedEvents = 256

// Queue is a bounded FIFO of engine events, filled by the message listener
// and drained by the engine's GetEvent.
type Queue struct {
	mu     sync.Mutex
	events []gore.DoomEvent
}

// Push appends ev, dropping the oldest event when the queue is full.
func (q *Queue) Push(ev gore.DoomEvent) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.events) >= MaxQueuedEvents {
		copy(q.events, q.events[1:])
		q.events = q.events[:len(q.events)-1]
	}
	q.events = append(q.events, ev)
}

// Pop removes the oldest event into *ev and reports whether there was one.
func (q *Queue) Pop(ev *gore.DoomEvent) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.events) == 0 {
		return false
	}
	*ev = q.events[0]
	copy(q.events, q.events[1:])
	q.events = q.events[:len(q.events)-1]
	return true
}

// Len returns the number of queued events.
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.events)
}

// KeyEvent translates a wasmbox input event (kind "keydown" / "keyup" and a
// DOM code) into an engine event. ok is false for other kinds and for keys
// Doom does not use.
func KeyEvent(kind, code string) (ev gore.DoomEvent, ok bool) {
	var t gore.Evtype_t
	switch kind {
	case "keydown":
		t = gore.Ev_keydown
	case "keyup":
		t = gore.Ev_keyup
	default:
		return ev, false
	}
	k, ok := DoomKey(code)
	if !ok {
		return ev, false
	}
	return gore.DoomEvent{Type: t, Key: k}, true
}

// ButtonEvent translates a wasmbox mouse button event into a key event: the
// left button fires, the right button opens doors (use). Mouse motion is not
// forwarded, so moving the pointer to the title bar never turns the player;
// and buttons cannot travel as gore Ev_mouse events because i_GetEvent drops
// every mouse event whose motion is under 5 units, button changes included.
func ButtonEvent(kind string, button int) (ev gore.DoomEvent, ok bool) {
	var t gore.Evtype_t
	switch kind {
	case "mousedown":
		t = gore.Ev_keydown
	case "mouseup":
		t = gore.Ev_keyup
	default:
		return ev, false
	}
	switch button {
	case 0:
		return gore.DoomEvent{Type: t, Key: keyFire}, true
	case 2:
		return gore.DoomEvent{Type: t, Key: keyUse}, true
	}
	return ev, false
}
