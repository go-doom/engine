// SPDX-License-Identifier: BSD-3-Clause
// Copyright (c) the go-doom/engine authors.
// Classic DOOM netgame (doomcom/ticcmd lockstep) — pure-Go, CGO=0.

package netgame

import (
	"encoding/binary"
	"errors"
	"hash/fnv"
	"math/rand"
	"testing"
)

// splitmix64 is a tiny deterministic PRNG used to derive reproducible ticcmds.
func splitmix64(x uint64) uint64 {
	x += 0x9E3779B97F4A7C15
	x = (x ^ (x >> 30)) * 0xBF58476D1CE4E5B9
	x = (x ^ (x >> 27)) * 0x94D049BB133111EB
	return x ^ (x >> 31)
}

// detBuild returns a deterministic input source for a given player: identical
// on every node, so all nodes agree on that player's ticcmd for each tic.
func detBuild(player int) BuildFunc {
	return func(tic int) Ticcmd {
		h := splitmix64(uint64(player)*0x100000001B3 + uint64(tic))
		return Ticcmd{
			ForwardMove: int8(h),
			SideMove:    int8(h >> 8),
			AngleTurn:   int16(h >> 16),
			Buttons:     uint8(h >> 32),
			ChatChar:    uint8(h >> 40),
		}
	}
}

// meshNodes handshakes n nodes over mesh and returns them ready to run. Each
// node uses a fresh HashGame with the same seed and its own deterministic input.
func meshNodes(t *testing.T, n int, mesh *MemMesh, gameFor func(i int) Game) []*Node {
	t.Helper()
	trs := make([]Transport, n)
	for i := 0; i < n; i++ {
		trs[i] = mesh.Endpoint(i)
	}
	dcs, err := Handshake(trs, Config{TicDup: 1}, 64)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	// Drain any residual SETUP packets so per-node stats start clean.
	for i := 0; i < n; i++ {
		for {
			if _, ok := trs[i].Recv(); !ok {
				break
			}
		}
	}
	nodes := make([]*Node, n)
	for i := 0; i < n; i++ {
		nodes[i] = NewNode(dcs[i], trs[i], gameFor(i), detBuild(i))
	}
	return nodes
}

func defaultGame(int) Game { return NewHashGame(0x5EED) }

// assertIdentical checks that all nodes share byte-identical per-tic state-hash
// histories (the lockstep determinism oracle).
func assertIdentical(t *testing.T, nodes []*Node, target int) {
	t.Helper()
	ref := nodes[0].StateHashes()
	if len(ref) != target+1 {
		t.Fatalf("node 0 ran %d tics, want %d", len(ref)-1, target)
	}
	for i := 1; i < len(nodes); i++ {
		h := nodes[i].StateHashes()
		if len(h) != len(ref) {
			t.Fatalf("node %d history len %d != %d", i, len(h), len(ref))
		}
		for k := range ref {
			if h[k] != ref[k] {
				t.Fatalf("node %d diverged at tic %d: %#016x != %#016x", i, k, h[k], ref[k])
			}
		}
	}
}

func TestLockstepDeterminism(t *testing.T) {
	const target = 150
	for _, n := range []int{2, 3, 4} {
		mesh := NewMemMesh(n)
		nodes := meshNodes(t, n, mesh, defaultGame)
		if err := Simulate(nodes, target); err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		for _, nd := range nodes {
			if nd.GameTic() != target {
				t.Fatalf("n=%d node %d reached tic %d, want %d", n, nd.id, nd.GameTic(), target)
			}
			if nd.MakeTic() != target {
				t.Fatalf("n=%d node %d maketic %d, want %d", n, nd.id, nd.MakeTic(), target)
			}
		}
		assertIdentical(t, nodes, target)
	}
}

func TestPacketLossConvergence(t *testing.T) {
	const (
		n      = 3
		target = 120
	)
	mesh := NewMemMesh(n)
	r := rand.New(rand.NewSource(1234))
	mesh.SetDrop(func(from, to, seq int) bool { return r.Float64() < 0.30 })
	nodes := meshNodes(t, n, mesh, defaultGame)
	if err := Simulate(nodes, target); err != nil {
		t.Fatalf("under 30%% loss: %v", err)
	}
	assertIdentical(t, nodes, target)

	totalResends := 0
	for _, nd := range nodes {
		totalResends += nd.Stats().Resends
	}
	if totalResends == 0 {
		t.Fatal("expected the resend path to be exercised under loss")
	}
}

func TestReorderConvergence(t *testing.T) {
	const (
		n      = 2
		target = 100
	)
	mesh := NewMemMesh(n)
	mesh.SetReorder(99)
	r := rand.New(rand.NewSource(7))
	mesh.SetDrop(func(from, to, seq int) bool { return r.Float64() < 0.15 })
	nodes := meshNodes(t, n, mesh, defaultGame)
	if err := Simulate(nodes, target); err != nil {
		t.Fatalf("under reorder+loss: %v", err)
	}
	assertIdentical(t, nodes, target)
}

