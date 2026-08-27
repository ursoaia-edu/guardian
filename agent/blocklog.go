package main

import (
	"sort"
	"sync"
	"time"
)

const (
	// maxPendingEntries bounds what one machine can hold while it cannot
	// reach the server. Past it the OLDEST are dropped: the recent past is
	// what somebody looks at, and a machine offline for a week must not grow
	// its own memory without limit.
	maxPendingEntries = 500

	// maxEntrySpan caps how long one aggregated entry may cover. Only reached
	// while the agent is offline, since a successful sync closes everything.
	maxEntrySpan = time.Hour

	// maxBatchEntries is what one sync carries. The server drops anything past
	// its own limit of 200, so sending more would lose data silently; the
	// remainder stays pending and goes with the next sync.
	maxBatchEntries = 200

	overflowProcess = "guardian.log_overflow"
	overflowReason  = "overflow"
)

// blockedEntry is one aggregated run of kills of a single process for a single
// reason. The JSON tags are the wire format the server parses.
type blockedEntry struct {
	Process string    `json:"process"`
	Reason  string    `json:"reason"`
	Count   int       `json:"count"`
	FirstAt time.Time `json:"first_at"`
	LastAt  time.Time `json:"last_at"`
}

// blockLog aggregates kills in memory between syncs. It is written from the
// enforce loop and read from the sync loop, so every method takes the lock.
type blockLog struct {
	mu sync.Mutex

	// open holds the entry currently accumulating for each (process, reason).
	open map[string]*blockedEntry
	// pending holds closed entries waiting for a sync.
	pending []blockedEntry
	// dropped counts what overflow threw away since the last report.
	dropped int

	// staged is the batch handed to the sync loop and not yet acknowledged.
	// It is returned verbatim on every stage() until ack(), so a retried sync
	// is the same batch under the same id and the server can ignore it.
	stagedID      string
	stagedEntries []blockedEntry

	now func() time.Time
}

func newBlockLog() *blockLog {
	return &blockLog{open: map[string]*blockedEntry{}, now: time.Now}
}

func (b *blockLog) record(process, reason string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.now()
	key := process + "\x00" + reason
	if e, ok := b.open[key]; ok {
		if now.Sub(e.FirstAt) < maxEntrySpan {
			e.Count++
			e.LastAt = now
			return
		}
		b.closeEntry(key, e)
	}
	b.open[key] = &blockedEntry{
		Process: process, Reason: reason, Count: 1, FirstAt: now, LastAt: now,
	}
}

// closeEntry moves an entry out of open and into pending, dropping the oldest
// pending entry when the buffer is full.
func (b *blockLog) closeEntry(key string, e *blockedEntry) {
	delete(b.open, key)
	if len(b.pending) >= maxPendingEntries {
		b.pending = b.pending[1:]
		b.dropped++
	}
	b.pending = append(b.pending, *e)
}

// closeAllOpen moves every open entry into pending, oldest first.
//
// The order matters and is not free: ranging over b.open would hand the
// entries over in Go's randomised map order, and since closeEntry drops
// pending[0] when the buffer is full, the entries thrown away would be ten
// arbitrary ones rather than the ten oldest. "The oldest are dropped" is the
// contract this buffer advertises, and on the sync after a long outage —
// exactly when overflow happens — the mass close below is where it is decided.
func (b *blockLog) closeAllOpen() {
	keys := make([]string, 0, len(b.open))
	for key := range b.open {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, z := b.open[keys[i]], b.open[keys[j]]
		if a.FirstAt.Equal(z.FirstAt) {
			return keys[i] < keys[j]
		}
		return a.FirstAt.Before(z.FirstAt)
	})
	for _, key := range keys {
		b.closeEntry(key, b.open[key])
	}
}

// stage returns the batch to send. It is idempotent until ack: the same id and
// the same entries come back on every call, so a sync retried after a lost
// response cannot be counted twice by the server.
func (b *blockLog) stage() (string, []blockedEntry) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.stagedID != "" {
		return b.stagedID, b.stagedEntries
	}

	b.closeAllOpen()
	if b.dropped > 0 {
		now := b.now()
		b.pending = append(b.pending, blockedEntry{
			Process: overflowProcess, Reason: overflowReason,
			Count: b.dropped, FirstAt: now, LastAt: now,
		})
		b.dropped = 0
	}
	if len(b.pending) == 0 {
		return "", nil
	}

	// Deterministic on the wire, so a test and a human reading two syncs see
	// the same thing twice.
	sort.SliceStable(b.pending, func(i, j int) bool {
		return b.pending[i].FirstAt.Before(b.pending[j].FirstAt)
	})

	n := len(b.pending)
	if n > maxBatchEntries {
		n = maxBatchEntries
	}
	b.stagedID = randomGUID()
	// Copied, not resliced: the remainder keeps appending into the same
	// backing array, and a staged batch that mutates underneath the sync
	// goroutine is the kind of bug that only shows up under load.
	b.stagedEntries = append([]blockedEntry(nil), b.pending[:n]...)
	b.pending = append([]blockedEntry(nil), b.pending[n:]...)
	return b.stagedID, b.stagedEntries
}

// ack discards the batch the server confirmed. An id that is not the one in
// flight is ignored: it can only be a late duplicate, and acting on it would
// throw away entries the server never saw.
func (b *blockLog) ack(batchID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if batchID == "" || batchID != b.stagedID {
		return
	}
	b.stagedID = ""
	b.stagedEntries = nil
}
