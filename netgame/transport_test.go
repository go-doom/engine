// SPDX-License-Identifier: BSD-3-Clause
// Copyright (c) the go-doom/engine authors.
// Classic DOOM netgame (doomcom/ticcmd lockstep) — pure-Go, CGO=0.

package netgame

import (
	"errors"
	"net"
	"testing"
	"time"
)

func TestMemMeshBasic(t *testing.T) {
	m := NewMemMesh(3)
	a := m.Endpoint(0)
	b := m.Endpoint(1)

	if _, ok := b.Recv(); ok {
		t.Fatal("expected empty inbox")
	}
	if err := a.Send(1, []byte("hello")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	p, ok := b.Recv()
	if !ok {
		t.Fatal("expected a packet")
	}
	if p.Node != 0 || string(p.Data) != "hello" {
		t.Fatalf("got %+v", p)
	}
	if _, ok := b.Recv(); ok {
		t.Fatal("inbox should be drained")
	}
}

func TestMemMeshErrors(t *testing.T) {
	m := NewMemMesh(2)
	a := m.Endpoint(0)
	if err := a.Send(5, nil); !errors.Is(err, ErrBadNode) {
		t.Fatalf("bad node err = %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := a.Send(1, nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed err = %v", err)
	}
}

func TestMemMeshDrop(t *testing.T) {
	m := NewMemMesh(2)
	m.SetDrop(func(from, to, seq int) bool { return true }) // drop everything
	a := m.Endpoint(0)
	b := m.Endpoint(1)
	if err := a.Send(1, []byte("x")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, ok := b.Recv(); ok {
		t.Fatal("packet should have been dropped")
	}
}

func TestMemMeshReorder(t *testing.T) {
	m := NewMemMesh(2)
	m.SetReorder(1)
	a := m.Endpoint(0)
	b := m.Endpoint(1)
	const n = 20
	for i := 0; i < n; i++ {
		if err := a.Send(1, []byte{byte(i)}); err != nil {
			t.Fatalf("Send: %v", err)
		}
	}
	seen := make([]bool, n)
	ordered := true
	prev := -1
	for {
		p, ok := b.Recv()
		if !ok {
			break
		}
		v := int(p.Data[0])
		seen[v] = true
		if v < prev {
			ordered = false
		}
		prev = v
	}
	for i := range seen {
		if !seen[i] {
			t.Fatalf("packet %d lost during reorder", i)
		}
	}
	if ordered {
		t.Fatal("expected reordering, got in-order delivery")
	}
}

func TestUDPTransport(t *testing.T) {
	la, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	c0, err := net.ListenUDP("udp", la)
	if err != nil {
		t.Fatalf("listen c0: %v", err)
	}
	c1, err := net.ListenUDP("udp", la)
	if err != nil {
		t.Fatalf("listen c1: %v", err)
	}
	a0 := c0.LocalAddr().(*net.UDPAddr)
	a1 := c1.LocalAddr().(*net.UDPAddr)

	t0 := NewUDPTransport(c0, []*net.UDPAddr{nil, a1})
	t1 := NewUDPTransport(c1, []*net.UDPAddr{a0, nil})
	t1.SetPollTimeout(3 * time.Millisecond)

	msg := []byte("doomdata")
	if err := t0.Send(1, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got, ok := recvRetry(t1)
	if !ok {
		t.Fatal("no datagram received")
	}
	if got.Node != 0 || string(got.Data) != "doomdata" {
		t.Fatalf("got %+v", got)
	}

	// Send to an out-of-range / nil-address node.
	if err := t0.Send(9, msg); !errors.Is(err, ErrBadNode) {
		t.Fatalf("bad node: %v", err)
	}
	if err := t0.Send(0, msg); !errors.Is(err, ErrBadNode) {
		t.Fatalf("nil address node: %v", err)
	}

	// Datagram from an unknown source maps to Node = -1.
	c2, err := net.ListenUDP("udp", la)
	if err != nil {
		t.Fatalf("listen c2: %v", err)
	}
	if _, err := c2.WriteToUDP([]byte("stranger"), a1); err != nil {
		t.Fatalf("c2 write: %v", err)
	}
	unk, ok := recvRetry(t1)
	if !ok {
		t.Fatal("no datagram from stranger")
	}
	if unk.Node != -1 {
		t.Fatalf("unknown source Node = %d, want -1", unk.Node)
	}
	_ = c2.Close()

	// Recv on a closed conn returns ok=false; Send on a closed conn errors.
	if err := t1.Close(); err != nil {
		t.Fatalf("close t1: %v", err)
	}
	if _, ok := t1.Recv(); ok {
		t.Fatal("Recv after close should report no packet")
	}
	if err := t0.Close(); err != nil {
		t.Fatalf("close t0: %v", err)
	}
	if err := t0.Send(1, msg); err == nil {
		t.Fatal("Send after close should error")
	}
}

// recvRetry polls a transport briefly, since a real UDP datagram may not be in
// the socket buffer the instant after Send returns.
func recvRetry(tr Transport) (Packet, bool) {
	for i := 0; i < 200; i++ {
		if p, ok := tr.Recv(); ok {
			return p, true
		}
		time.Sleep(time.Millisecond)
	}
	return Packet{}, false
}
