package main

import (
	"strconv"
	"testing"
	"time"
)

func fixedClock(start time.Time) (*blockLog, *time.Time) {
	now := start
	b := newBlockLog()
	b.now = func() time.Time { return now }
	return b, &now
}

// A respawning process is killed once a second forever. One row per kill would
// be 3,600 rows an hour per process per machine; the log has to carry a count
// instead.
func TestRepeatedKillsBecomeOneEntryWithACount(t *testing.T) {
	b, now := fixedClock(time.Date(2026, 9, 9, 14, 0, 0, 0, time.UTC))

	for i := 0; i < 47; i++ {
		b.record("steam.exe", "blacklist")
		*now = now.Add(time.Second)
	}

	_, entries := b.stage()
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1: %+v", len(entries), entries)
	}
	e := entries[0]
	if e.Count != 47 {
		t.Fatalf("count %d, want 47", e.Count)
	}
	if !e.FirstAt.Equal(time.Date(2026, 9, 9, 14, 0, 0, 0, time.UTC)) {
		t.Fatalf("first_at %s", e.FirstAt)
	}
	if !e.LastAt.Equal(time.Date(2026, 9, 9, 14, 0, 46, 0, time.UTC)) {
		t.Fatalf("last_at %s", e.LastAt)
	}
}

// Different processes, and the same process killed for different reasons, are
// different rows. "Why was this killed?" is the question the log answers.
func TestEntriesAreKeyedByProcessAndReason(t *testing.T) {
	b, _ := fixedClock(time.Now())
	b.record("steam.exe", "blacklist")
	b.record("steam.exe", "whitelist")
	b.record("discord.exe", "blacklist")

	_, entries := b.stage()
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3: %+v", len(entries), entries)
	}
}

// While the agent is offline nothing flushes. Without a cap a machine off the
// network for a week arrives with one row claiming 600,000 kills across seven
// days, which is true and useless.
func TestAnEntrySpanIsCappedAtAnHour(t *testing.T) {
	b, now := fixedClock(time.Date(2026, 9, 9, 14, 0, 0, 0, time.UTC))

	b.record("steam.exe", "blacklist")
	*now = now.Add(59 * time.Minute)
	b.record("steam.exe", "blacklist")
	*now = now.Add(2 * time.Minute) // 61 minutes after the first
	b.record("steam.exe", "blacklist")

	_, entries := b.stage()
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2 (the hour rolled over): %+v", len(entries), entries)
	}
	if entries[0].Count != 2 || entries[1].Count != 1 {
		t.Fatalf("counts %d and %d, want 2 and 1", entries[0].Count, entries[1].Count)
	}
}

// The buffer is bounded, the oldest go first (the recent past is what gets
// looked at), and the loss is itself reported rather than silent.
func TestOverflowDropsTheOldestAndReportsIt(t *testing.T) {
	b, now := fixedClock(time.Date(2026, 9, 9, 14, 0, 0, 0, time.UTC))

	// Each process is its own key, and each is pushed out of its hour so it
	// closes, so this creates maxPendingEntries+10 closed entries.
	for i := 0; i < maxPendingEntries+10; i++ {
		b.record(processName(i), "blacklist")
		*now = now.Add(2 * time.Hour)
	}

	// stage() hands over at most maxBatchEntries at a time, so the buffer is
	// drained across several batches -- which is also what a real agent does
	// after a long outage.
	var entries []blockedEntry
	for i := 0; ; i++ {
		if i > 10 {
			t.Fatal("draining did not terminate")
		}
		id, batch := b.stage()
		if id == "" {
			break
		}
		if len(batch) > maxBatchEntries {
			t.Fatalf("a batch carried %d entries, over the cap of %d", len(batch), maxBatchEntries)
		}
		entries = append(entries, batch...)
		b.ack(id)
	}
	if len(entries) != maxPendingEntries+1 {
		t.Fatalf("drained %d entries, want %d plus one overflow marker",
			len(entries), maxPendingEntries)
	}

	var marker *blockedEntry
	for i := range entries {
		if entries[i].Reason == "overflow" {
			marker = &entries[i]
		}
	}
	if marker == nil {
		t.Fatal("the buffer dropped entries and said nothing")
	}
	if marker.Process != "guardian.log_overflow" {
		t.Fatalf("marker process %q", marker.Process)
	}
	if marker.Count != 10 {
		t.Fatalf("marker count %d, want 10", marker.Count)
	}

	// The survivors are the newest, not the oldest. This is the assertion that
	// map iteration order would make a coin flip: the ten that go must be the
	// ten oldest by clock, not the ten Go happened to hand over first.
	for _, e := range entries {
		for i := 0; i < 10; i++ {
			if e.Process == processName(i) {
				t.Fatalf("%s survived; the ten oldest should have been dropped", e.Process)
			}
		}
	}
}

