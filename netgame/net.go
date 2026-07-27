// SPDX-License-Identifier: BSD-3-Clause
// Copyright (c) the go-doom/engine authors.
// Classic DOOM netgame (doomcom/ticcmd lockstep) — pure-Go, CGO=0.

package netgame

import (
	"errors"
	"fmt"
)

// Protocol constants, modelled on vanilla d_net.c.
const (
	// BACKUPTICS is the vanilla send/receive window and the maximum span of a
	// single retransmit or the number of tics a node may buffer ahead.
	BACKUPTICS = 128

	// ticLead is how many tics a node may produce (maketic) ahead of the tic it
	// has simulated (gametic). It doubles as the consistancy lag: the
	// consistancy carried by a ticcmd for tic T is the state hash at tic
	// max(0, T-ticLead), which every synced node can compute by the time it
	// produces (and later runs) tic T. The invariant lag == lead is required so
	// the lagged hash is always available on both sides.
	ticLead = 3

	// windowTics is how many recent tics each NetUpdate re-sends unsolicited, so
	// isolated packet losses recover without an explicit retransmit request.
	windowTics = 12

	// timeoutRounds is how many scheduler rounds with zero global progress are
	// tolerated before RunGame reports a lost node.
	timeoutRounds = 64
)

// Loop errors.
var (
	// ErrMaxRounds is returned when RunGame exhausts its round budget without
	// every node reaching the target tic (and without a cleaner diagnosis).
	ErrMaxRounds = errors.New("netgame: exceeded maximum scheduler rounds")
	// ErrHandshake is returned when the handshake fails to converge.
	ErrHandshake = errors.New("netgame: handshake did not converge")
)

// DesyncError reports a consistancy-check failure: player Player's ticcmd for
// tic Tic carried consistancy Got, but node Node computed Want for the state at
// that tic. It signals a divergence between nodes — the game must stop, not
// crash.
type DesyncError struct {
	Node   int
	Player int
	Tic    int
	Got    uint16
	Want   uint16
}

func (e *DesyncError) Error() string {
	return fmt.Sprintf("netgame: consistancy failure at node %d, player %d, tic %d: got %#04x want %#04x",
		e.Node, e.Player, e.Tic, e.Got, e.Want)
}

// TimeoutError reports that node Node could not obtain player Player's ticcmd
// for tic Tic within the timeout — a lost or unreachable node.
type TimeoutError struct {
	Node   int
	Player int
	Tic    int
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("netgame: node %d timed out waiting for player %d at tic %d",
		e.Node, e.Player, e.Tic)
}

// BuildFunc produces this node's local ticcmd for the given tic. It must be a
// pure, deterministic function of tic (and the node's own player index, which
// is captured by the closure). The consistancy field is filled in by the loop
// and may be left zero.
type BuildFunc func(tic int) Ticcmd

// Stats records per-node counters for observability and tests.
type Stats struct {
	PacketsSent    int
	PacketsRecv    int
	PacketsDropped int // packets that failed to decode (e.g. bad checksum)
	TicsRun        int
	Resends        int // retransmit requests this node issued
}

// Node is one participant in the lockstep game. It owns its Doomcom, a Transport
// to its peers, a Game (the world model) and a BuildFunc (its input source). The
// scheduler (RunGame) drives every node through netUpdate/pump/tryRun each round.
type Node struct {
	id       int
	numNodes int
	tr       Transport
	game     Game
	build    BuildFunc

	maketic int
	gametic int
	target  int
	round   int

	// cmds[player][tic] -> ticcmd received (or locally produced for own player).
	cmds []map[int]Ticcmd
	// hashes[k] is the 64-bit state hash after k tics have run (hashes[0] is the
	// initial state). uint16(hashes[t]) is the consistancy value for tic t.
	hashes []uint64

	// pendingReq[dest] is the lowest retransmit-from requested by dest (-1 none).
	pendingReq []int
	exited     []bool

	stats Stats
}

// NewNode builds a node from a negotiated Doomcom, a transport, a game model and
// a build function. The transport must deliver packets tagged with the sender's
// player index (as MemMesh and UDPTransport do).
func NewNode(dc *Doomcom, tr Transport, game Game, build BuildFunc) *Node {
	n := &Node{
		id:         dc.ConsolePlayer,
		numNodes:   dc.NumNodes,
		tr:         tr,
		game:       game,
		build:      build,
		cmds:       make([]map[int]Ticcmd, dc.NumNodes),
		hashes:     []uint64{game.Hash()},
		pendingReq: make([]int, dc.NumNodes),
		exited:     make([]bool, dc.NumNodes),
	}
	for i := range n.cmds {
		n.cmds[i] = make(map[int]Ticcmd)
	}
	for i := range n.pendingReq {
		n.pendingReq[i] = -1
	}
	return n
}

