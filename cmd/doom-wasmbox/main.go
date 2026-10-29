// Copyright (c) 2026 the go-doom/engine authors.
// SPDX-License-Identifier: GPL-2.0-only

//go:build js && wasm

// doom-wasmbox runs the go-doom engine as a wasmbox desktop client
// (github.com/wasmdesk/wasmbox): it opens a 640x480, 4:3-locked window over
// the wasmbox protocol, streams the Freedoom IWAD from the same-origin /v2
// OCI mirror the site serves (sha256-pinned, directory-validated), and runs
// the engine's software renderer into the window.
//
// Every line it prints starts with "DOOM: " so the browser tests can read
// them from the worker console: "DOOM: FAIL ..." on any fatal error, a
// "DOOM: state=..." line whenever the game state changes, and an "fps=" line
// every few seconds.
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"syscall/js"
	"time"

	gore "github.com/go-doom/engine"
	"github.com/go-doom/engine/backend/wasmbox"
	"github.com/go-doom/engine/embedwad"
	"github.com/go-doom/engine/ociblob"
	"github.com/go-doom/engine/wadcheck"
)

func logf(format string, args ...any) {
	fmt.Printf("DOOM: "+format+"\n", args...)
}

func main() {
	if err := run(); err != nil {
		logf("FAIL %v", err)
		// Keep the instance alive so the failure screen stays up and the
		// console line is flushed.
		select {}
	}
	logf("exited cleanly")
}

func run() error {
	fe, err := wasmbox.NewClient(title, surfaceW, surfaceH, lockAspect)
	if err != nil {
		return err
	}
	w, h := fe.Size()
	logf("surface %dx%d window=%d audio=%s", w, h, fe.WindowID(), fe.AudioState())

	data, err := fetchWAD(fe, w, h)
	if err != nil {
		screen := make([]byte, 4*w*h)
		paintFail(screen, w, h)
		fe.Present(screen)
		return err
	}
	lumps, err := wadcheck.Validate(bytes.NewReader(data), int64(len(data)), wadcheck.DefaultLimits)
	if err != nil {
		return err
	}
	logf("wad %s: sha256 verified, %d bytes, %d lumps", WADName, len(data), len(lumps))

	gore.SetVirtualFileSystem(embedwad.New(WADName, data))
	args := []string{"-iwad", WADName}
	if fe.AudioState() == "unavailable" {
		args = append(args, "-nosound", "-nomusic")
	}
	fe.OnFrame = newReporter().frame
	logf("engine start %v", args)
	gore.Run(fe, args)
	return nil
}

// ociBase returns the site root published by clients/doom/worker.js, after
// checking it is on this worker's own origin: the client fetches nothing
// from anywhere else.
func ociBase() (string, error) {
	g := js.Global()
	v := g.Get("__doomOCIBase")
	if v.Type() != js.TypeString || v.String() == "" {
		return "", errors.New("self.__doomOCIBase is unset (worker.js publishes it)")
	}
	base := strings.TrimRight(v.String(), "/")
	origin := g.Get("location").Get("origin")
	if origin.Type() != js.TypeString {
		return "", errors.New("self.location.origin is unavailable")
	}
	if o := origin.String(); base != o && !strings.HasPrefix(base, o+"/") {
		return "", fmt.Errorf("asset base %q is not on this origin %q", base, o)
	}
	return base, nil
}

// fetchWAD pulls the WAD from the mirror while painting a progress bar.
func fetchWAD(fe *wasmbox.Frontend, w, h int) ([]byte, error) {
	repo, tag, err := parseReference(OCIReference)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(OCIReference, "/") {
		return nil, fmt.Errorf("reference %q is not same-origin (must start with '/')", OCIReference)
	}
	base, err := ociBase()
	if err != nil {
		return nil, err
	}
	logf("fetching %s from %s/v2/%s:%s", WADName, base, repo, tag)

	var got, total atomic.Int64
	done := make(chan struct{})
	painted := make(chan struct{})
	go func() {
		defer close(painted)
		screen := make([]byte, 4*w*h)
		t := time.NewTicker(50 * time.Millisecond)
		defer t.Stop()
		for {
			paintProgress(screen, w, h, got.Load(), total.Load())
			fe.Present(screen)
			select {
			case <-done:
				return
			case <-t.C:
			}
		}
	}()
	defer func() { close(done); <-painted }()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	c := &ociblob.Client{Base: base}
	start := time.Now()
	data, err := c.Fetch(ctx, ociblob.Request{
		Repo: repo, Tag: tag, File: WADName,
		Want:     "sha256:" + WADSHA256,
		MaxBytes: maxWADBytes,
		Progress: func(n, t int64) { got.Store(n); total.Store(t) },
	})
	if err != nil {
		return nil, err
	}
	logf("fetched %s in %.2fs", WADName, time.Since(start).Seconds())
	return data, nil
}

// reporter logs the game state when it changes (position at most twice a
// second) and the frame rate every five seconds.
type reporter struct {
	lastKey, lastLine string
	lastPos           time.Time
	windowStart       time.Time
	windowFrames      int
}

func newReporter() *reporter {
	return &reporter{windowStart: time.Now()}
}

func (r *reporter) frame(n int) {
	now := time.Now()
	if n == 1 {
		logf("first frame")
	}
	s := gore.Observe()
	line := stateLine(s)
	key := line[:strings.Index(line, " health=")]
	switch {
	case key != r.lastKey:
		logf("%s", line)
		r.lastKey, r.lastLine, r.lastPos = key, line, now
	case line != r.lastLine && now.Sub(r.lastPos) >= 500*time.Millisecond:
		logf("%s", line)
		r.lastLine, r.lastPos = line, now
	}
	r.windowFrames++
	if el := now.Sub(r.windowStart); el >= 5*time.Second {
		fps := float64(r.windowFrames) / el.Seconds()
		logf("fps=%.1f frame_ms=%.2f frames=%d", fps, 1000/fps, n)
		r.windowStart, r.windowFrames = now, 0
	}
}