// A sync whose response is lost is retried, and the retry must be the same
// batch under the same id -- otherwise the server, which has already committed
// it, counts it twice.
func TestStageIsStableUntilAcknowledged(t *testing.T) {
	b, _ := fixedClock(time.Now())
	b.record("steam.exe", "blacklist")

	id1, first := b.stage()
	if id1 == "" {
		t.Fatal("stage returned no batch id")
	}
	id2, second := b.stage()
	if id1 != id2 {
		t.Fatalf("batch id changed on retry: %q then %q", id1, id2)
	}
	if len(first) != len(second) || first[0].Count != second[0].Count {
		t.Fatalf("the retry carried a different batch: %+v vs %+v", first, second)
	}

	// Kills recorded while the batch is in flight belong to the next one.
	b.record("discord.exe", "blacklist")
	b.ack(id1)
	id3, third := b.stage()
	if id3 == id1 {
		t.Fatal("the batch id was reused after acknowledgement")
	}
	if len(third) != 1 || third[0].Process != "discord.exe" {
		t.Fatalf("after the ack the next batch is %+v, want only discord.exe", third)
	}
}

// Acknowledging a batch that is not the one in flight must not throw away
// entries the server never saw.
func TestAckOfAStaleBatchIsIgnored(t *testing.T) {
	b, _ := fixedClock(time.Now())
	b.record("steam.exe", "blacklist")
	id, _ := b.stage()

	b.ack("some-other-batch")
	sameID, entries := b.stage()
	if sameID != id || len(entries) != 1 {
		t.Fatalf("a stale ack disturbed the batch in flight: %q, %+v", sameID, entries)
	}
}

// Nothing to report is nothing to send: an empty batch must not travel and
// must not consume a batch id.
func TestStageOfAnEmptyLogReturnsNothing(t *testing.T) {
	b, _ := fixedClock(time.Now())
	id, entries := b.stage()
	if id != "" || len(entries) != 0 {
		t.Fatalf("empty log staged %q with %d entries", id, len(entries))
	}
}

// A batch is ordered oldest first, whatever order the entries were closed in,
// so two people reading the same two syncs see the same thing.
func TestABatchIsOrderedOldestFirst(t *testing.T) {
	b, now := fixedClock(time.Date(2026, 9, 9, 14, 0, 0, 0, time.UTC))
	for i := 0; i < 20; i++ {
		b.record(processName(i), "blacklist")
		*now = now.Add(time.Minute)
	}

	_, entries := b.stage()
	if len(entries) != 20 {
		t.Fatalf("got %d entries, want 20", len(entries))
	}
	for i := 1; i < len(entries); i++ {
		if entries[i].FirstAt.Before(entries[i-1].FirstAt) {
			t.Fatalf("entry %d (%s) is older than the one before it (%s)",
				i, entries[i].FirstAt, entries[i-1].FirstAt)
		}
	}
}

func processName(i int) string {
	return "proc-" + strconv.Itoa(i) + ".exe"
}