// GameTic returns the number of tics this node has simulated.
func (n *Node) GameTic() int { return n.gametic }

// MakeTic returns the highest local tic this node has produced.
func (n *Node) MakeTic() int { return n.maketic }

// StateHashes returns the per-tic state-hash history: index k is the state after
// k tics. Two nodes that ran the same tics in lockstep have identical slices.
func (n *Node) StateHashes() []uint64 { return n.hashes }

// Stats returns a snapshot of this node's counters.
func (n *Node) Stats() Stats { return n.stats }

// consistancyAt returns uint16 of the state hash at tic max(0, tic-ticLead).
// The caller guarantees that index is present in hashes.
func (n *Node) consistancyAt(tic int) uint16 {
	i := tic - ticLead
	if i < 0 {
		i = 0
	}
	return uint16(n.hashes[i])
}

// netUpdate produces new local tics (up to ticLead ahead of gametic, bounded by
// target) and sends the recent send-window of local commands to every peer,
// honouring any pending retransmit requests.
func (n *Node) netUpdate() error {
	for n.maketic < n.gametic+ticLead && n.maketic < n.target {
		cmd := n.build(n.maketic)
		cmd.Consistancy = n.consistancyAt(n.maketic)
		n.cmds[n.id][n.maketic] = cmd
		n.maketic++
	}

	for d := 0; d < n.numNodes; d++ {
		if d == n.id {
			continue
		}
		start := n.maketic - windowTics
		if start < 0 {
			start = 0
		}
		if req := n.pendingReq[d]; req >= 0 && req < start {
			start = req
		}
		n.pendingReq[d] = -1

		num := n.maketic - start
		if num <= 0 {
			continue
		}
		if num > BACKUPTICS {
			start = n.maketic - BACKUPTICS
			num = BACKUPTICS
		}

		dd := &Doomdata{Player: uint8(n.id), StartTic: uint8(start)}
		dd.Cmds = make([]Ticcmd, num)
		for i := 0; i < num; i++ {
			dd.Cmds[i] = n.cmds[n.id][start+i]
		}
		b, err := dd.Encode()
		if err != nil {
			return err
		}
		if err := n.tr.Send(d, b); err != nil {
			return err
		}
		n.stats.PacketsSent++
	}
	return nil
}

// pump drains all pending inbound packets, storing received ticcmds and noting
// retransmit requests and exits. Packets that fail to decode are counted as
// dropped and ignored (lossy datagram semantics).
func (n *Node) pump() {
	for {
		p, ok := n.tr.Recv()
		if !ok {
			return
		}
		n.stats.PacketsRecv++
		dd, err := DecodeDoomdata(p.Data)
		if err != nil {
			n.stats.PacketsDropped++
			continue
		}
		if dd.Flags&NCMD_RETRANSMIT != 0 {
			rf := int(dd.RetransmitFrom)
			if n.pendingReq[p.Node] < 0 || rf < n.pendingReq[p.Node] {
				n.pendingReq[p.Node] = rf
			}
			continue
		}
		if dd.Flags&NCMD_EXIT != 0 {
			n.exited[dd.Player] = true
		}
		pl := int(dd.Player)
		if pl < 0 || pl >= n.numNodes {
			continue
		}
		for i := 0; i < int(dd.NumTics); i++ {
			t := int(dd.StartTic) + i
			if _, have := n.cmds[pl][t]; !have {
				n.cmds[pl][t] = dd.Cmds[i]
			}
		}
	}
}

// tryRun runs every tic for which all players' commands are available, checking
// consistancy before each. On a gap it issues a retransmit request to the
// missing player and stops. It returns a *DesyncError on a consistancy mismatch.
func (n *Node) tryRun() error {
	for n.gametic < n.target {
		missing := -1
		for p := 0; p < n.numNodes; p++ {
			if _, ok := n.cmds[p][n.gametic]; !ok {
				missing = p
				break
			}
		}
		if missing >= 0 {
			n.requestResend(missing, n.gametic)
			return nil
		}

		want := n.consistancyAt(n.gametic)
		cmds := make([]Ticcmd, n.numNodes)
		for p := 0; p < n.numNodes; p++ {
			c := n.cmds[p][n.gametic]
			if c.Consistancy != want {
				return &DesyncError{
					Node: n.id, Player: p, Tic: n.gametic,
					Got: c.Consistancy, Want: want,
				}
			}
			cmds[p] = c
		}

		n.game.Step(cmds)
		n.gametic++
		n.hashes = append(n.hashes, n.game.Hash())
		n.stats.TicsRun++
	}
	return nil
}

