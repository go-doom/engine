// Copyright (c) 2026 the go-doom/engine authors.
// SPDX-License-Identifier: GPL-2.0-only

//go:build js && wasm

package wasmbox

import (
	"errors"
	"fmt"
	"image"
	"sync/atomic"
	"syscall/js"
	"time"

	gore "github.com/go-doom/engine"
)

// Errors returned by NewClient.
var (
	ErrNoSAB     = errors.New("wasmbox: SharedArrayBuffer unavailable -- the page needs COOP/COEP")
	ErrNoWelcome = errors.New("wasmbox: the compositor granted an empty surface")
)

// yieldEvery bounds how long the engine may hold the wasm thread. The engine
// sleeps (time.Sleep, which returns to the JS event loop) whenever it is
// ahead of its 35 Hz tic clock; when it is behind it renders back to back
// and the worker's message listener would never run, so DrawFrame yields
// for 1 ms if this much time has passed since the last yield.
const yieldEvery = 50 * time.Millisecond

// Frontend implements gore.DoomFrontend over the wasmbox protocol.
type Frontend struct {
	post    js.Value // self.postMessage
	pixels  js.Value // Uint8ClampedArray over the surface SAB
	ctl     js.Value // Int32Array over the seqlock control SAB
	atomics js.Value
	commit  js.Value // reusable {type:"commit", window_id, damage}

	w, h     int
	windowID int
	scaler   *Scaler
	frame    []byte

	queue  Queue
	closed atomic.Bool
	audio  audioSink

	lastYield time.Time
	frames    int

	// OnFrame, when set, is called after every presented frame with the
	// frame count. The client uses it to log state changes.
	OnFrame func(frames int)
}

// NewClient performs the wasmbox handshake from the worker scope: allocate
// the w×h RGBA surface and a seqlock control word as SharedArrayBuffers,
// install the message listener, post hello (with an aspect lock, so the
// resize grip keeps 4:3), and wait for welcome.
func NewClient(title string, w, h int, lockAspect float64) (*Frontend, error) {
	self := js.Global()
	sabCtor := self.Get("SharedArrayBuffer")
	if !sabCtor.Truthy() {
		return nil, ErrNoSAB
	}
	sab := sabCtor.New(4 * w * h)
	ctlSAB := sabCtor.New(4)
	f := &Frontend{
		post:    self.Get("postMessage"),
		pixels:  self.Get("Uint8ClampedArray").New(sab),
		ctl:     self.Get("Int32Array").New(ctlSAB),
		atomics: self.Get("Atomics"),
	}

	welcome := make(chan [3]int, 1)
	listener := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) == 0 {
			return nil
		}
		data := args[0].Get("data")
		if data.Type() != js.TypeObject {
			return nil
		}
		switch jsString(data.Get("type")) {
		case "welcome":
			select {
			case welcome <- [3]int{jsInt(data.Get("window_id")), jsInt(data.Get("granted_w")), jsInt(data.Get("granted_h"))}:
			default:
			}
		case "input":
			f.handleInput(data.Get("event"))
		case "closed":
			f.closed.Store(true)
		}
		return nil
	})
	// Never released: the listener lives as long as the worker.
	self.Call("addEventListener", "message", listener)

	hello := self.Get("Object").New()
	hello.Set("type", "hello")
	hello.Set("title", title)
	hello.Set("w", w)
	hello.Set("h", h)
	hello.Set("sab", sab)
	hello.Set("stride", 4*w)
	hello.Set("ctl", ctlSAB)
	if lockAspect > 0 {
		hello.Set("lock_aspect", lockAspect)
	}
	// Ask the compositor not to match its own chords while this window has
	// the keyboard (Wayland's keyboard-shortcuts-inhibit analog): Doom fires
	// with Ctrl and strafes with Alt, and F3 loads a game. A compositor that
	// does not know the field ignores it.
	hello.Set("inhibit_shortcuts", true)
	f.post.Invoke(hello)

	got := <-welcome
	f.windowID = got[0]
	// The surface is the SAB we allocated; a smaller grant only means the
	// compositor will scale-fit it, so keep drawing at w×h.
	if got[1] <= 0 || got[2] <= 0 {
		return nil, fmt.Errorf("%w (%dx%d)", ErrNoWelcome, got[1], got[2])
	}
	f.w, f.h = w, h
	f.frame = make([]byte, 4*w*h)

	damage := self.Get("Object").New()
	damage.Set("x", 0)
	damage.Set("y", 0)
	damage.Set("w", w)
	damage.Set("h", h)
	f.commit = self.Get("Object").New()
	f.commit.Set("type", "commit")
	f.commit.Set("window_id", f.windowID)
	f.commit.Set("damage", damage)

	// Under the wasmbox compositor the sound goes through the relay: a worker
	// has no AudioContext, so PCM is written into a SharedArrayBuffer ring the
	// page plays from an AudioWorklet. A worker-scope AudioContext remains the
	// fallback. (Assign only non-nil values: a nil *webAudio in the interface
	// would read as a live sink.)
	noWrite := self.Get("__wasmboxNoAudioWrite").Truthy() // planted-negative hook for the e2e
	if ra := newRingAudio(f.windowID, noWrite); ra != nil {
		f.audio = ra
	} else if wa := newWebAudio(); wa != nil {
		f.audio = wa
	}
	f.lastYield = time.Now()
	return f, nil
}