func TestLostNodeTimeout(t *testing.T) {
	const n = 3
	mesh := NewMemMesh(n)
	nodes := meshNodes(t, n, mesh, defaultGame)
	// After a clean handshake, node 0 goes permanently silent.
	mesh.SetDrop(func(from, to, seq int) bool { return from == 0 })
	err := RunGame(nodes, 100, 20000)
	var te *TimeoutError
	if !errors.As(err, &te) {
		t.Fatalf("err = %v, want *TimeoutError", err)
	}
	if te.Error() == "" {
		t.Fatal("empty timeout message")
	}
}

// desyncGame mirrors HashGame but injects a divergence at tic badAt, modelling a
// node whose simulation drifts out of sync with its peers.
type desyncGame struct {
	state uint64
	step  int
	badAt int
}

func (g *desyncGame) Hash() uint64 { return g.state }

func (g *desyncGame) Step(cmds []Ticcmd) {
	h := fnv.New64a()
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], g.state)
	_, _ = h.Write(buf[:])
	for i := range cmds {
		b, _ := cmds[i].MarshalBinary()
		b[4], b[5] = 0, 0
		_, _ = h.Write(b)
	}
	g.state = h.Sum64()
	if g.step == g.badAt {
		g.state ^= 1 // silent corruption
	}
	g.step++
}

func TestDesyncDetection(t *testing.T) {
	const (
		n      = 3
		target = 60
	)
	mesh := NewMemMesh(n)
	nodes := meshNodes(t, n, mesh, func(i int) Game {
		if i == 1 {
			return &desyncGame{state: 0x5EED, badAt: 10}
		}
		return NewHashGame(0x5EED)
	})
	err := Simulate(nodes, target)
	var de *DesyncError
	if !errors.As(err, &de) {
		t.Fatalf("err = %v, want *DesyncError", err)
	}
	if de.Player != 1 {
		t.Fatalf("desync attributed to player %d, want 1", de.Player)
	}
	if de.Got == de.Want {
		t.Fatalf("desync error with matching consistancy: %+v", de)
	}
	if de.Error() == "" {
		t.Fatal("empty desync message")
	}
}

func TestRunGameMaxRounds(t *testing.T) {
	const n = 2
	mesh := NewMemMesh(n)
	nodes := meshNodes(t, n, mesh, defaultGame)
	if err := RunGame(nodes, 150, 2); !errors.Is(err, ErrMaxRounds) {
		t.Fatalf("err = %v, want ErrMaxRounds", err)
	}
}

func TestNetUpdateSendError(t *testing.T) {
	const n = 2
	mesh := NewMemMesh(n)
	nodes := meshNodes(t, n, mesh, defaultGame)
	// Closing one endpoint closes the whole mesh; the next Send must error out
	// through netUpdate and RunGame.
	if err := nodes[0].tr.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := RunGame(nodes, 50, 100); !errors.Is(err, ErrClosed) {
		t.Fatalf("err = %v, want ErrClosed", err)
	}
}

func TestHandshakeFailure(t *testing.T) {
	const n = 3
	mesh := NewMemMesh(n)
	mesh.SetDrop(func(from, to, seq int) bool { return true }) // nothing gets through
	trs := make([]Transport, n)
	for i := 0; i < n; i++ {
		trs[i] = mesh.Endpoint(i)
	}
	if _, err := Handshake(trs, Config{}, 5); !errors.Is(err, ErrHandshake) {
		t.Fatalf("err = %v, want ErrHandshake", err)
	}
}

func TestHandshakeAssignsConsoleplayer(t *testing.T) {
	const n = 4
	mesh := NewMemMesh(n)
	trs := make([]Transport, n)
	for i := 0; i < n; i++ {
		trs[i] = mesh.Endpoint(i)
	}
	dcs, err := Handshake(trs, Config{TicDup: 2, ExtraTics: 1, DeathMatch: 1}, 64)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	for i, dc := range dcs {
		if dc.ConsolePlayer != i {
			t.Fatalf("node %d consoleplayer = %d", i, dc.ConsolePlayer)
		}
		if dc.NumNodes != n || dc.NumPlayers != n {
			t.Fatalf("node %d nodes/players = %d/%d", i, dc.NumNodes, dc.NumPlayers)
		}
		if dc.TicDup != 2 || dc.ExtraTics != 1 || dc.DeathMatch != 1 {
			t.Fatalf("node %d config not propagated: %+v", i, dc)
		}
	}
}