// requestResend sends an NCMD_RETRANSMIT request to player from, asking it to
// resend starting at tic.
func (n *Node) requestResend(from, tic int) {
	req := &Doomdata{
		Flags:          NCMD_RETRANSMIT,
		RetransmitFrom: uint8(tic),
		Player:         uint8(n.id),
	}
	b, err := req.Encode()
	if err != nil {
		return
	}
	if err := n.tr.Send(from, b); err == nil {
		n.stats.Resends++
	}
}

// RunGame drives nodes in lockstep until every node has simulated target tics,
// or an error occurs. It returns:
//
//   - *DesyncError    on a consistancy mismatch,
//   - *TimeoutError   when a node cannot obtain a peer's tics (lost node),
//   - ErrMaxRounds    if maxRounds is exhausted without any cleaner diagnosis,
//   - a transport error, or nil on success.
//
// The scheduler is deterministic: each round it runs netUpdate, then pump, then
// tryRun for every node in index order, so a given set of nodes, games, build
// functions and (deterministic) transport always produces the same result.
func RunGame(nodes []*Node, target, maxRounds int) error {
	for _, nd := range nodes {
		nd.target = target
	}

	stalled := 0
	for round := 0; round < maxRounds; round++ {
		for _, nd := range nodes {
			nd.round = round
			if err := nd.netUpdate(); err != nil {
				return err
			}
		}
		for _, nd := range nodes {
			nd.pump()
		}

		progressed := false
		for _, nd := range nodes {
			before := nd.gametic
			if err := nd.tryRun(); err != nil {
				return err
			}
			if nd.gametic > before {
				progressed = true
			}
		}

		if allDone(nodes, target) {
			return nil
		}
		if progressed {
			stalled = 0
			continue
		}
		stalled++
		if stalled > timeoutRounds {
			if te := diagnoseStall(nodes); te != nil {
				return te
			}
			return ErrMaxRounds
		}
	}
	return ErrMaxRounds
}

// Simulate is a convenience wrapper around RunGame with a generous round budget.
func Simulate(nodes []*Node, target int) error {
	return RunGame(nodes, target, target*16+2048)
}

func allDone(nodes []*Node, target int) bool {
	for _, nd := range nodes {
		if nd.gametic < target {
			return false
		}
	}
	return true
}

// diagnoseStall finds a node still short of target and the first player whose
// tic it is missing, so a lost-node timeout can be reported cleanly.
func diagnoseStall(nodes []*Node) *TimeoutError {
	for _, nd := range nodes {
		if nd.gametic >= nd.target {
			continue
		}
		for p := 0; p < nd.numNodes; p++ {
			if _, ok := nd.cmds[p][nd.gametic]; !ok {
				return &TimeoutError{Node: nd.id, Player: p, Tic: nd.gametic}
			}
		}
	}
	return nil
}

// Handshake performs deterministic node discovery over the given transports
// (trs[i] belongs to node i). Each node repeatedly broadcasts an NCMD_SETUP
// announcement and collects announcements from its peers; once every node has
// heard from all N participants, each is assigned a Doomcom with the shared
// Config, NumNodes/NumPlayers = N and ConsolePlayer = its own index (node
// indices are the lowest-id-first arbitration). It returns ErrHandshake if it
// does not converge within maxRounds.
func Handshake(trs []Transport, cfg Config, maxRounds int) ([]*Doomcom, error) {
	nn := len(trs)
	seen := make([]map[int]bool, nn)
	for i := range seen {
		seen[i] = map[int]bool{i: true}
	}

	for round := 0; round < maxRounds; round++ {
		for i := 0; i < nn; i++ {
			dd := &Doomdata{Flags: NCMD_SETUP, Player: uint8(i)}
			b, _ := dd.Encode()
			for d := 0; d < nn; d++ {
				if d != i {
					_ = trs[i].Send(d, b)
				}
			}
		}
		for i := 0; i < nn; i++ {
			for {
				p, ok := trs[i].Recv()
				if !ok {
					break
				}
				dd, err := DecodeDoomdata(p.Data)
				if err != nil {
					continue
				}
				if dd.Flags&NCMD_SETUP != 0 {
					seen[i][int(dd.Player)] = true
				}
			}
		}
		if handshakeConverged(seen, nn) {
			dcs := make([]*Doomcom, nn)
			for i := 0; i < nn; i++ {
				dcs[i] = &Doomcom{
					NumNodes:      nn,
					NumPlayers:    nn,
					ConsolePlayer: i,
					TicDup:        cfg.TicDup,
					ExtraTics:     cfg.ExtraTics,
					DeathMatch:    cfg.DeathMatch,
				}
			}
			return dcs, nil
		}
	}
	return nil, ErrHandshake
}

func handshakeConverged(seen []map[int]bool, nn int) bool {
	for i := 0; i < nn; i++ {
		if len(seen[i]) < nn {
			return false
		}
	}
	return true
}
