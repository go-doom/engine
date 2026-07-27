// SPDX-License-Identifier: GPL-2.0-or-later
// Go port of chocolate-doom i_oplmusic.c (Simon Howard et al.).
// Ported for the go-doom/engine authors.

package oplplayer

// callback kinds queued in the scheduler.
const (
	cbTrackTimer = iota
	cbRestartSong
)

// maxOPLQueue mirrors MAX_OPL_QUEUE in opl_queue.c.
const maxOPLQueue = 64

// queueEntry is one scheduled callback.
type queueEntry struct {
	kind  int
	track int
	time  uint64
}

// callbackQueue is a binary min-heap of pending callbacks, a faithful port of
// opl_queue.c so that callback ordering (including ties) matches the reference
// exactly.
type callbackQueue struct {
	entries    [maxOPLQueue]queueEntry
	numEntries int
}

func newQueue() *callbackQueue { return &callbackQueue{} }

func (q *callbackQueue) isEmpty() bool { return q.numEntries == 0 }

func (q *callbackQueue) clear() { q.numEntries = 0 }

// push inserts a callback at the given absolute time (OPL_Queue_Push).
func (q *callbackQueue) push(e queueEntry) {
	if q.numEntries >= maxOPLQueue {
		// OPL_Queue_Push drops on overflow.
		return
	}

	entryID := q.numEntries
	q.numEntries++

	for entryID > 0 {
		parentID := (entryID - 1) / 2
		if e.time >= q.entries[parentID].time {
			break
		}
		q.entries[entryID] = q.entries[parentID]
		entryID = parentID
	}

	q.entries[entryID] = e
}

// pop removes and returns the earliest callback (OPL_Queue_Pop). It must not be
// called on an empty queue.
func (q *callbackQueue) pop() queueEntry {
	result := q.entries[0]

	q.numEntries--
	entry := q.entries[q.numEntries]

	i := 0
	for {
		child1 := i*2 + 1
		child2 := i*2 + 2

		var nextI int
		if child1 < q.numEntries && q.entries[child1].time < entry.time {
			if child2 < q.numEntries && q.entries[child2].time < q.entries[child1].time {
				nextI = child2
			} else {
				nextI = child1
			}
		} else if child2 < q.numEntries && q.entries[child2].time < entry.time {
			nextI = child2
		} else {
			break
		}

		q.entries[i] = q.entries[nextI]
		i = nextI
	}

	q.entries[i] = entry
	return result
}

// peek returns the time of the earliest callback (OPL_Queue_Peek).
func (q *callbackQueue) peek() uint64 {
	if q.numEntries > 0 {
		return q.entries[0].time
	}
	return 0
}

// adjustCallbacks rescales all queued callback times relative to now by the
// given factor (OPL_Queue_AdjustCallbacks). The float32 arithmetic matches the
// reference C exactly.
func (q *callbackQueue) adjustCallbacks(now uint64, factor float32) {
	for i := 0; i < q.numEntries; i++ {
		offset := int64(q.entries[i].time - now)
		q.entries[i].time = now + uint64(float32(offset)/factor)
	}
}
