// Copyright (c) 2026 the go-doom/engine authors.
// SPDX-License-Identifier: GPL-2.0-only

package main

import (
	"testing"

	gore "github.com/go-doom/engine"
	"github.com/go-doom/engine/ociblob"
)

func TestParseReference(t *testing.T) {
	for ref, want := range map[string][2]string{
		"/doom-assets:latest": {"doom-assets", "latest"},
		"doom-assets:0.13.0":  {"doom-assets", "0.13.0"},
		"org/doom:v1":         {"org/doom", "v1"},
	} {
		repo, tag, err := parseReference(ref)
		if err != nil || repo != want[0] || tag != want[1] {
			t.Errorf("%q: got %q %q %v", ref, repo, tag, err)
		}
	}
	for _, bad := range []string{"", "/", "doom", "doom:", ":latest", "host:5000/doom"} {
		if _, _, err := parseReference(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestDefaultsAreWellFormed(t *testing.T) {
	if !ociblob.ValidDigest("sha256:" + WADSHA256) {
		t.Fatalf("WADSHA256 %q is not a sha256 hex digest", WADSHA256)
	}
	if _, _, err := parseReference(OCIReference); err != nil {
		t.Fatal(err)
	}
}

func pixel(rgba []byte, w, x, y int) [3]byte {
	i := (y*w + x) * 4
	return [3]byte{rgba[i], rgba[i+1], rgba[i+2]}
}

func TestPaintProgress(t *testing.T) {
	const w, h = 64, 48
	rgba := make([]byte, w*h*4)
	paintProgress(rgba, w, h, 1, 2)
	// Bar spans x 8..56 at y 23..27; half done fills 8..32.
	if got := pixel(rgba, w, 10, 24); got != [3]byte{200, 32, 16} {
		t.Fatalf("filled part %v", got)
	}
	if got := pixel(rgba, w, 50, 24); got != [3]byte{40, 16, 16} {
		t.Fatalf("empty part %v", got)
	}
	paintProgress(rgba, w, h, 10, 0) // unknown total: no fill, no panic
	if got := pixel(rgba, w, 10, 24); got != [3]byte{40, 16, 16} {
		t.Fatalf("unknown total filled %v", got)
	}
	paintProgress(rgba, w, h, 5, 2) // over-reporting clamps
	if got := pixel(rgba, w, 50, 24); got != [3]byte{200, 32, 16} {
		t.Fatalf("clamped full bar %v", got)
	}
	paintFail(rgba, w, h)
	if got := pixel(rgba, w, 0, 0); got != [3]byte{96, 0, 0} {
		t.Fatalf("fail screen %v", got)
	}
}

func TestStateLine(t *testing.T) {
	got := stateLine(gore.Snapshot{State: gore.StateLevel, MenuActive: true, Episode: 1, Map: 1, Health: 100, X: 1056, Y: -3616})
	want := "state=level menu=1 demo=0 map=E1M1 health=100 pos=1056,-3616"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