func TestNetUpdateWindowCap(t *testing.T) {
	mesh := NewMemMesh(2)
	dc := &Doomcom{NumNodes: 2, NumPlayers: 2, ConsolePlayer: 0}
	nd := NewNode(dc, mesh.Endpoint(0), NewHashGame(0), detBuild(0))

	// Fabricate a far-ahead node so the send window exceeds BACKUPTICS.
	nd.target = 400
	nd.gametic = 300
	for len(nd.hashes) <= nd.gametic {
		nd.hashes = append(nd.hashes, uint64(len(nd.hashes)))
	}
	// Ask to resend from tic 0: start collapses to 0, so num > BACKUPTICS and
	// must be capped.
	nd.pendingReq[1] = 0
	if err := nd.netUpdate(); err != nil {
		t.Fatalf("netUpdate: %v", err)
	}
	p, ok := mesh.Endpoint(1).Recv()
	if !ok {
		t.Fatal("expected a packet")
	}
	dd, err := DecodeDoomdata(p.Data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if int(dd.NumTics) != BACKUPTICS {
		t.Fatalf("NumTics = %d, want capped to %d", dd.NumTics, BACKUPTICS)
	}
	if int(dd.StartTic) != nd.maketic-BACKUPTICS {
		t.Fatalf("StartTic = %d, want %d", dd.StartTic, nd.maketic-BACKUPTICS)
	}
}

func TestNetUpdateNothingToSend(t *testing.T) {
	mesh := NewMemMesh(2)
	dc := &Doomcom{NumNodes: 2, NumPlayers: 2, ConsolePlayer: 0}
	nd := NewNode(dc, mesh.Endpoint(0), NewHashGame(0), detBuild(0))
	nd.target = 0 // produce nothing -> num <= 0 -> nothing sent
	if err := nd.netUpdate(); err != nil {
		t.Fatalf("netUpdate: %v", err)
	}
	if _, ok := mesh.Endpoint(1).Recv(); ok {
		t.Fatal("no packet should be sent when there are no tics")
	}
}

func TestDiagnoseStall(t *testing.T) {
	mesh := NewMemMesh(2)
	// A node that has reached its target contributes no diagnosis.
	done := NewNode(&Doomcom{NumNodes: 1, NumPlayers: 1, ConsolePlayer: 0},
		mesh.Endpoint(0), NewHashGame(0), detBuild(0))
	done.target = 0
	if te := diagnoseStall([]*Node{done}); te != nil {
		t.Fatalf("finished node diagnosed as stalled: %v", te)
	}
	// A node short of target, missing player 0's tic, is diagnosed.
	stuck := NewNode(&Doomcom{NumNodes: 2, NumPlayers: 2, ConsolePlayer: 1},
		mesh.Endpoint(1), NewHashGame(0), detBuild(1))
	stuck.target = 5
	te := diagnoseStall([]*Node{stuck})
	if te == nil || te.Player != 0 || te.Tic != 0 {
		t.Fatalf("diagnose = %+v, want player 0 tic 0", te)
	}
}

// TestPumpHandlesAllPacketKinds drives pump directly with crafted packets to
// cover the retransmit-request, exit, bad-checksum and out-of-range branches.
func TestPumpHandlesAllPacketKinds(t *testing.T) {
	mesh := NewMemMesh(2)
	dc := &Doomcom{NumNodes: 2, NumPlayers: 2, ConsolePlayer: 0}
	nd := NewNode(dc, mesh.Endpoint(0), NewHashGame(0), detBuild(0))
	peer := mesh.Endpoint(1)

	// Normal tic delivery from player 1.
	good := &Doomdata{Player: 1, StartTic: 0, Cmds: []Ticcmd{{ForwardMove: 7}}}
	gb, _ := good.Encode()
	// Retransmit request.
	req := &Doomdata{Flags: NCMD_RETRANSMIT, RetransmitFrom: 5, Player: 1}
	rb, _ := req.Encode()
	// Exit notice.
	exit := &Doomdata{Flags: NCMD_EXIT, Player: 1}
	eb, _ := exit.Encode()
	// Out-of-range player.
	oor := &Doomdata{Player: 9}
	ob, _ := oor.Encode()

	for _, b := range [][]byte{gb, rb, eb, ob} {
		if err := peer.Send(0, b); err != nil {
			t.Fatalf("send: %v", err)
		}
	}
	// A corrupt datagram (bad checksum) must be counted as dropped.
	bad := append([]byte(nil), gb...)
	bad[len(bad)-1] ^= 0xFF
	if err := peer.Send(0, bad); err != nil {
		t.Fatalf("send bad: %v", err)
	}

	nd.pump()

	if _, ok := nd.cmds[1][0]; !ok {
		t.Fatal("normal tic was not stored")
	}
	if nd.pendingReq[1] != 5 {
		t.Fatalf("retransmit request not recorded: %d", nd.pendingReq[1])
	}
	if !nd.exited[1] {
		t.Fatal("exit flag not set")
	}
	if nd.stats.PacketsDropped != 1 {
		t.Fatalf("PacketsDropped = %d, want 1", nd.stats.PacketsDropped)
	}
}