// WindowID returns the compositor-assigned window id.
func (f *Frontend) WindowID() int { return f.windowID }

// Size returns the surface size.
func (f *Frontend) Size() (int, int) { return f.w, f.h }

// AudioState returns the AudioContext state, or "unavailable" when the
// worker has no AudioContext.
func (f *Frontend) AudioState() string {
	if f.audio == nil {
		return "unavailable"
	}
	return f.audio.state()
}

// Closed reports whether the compositor closed the window.
func (f *Frontend) Closed() bool { return f.closed.Load() }

// Present copies one w×h RGBA frame to the surface under the seqlock and
// commits it. The client uses it for its loading screen.
func (f *Frontend) Present(rgba []byte) {
	f.atomics.Call("add", f.ctl, 0, 1) // odd: frame in progress
	js.CopyBytesToJS(f.pixels, rgba)
	f.atomics.Call("add", f.ctl, 0, 1) // even: complete
	f.post.Invoke(f.commit)
}

// DrawFrame implements gore.DoomFrontend.
func (f *Frontend) DrawFrame(img *image.RGBA) {
	b := img.Bounds()
	if f.scaler == nil || f.scaler.sw != b.Dx() || f.scaler.sh != b.Dy() {
		f.scaler = NewScaler(b.Dx(), b.Dy(), f.w, f.h)
	}
	f.scaler.Scale(f.frame, img.Pix)
	f.Present(f.frame)
	f.frames++
	if f.audio != nil {
		f.audio.pumpMusic()
	}
	if f.OnFrame != nil {
		f.OnFrame(f.frames)
	}
	if f.closed.Load() {
		gore.Stop()
	}
	if time.Since(f.lastYield) > yieldEvery {
		time.Sleep(time.Millisecond)
		f.lastYield = time.Now()
	}
}

// SetTitle implements gore.DoomFrontend. The window keeps the title it was
// opened with: wasmbox keys its saved window geometry on the title.
func (f *Frontend) SetTitle(string) {}

// GetEvent implements gore.DoomFrontend.
func (f *Frontend) GetEvent(ev *gore.DoomEvent) bool {
	return f.queue.Pop(ev)
}

// CacheSound implements gore.DoomFrontend.
func (f *Frontend) CacheSound(name string, data []byte) {
	if f.audio != nil {
		f.audio.cache(name, data)
	}
}

// PlaySound implements gore.DoomFrontend.
func (f *Frontend) PlaySound(name string, channel, vol, sep int) {
	if f.audio != nil {
		f.audio.play(name, channel, vol, sep)
	}
}

func (f *Frontend) handleInput(ev js.Value) {
	if ev.Type() != js.TypeObject {
		return
	}
	kind := jsString(ev.Get("kind"))
	if e, ok := KeyEvent(kind, jsString(ev.Get("code"))); ok {
		f.queue.Push(e)
		return
	}
	if e, ok := ButtonEvent(kind, jsInt(ev.Get("button"))); ok {
		f.queue.Push(e)
	}
}

func jsString(v js.Value) string {
	if v.Type() != js.TypeString {
		return ""
	}
	return v.String()
}

func jsInt(v js.Value) int {
	if v.Type() != js.TypeNumber {
		return 0
	}
	return v.Int()
}
