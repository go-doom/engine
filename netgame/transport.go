// SPDX-License-Identifier: BSD-3-Clause
// Copyright (c) the go-doom/engine authors.
// Classic DOOM netgame (doomcom/ticcmd lockstep) — pure-Go, CGO=0.

package netgame

import (
	"errors"
	"math/rand"
	"net"
	"sync"
	"time"
)

// Transport errors.
var (
	// ErrClosed is returned by a transport whose Close has been called.
	ErrClosed = errors.New("netgame: transport closed")
	// ErrBadNode is returned when Send targets a node index outside [0,n).
	ErrBadNode = errors.New("netgame: destination node out of range")
)

// Packet is a datagram received from a peer. Node is the sender's node index
// and Data is the raw (already length-delimited by the datagram) packet body.
type Packet struct {
	Node int
	Data []byte
}

// Transport is the injectable datagram seam used by the lockstep loop. It is
// deliberately non-blocking: Recv reports whether a packet was available rather
// than blocking, so the loop can poll all nodes deterministically.
type Transport interface {
	// Send delivers data to the given peer node index.
	Send(node int, data []byte) error
	// Recv returns the next queued packet, or ok=false if none is pending.
	Recv() (Packet, bool)
	// Close releases the transport's resources.
	Close() error
}

// ---------------------------------------------------------------------------
// In-memory mesh transport (for tests, no real sockets).
// ---------------------------------------------------------------------------

// MemMesh wires N nodes together in-process. Each node gets a Transport from
// Endpoint(i); Send from node i to node j enqueues a copy of the data into
// node j's inbox, tagged with sender i. Optional deterministic packet loss and
// reordering can be installed for resilience testing.
type MemMesh struct {
	mu      sync.Mutex
	n       int
	queues  [][]Packet
	drop    func(from, to, seq int) bool
	reorder bool
	rnd     *rand.Rand
	seq     int
	closed  bool
}

// NewMemMesh creates a mesh for n nodes.
func NewMemMesh(n int) *MemMesh {
	return &MemMesh{n: n, queues: make([][]Packet, n)}
}

// Endpoint returns the Transport for node id.
func (m *MemMesh) Endpoint(id int) Transport { return &memEndpoint{mesh: m, id: id} }

// SetDrop installs a delivery filter. Returning true drops the packet in
// transit (the Send still succeeds — datagrams are lossy). from/to are node
// indices and seq is a monotonically increasing per-mesh counter, so the filter
// can be made fully deterministic.
func (m *MemMesh) SetDrop(f func(from, to, seq int) bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.drop = f
}

// SetReorder enables deterministic reordering: delivered packets are inserted at
// a pseudo-random position in the receiver's queue, seeded by seed.
func (m *MemMesh) SetReorder(seed int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reorder = true
	m.rnd = rand.New(rand.NewSource(seed))
}

type memEndpoint struct {
	mesh *MemMesh
	id   int
}

func (e *memEndpoint) Send(node int, data []byte) error {
	m := e.mesh
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	if node < 0 || node >= m.n {
		return ErrBadNode
	}
	seq := m.seq
	m.seq++
	if m.drop != nil && m.drop(e.id, node, seq) {
		return nil // silently lost in transit
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	pkt := Packet{Node: e.id, Data: cp}

	q := m.queues[node]
	if m.reorder && len(q) > 0 {
		idx := m.rnd.Intn(len(q) + 1)
		q = append(q, Packet{})
		copy(q[idx+1:], q[idx:])
		q[idx] = pkt
	} else {
		q = append(q, pkt)
	}
	m.queues[node] = q
	return nil
}

func (e *memEndpoint) Recv() (Packet, bool) {
	m := e.mesh
	m.mu.Lock()
	defer m.mu.Unlock()
	q := m.queues[e.id]
	if len(q) == 0 {
		return Packet{}, false
	}
	p := q[0]
	m.queues[e.id] = q[1:]
	return p, true
}

func (e *memEndpoint) Close() error {
	m := e.mesh
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

// ---------------------------------------------------------------------------
// UDP transport (real sockets, pure-Go, CGO=0).
// ---------------------------------------------------------------------------

// UDPTransport is a real-socket Transport backed by a net.UDPConn. It maps peer
// node indices to UDP addresses and back, so Recv can report which node a
// datagram came from. It performs no CGO and works on every Go target.
type UDPTransport struct {
	conn        *net.UDPConn
	addrs       []*net.UDPAddr // node index -> peer address
	rev         map[string]int // peer address string -> node index
	buf         []byte
	pollTimeout time.Duration // max time a single Recv poll may block
}

// defaultPollTimeout bounds how long a UDP Recv poll blocks. It is deliberately
// small so the lockstep loop stays responsive, but non-zero: a zero/past read
// deadline can return "i/o timeout" even when a datagram is already buffered.
const defaultPollTimeout = 2 * time.Millisecond

// NewUDPTransport builds a transport over an already-bound conn. addrs maps each
// peer node index to its UDP address (the local node's own slot may be nil).
func NewUDPTransport(conn *net.UDPConn, addrs []*net.UDPAddr) *UDPTransport {
	rev := make(map[string]int, len(addrs))
	for i, a := range addrs {
		if a != nil {
			rev[a.String()] = i
		}
	}
	return &UDPTransport{
		conn:        conn,
		addrs:       addrs,
		rev:         rev,
		buf:         make([]byte, 2048),
		pollTimeout: defaultPollTimeout,
	}
}

// SetPollTimeout adjusts how long a single Recv may block waiting for a datagram.
func (u *UDPTransport) SetPollTimeout(d time.Duration) { u.pollTimeout = d }

// Send writes data to the peer node's UDP address.
func (u *UDPTransport) Send(node int, data []byte) error {
	if node < 0 || node >= len(u.addrs) || u.addrs[node] == nil {
		return ErrBadNode
	}
	_, err := u.conn.WriteToUDP(data, u.addrs[node])
	return err
}

// Recv performs a non-blocking read. If no datagram is pending it returns
// ok=false. Datagrams from unknown addresses are reported with Node=-1.
func (u *UDPTransport) Recv() (Packet, bool) {
	_ = u.conn.SetReadDeadline(time.Now().Add(u.pollTimeout))
	n, addr, err := u.conn.ReadFromUDP(u.buf)
	if err != nil {
		return Packet{}, false
	}
	node := -1
	if idx, ok := u.rev[addr.String()]; ok {
		node = idx
	}
	data := make([]byte, n)
	copy(data, u.buf[:n])
	return Packet{Node: node, Data: data}, true
}

// Close closes the underlying UDP connection.
func (u *UDPTransport) Close() error { return u.conn.Close() }
