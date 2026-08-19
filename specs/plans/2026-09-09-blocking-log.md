# Blocking Log Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the cabinet able to answer "what did this policy actually kill, on which machine, when" — the agent reports every process it kills, the server stores it, and two paged endpoints serve it to the room and computer screens.

**Architecture:** The agent aggregates kills in memory keyed by `(process, reason)` and ships them as a batch inside the sync body it already posts. The server writes them to a dedicated `process_events` table — separate from the `events` audit feed, with its own 30-day retention — and serves them through cursor-paged endpoints gated by the existing room-visibility check. Idempotency comes from a batch id the agent keeps until the server acknowledges it.

**Tech Stack:** Go 1.25, `go-chi/chi` v5, `jackc/pgx/v5`, `sqlc` (pgx/v5 driver), `pressly/goose/v3` migrations, PostgreSQL 16 with row-level security.

**Spec:** `specs/2026-09-09-cabinet-v1-design.md` — §1 *Blocking log*. This plan implements step 1 of that document's **Implementation order**; steps 2–9 are separate plans.

## Global Constraints

- **Telemetry must never fail a sync.** A malformed batch, a bad `reason`, an oversized field, a duplicate `batch_id` — every one of these drops data and still returns `200`. A machine's policy must never depend on the log it is shipping.
- **Ordering is by the server's `created_at`, never the agent's timestamps.** `first_at`/`last_at` are stored and displayed but never sort anything, and are clamped into `[now-24h, now+5min]`.
- **Every new account-scoped endpoint gets a row in `server/isolation_test.go`.** A route absent from that table is a route nobody proved is isolated.
- **Every new route under `/api/v1` gets classified in `server/authz_test.go`**, which walks the router and fails the build on an unclassified route.
- **Every account-scoped table carries RLS** keyed on `current_account_id()`, with `account_id` a real column on the row (no joins — an RLS policy tests the row it is on).
- **Reads of account-scoped tables in tests must be scoped**, or use `observe(t)` (a pool on the owner role, not subject to RLS) when the point is to observe the database independently of the app's scoping.
- **Server tests need a live Postgres.** `docker compose -f server/docker-compose.dev.yml up -d`, then `TEST_DATABASE_URL` and `TEST_APP_DATABASE_URL`. Agent tests need neither Postgres nor Windows.
- **Commit message format:** this repo's history uses Conventional Commits (`feat(server): …`, `fix(agent): …`). No Claude attribution, no session trailer.
- **Docs are updated in the same commit as the code:** `specs/api.md`, `specs/server.md`, `specs/agent.md`, and `CLAUDE.md` where the change touches what they describe.

### Two corrections to the spec, decided while planning

Both are small, both make the spec's own intent work, and both are applied to `specs/2026-09-09-cabinet-v1-design.md` in Task 1's commit.

1. **`reason` takes a fourth value, `overflow`.** The spec asks for a `guardian.log_overflow` marker entry when the agent's buffer overflows, but the marker cannot be stored while `reason` is constrained to three values — the server would drop the very row that says data was lost.
2. **`batch_id` is `TEXT`, not `UUID`.** The agent module depends on `golang.org/x/sys` and nothing else; pulling in `google/uuid` to generate one identifier is not worth it, and the agent's existing `randomGUID()` (`agent/client.go:132`) already produces a non-UUID random string. The server treats it as an opaque token, capped at 64 characters.

Also settled here, and consistent with the spec rather than a change to it: **the agent never sends `reason: "locked"`.** It cannot tell a lock from an empty whitelist — both arrive as `mode: "whitelist"` with no applications, which is exactly what the spec's wire design intends. The server rewrites `whitelist` to `locked` when the reporting machine has `blocked = true`, because only the server knows.

---

## File Structure

**Created:**

| File | Responsibility |
|---|---|
| `server/db/migrations/00017_process_events.sql` | the table, its indexes, its RLS policy, and `computers.last_event_batch` |
| `server/db/migrations/00018_process_event_retention.sql` | `purge_old_process_events(INTERVAL)`, `SECURITY DEFINER` |
| `server/db/queries/process_events.sql` | sqlc sources: one insert, two paged reads, one batch stamp |
| `server/handlers_process_events.go` | the two `GET` handlers, their filters and cursor |
| `server/process_events_test.go` | handler, filter, paging and isolation tests |
| `agent/blocklog.go` | the in-memory aggregation buffer — pure Go, no I/O |
| `agent/blocklog_test.go` | aggregation, the span cap, overflow, staging and acknowledgement |

**Modified:**

| File | Change |
|---|---|
| `server/handlers_agent.go:283-300` | `readRuntime` becomes `readSyncBody`; the batch is validated and written |
| `server/routes.go:139-152` | two routes added to the guest-reachable group |
| `server/maintenance.go:11-20,39-63` | the 30-day purge joins the hourly loop |
| `server/isolation_test.go` | two rows |
| `server/authz_test.go` | two entries in `guestReachableRoutes` |
| `server/fakeagent/` | sends a batch, so the loop is exercisable without Windows |
| `agent/agent.go:114-136,284-300,399-437` | the buffer on the struct, kills recorded, batch staged and acknowledged |
| `agent/client.go:206-223` | `sync` carries the batch |
| `agent/agent_test.go:42-84` | `fakeServer` records what arrived |

---

## Task 1: The table

**Files:**
- Create: `server/db/migrations/00017_process_events.sql`
- Create: `server/db/queries/process_events.sql`
- Create: `server/process_events_test.go`
- Modify: `specs/2026-09-09-cabinet-v1-design.md` (the two corrections above), `specs/server.md`

**Interfaces:**
- Consumes: `current_account_id()` (migration `00003_rls.sql`), `testPool(t)`, `observe(t)`, `registerAndLogin`, `mintBindingToken`, `enroll` (all in `server/*_test.go`).
- Produces: table `process_events`; column `computers.last_event_batch TEXT`; generated `db.RecordProcessEvent`, `db.SetLastEventBatch`, `db.ListRoomProcessEvents`, `db.ListComputerProcessEvents`, and the regenerated `db.Computer` with a `LastEventBatch *string` field.

- [ ] **Step 1: Write the failing test**

Create `server/process_events_test.go`:

```go
package main

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The kill log is account-scoped like every other table here: a row written
// under account A must be unreadable inside account B's scope. This is the
// RLS policy itself, tested below the HTTP layer — the handler tests come
// later and would pass against a table with no policy at all.
func TestProcessEventsAreScopedToTheirAccount(t *testing.T) {
	s := &Server{pool: testPool(t)}
	ctx := context.Background()

	ca := registerAndLogin(t, s, "a@example.com")
	enroll(t, s, mintBindingToken(t, s, ca), "guid-a", "PC-A")
	accountA := accountIDOf(t, s, "a@example.com")
	registerAndLogin(t, s, "b@example.com")
	accountB := accountIDOf(t, s, "b@example.com")

	var computerA uuid.UUID
	if err := s.inAccount(ctx, accountA, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id FROM computers LIMIT 1`).Scan(&computerA)
	}); err != nil {
		t.Fatalf("read account A's computer: %v", err)
	}

	if err := s.inAccount(ctx, accountA, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO process_events
			  (account_id, computer_id, process, reason, count, first_at, last_at)
			VALUES ($1, $2, 'steam.exe', 'blacklist', 3, now(), now())`,
			accountA, computerA)
		return err
	}); err != nil {
		t.Fatalf("insert in account A: %v", err)
	}

	var visibleToB int
	if err := s.inAccount(ctx, accountB, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM process_events`).Scan(&visibleToB)
	}); err != nil {
		t.Fatalf("count in account B: %v", err)
	}
	if visibleToB != 0 {
		t.Fatalf("account B sees %d of account A's kill rows", visibleToB)
	}

	var total int
	if err := observe(t).QueryRow(ctx, `SELECT count(*) FROM process_events`).Scan(&total); err != nil {
		t.Fatalf("observe: %v", err)
	}
	if total != 1 {
		t.Fatalf("the row is not in the database at all (%d rows); the test above proved nothing", total)
	}
}

// 'overflow' is the marker the agent sends when its buffer dropped entries.
// Without it in the CHECK the server would reject the one row that says data
// was lost. See the plan's "corrections to the spec".
func TestOverflowIsAPermittedReason(t *testing.T) {
	s := &Server{pool: testPool(t)}
	ctx := context.Background()

	ca := registerAndLogin(t, s, "a@example.com")
	enroll(t, s, mintBindingToken(t, s, ca), "guid-a", "PC-A")
	accountA := accountIDOf(t, s, "a@example.com")

	var computerA uuid.UUID
	if err := s.inAccount(ctx, accountA, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id FROM computers LIMIT 1`).Scan(&computerA)
	}); err != nil {
		t.Fatalf("read computer: %v", err)
	}

	if err := s.inAccount(ctx, accountA, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO process_events
			  (account_id, computer_id, process, reason, count, first_at, last_at)
			VALUES ($1, $2, 'guardian.log_overflow', 'overflow', 12, now(), now())`,
			accountA, computerA)
		return err
	}); err != nil {
		t.Fatalf("insert an overflow marker: %v", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd server && go test ./... -run 'TestProcessEventsAreScopedToTheirAccount|TestOverflowIsAPermittedReason' -v`

Expected: FAIL — `ERROR: relation "process_events" does not exist (SQLSTATE 42P01)`.

- [ ] **Step 3: Write the migration**

Create `server/db/migrations/00017_process_events.sql`:

```sql
-- +goose Up
-- The blocking log. Deliberately NOT rows in `events`: that table records
-- deliberate human acts, is read as a narrative and is kept 180 days. This one
-- is machine output — a misconfigured whitelist on one classroom produces
-- thousands of rows an hour — and it gets its own retention, its own endpoint
-- and its own tab. Sharing a table would drown the audit feed in noise and
-- force one retention policy onto two kinds of data with different value.
CREATE TABLE process_events (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id  UUID NOT NULL REFERENCES accounts(id)  ON DELETE CASCADE,
    computer_id UUID NOT NULL REFERENCES computers(id) ON DELETE CASCADE,
    -- The room the machine was in AT THE MOMENT OF THE KILL, not the one it is
    -- in now. Moving a machine between rooms must not rewrite its history, and
    -- the room tab's query must not join through a column that has since
    -- changed.
    room_id     UUID          REFERENCES rooms(id)     ON DELETE SET NULL,
    process     TEXT NOT NULL,
    -- Why this was killed, which is the question the log exists to answer.
    -- 'locked' is written by the server, never sent by an agent: an agent
    -- cannot tell a locked machine from a whitelist that allows nothing, since
    -- both arrive on the wire as "whitelist, empty list".
    -- 'overflow' marks entries the agent had to drop; see agent/blocklog.go.
    reason      TEXT NOT NULL CHECK (reason IN ('blacklist', 'whitelist', 'locked', 'overflow')),
    count       INTEGER NOT NULL DEFAULT 1 CHECK (count > 0),
    first_at    TIMESTAMPTZ NOT NULL,
    last_at     TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_process_events_account_time  ON process_events(account_id, created_at DESC);
CREATE INDEX idx_process_events_room_time     ON process_events(room_id, created_at DESC);
CREATE INDEX idx_process_events_computer_time ON process_events(computer_id, created_at DESC);

ALTER TABLE process_events ENABLE ROW LEVEL SECURITY;
CREATE POLICY process_events_isolation ON process_events
    USING (account_id = current_account_id())
    WITH CHECK (account_id = current_account_id());

-- The last batch this machine's agent shipped. A sync whose response is lost
-- is retried, and the server has already committed the batch; without this
-- stamp the log double-counts, and a log that inflates its own numbers is
-- worse than no log. TEXT rather than UUID because the agent module depends on
-- golang.org/x/sys and nothing else, and it is opaque to the server anyway.
ALTER TABLE computers ADD COLUMN last_event_batch TEXT;

-- +goose Down
ALTER TABLE computers DROP COLUMN last_event_batch;
DROP TABLE process_events;
```

- [ ] **Step 4: Write the sqlc queries**

Create `server/db/queries/process_events.sql`:

```sql
-- name: RecordProcessEvent :exec
INSERT INTO process_events
    (account_id, computer_id, room_id, process, reason, count, first_at, last_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: SetLastEventBatch :exec
-- Stamped in the same transaction as the batch it acknowledges, so a partially
-- recorded batch cannot exist.
UPDATE computers SET last_event_batch = $2 WHERE id = $1;

-- name: ListRoomProcessEvents :many
-- One query rather than a family of them: five optional filters would
-- otherwise be thirty-two statements. Each filter is inert when its argument
-- is NULL. The cursor is the same (created_at, id) row comparison the events
-- feed uses -- keyed on the pair rather than an offset, because rows arrive
-- while somebody is paging and OFFSET would silently repeat or skip them.
SELECT pe.id, pe.computer_id, pe.room_id, pe.process, pe.reason, pe.count,
       pe.first_at, pe.last_at, pe.created_at,
       COALESCE(NULLIF(c.display_name, ''), c.hostname)::text AS computer_name
FROM process_events pe
JOIN computers c ON c.id = pe.computer_id
WHERE pe.room_id = sqlc.arg(room_id)::uuid
  AND (sqlc.narg(process)::text     IS NULL OR pe.process     = sqlc.narg(process)::text)
  AND (sqlc.narg(reason)::text      IS NULL OR pe.reason      = sqlc.narg(reason)::text)
  AND (sqlc.narg(computer)::uuid    IS NULL OR pe.computer_id = sqlc.narg(computer)::uuid)
  AND (sqlc.narg(since)::timestamptz IS NULL OR pe.created_at >= sqlc.narg(since)::timestamptz)
  AND (sqlc.narg(until)::timestamptz IS NULL OR pe.created_at <= sqlc.narg(until)::timestamptz)
  AND (sqlc.narg(before_time)::timestamptz IS NULL
       OR (pe.created_at, pe.id) < (sqlc.narg(before_time)::timestamptz, sqlc.narg(before_id)::uuid))
ORDER BY pe.created_at DESC, pe.id DESC
LIMIT sqlc.arg(row_limit);

-- name: ListComputerProcessEvents :many
-- Its own statement rather than the room query with a computer filter: a
-- machine in no room has room_id NULL on every row, and the room query can
-- never match it.
SELECT pe.id, pe.computer_id, pe.room_id, pe.process, pe.reason, pe.count,
       pe.first_at, pe.last_at, pe.created_at,
       COALESCE(NULLIF(c.display_name, ''), c.hostname)::text AS computer_name
FROM process_events pe
JOIN computers c ON c.id = pe.computer_id
WHERE pe.computer_id = sqlc.arg(computer_id)::uuid
  AND (sqlc.narg(process)::text     IS NULL OR pe.process = sqlc.narg(process)::text)
  AND (sqlc.narg(reason)::text      IS NULL OR pe.reason  = sqlc.narg(reason)::text)
  AND (sqlc.narg(since)::timestamptz IS NULL OR pe.created_at >= sqlc.narg(since)::timestamptz)
  AND (sqlc.narg(until)::timestamptz IS NULL OR pe.created_at <= sqlc.narg(until)::timestamptz)
  AND (sqlc.narg(before_time)::timestamptz IS NULL
       OR (pe.created_at, pe.id) < (sqlc.narg(before_time)::timestamptz, sqlc.narg(before_id)::uuid))
ORDER BY pe.created_at DESC, pe.id DESC
LIMIT sqlc.arg(row_limit);
```

- [ ] **Step 5: Regenerate the sqlc code**

Run: `cd server && sqlc generate`

Expected: `internal/db/` is rewritten; `git status` shows `internal/db/models.go` (a `LastEventBatch *string` field on `Computer`) and a new `internal/db/process_events.sql.go`. CI runs `sqlc diff` and fails if the committed output does not match the queries, so this step is not optional.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `cd server && go test ./... -run 'TestProcessEventsAreScopedToTheirAccount|TestOverflowIsAPermittedReason' -v`

Expected: PASS, both.

- [ ] **Step 7: Run the whole suite**

Run: `cd server && gofmt -l . && go vet ./... && go test ./...`

Expected: `gofmt -l` prints nothing, vet is silent, all tests pass. A new nullable column on `computers` changes the generated struct, so this catches anything that constructs a `db.Computer` positionally.

- [ ] **Step 8: Update the docs**

In `specs/2026-09-09-cabinet-v1-design.md` §1, apply the two corrections from this plan's header: the `reason` CHECK gains `'overflow'`, and `computers.last_event_batch` is `TEXT` rather than `UUID`. Add one sentence after the `reason` paragraph: *"An agent never sends `locked`: it cannot tell a lock from an empty whitelist, since both arrive as `mode: whitelist` with no applications. The server rewrites `whitelist` to `locked` when the reporting machine is blocked."*

In `specs/server.md`, add `process_events` to the table list and to the RLS section, following the shape of the existing `events` entry.

- [ ] **Step 9: Commit**

```bash
git add server/db/migrations/00017_process_events.sql \
        server/db/queries/process_events.sql \
        server/internal/db server/process_events_test.go \
        specs/2026-09-09-cabinet-v1-design.md specs/server.md
git commit -m "feat(server): add the process_events table and its RLS policy"
```

---

## Task 2: The agent's aggregation buffer

Pure Go, no I/O, no server, no Windows. It is the piece with the most behaviour and the least infrastructure, so it is worth having alone and green before anything is wired to it.

**Files:**
- Create: `agent/blocklog.go`
- Create: `agent/blocklog_test.go`

**Interfaces:**
- Consumes: `randomGUID()` from `agent/client.go:132`.
- Produces:
  - `type blockedEntry struct { Process string; Reason string; Count int; FirstAt time.Time; LastAt time.Time }` with JSON tags `process`, `reason`, `count`, `first_at`, `last_at`.
  - `func newBlockLog() *blockLog`
  - `func (b *blockLog) record(process, reason string)`
  - `func (b *blockLog) stage() (batchID string, entries []blockedEntry)` — returns the same batch on every call until `ack`.
  - `func (b *blockLog) ack(batchID string)`
  - `b.now func() time.Time` — a clock seam the tests replace.

- [ ] **Step 1: Write the failing tests**

Create `agent/blocklog_test.go`:

```go
package main

import (
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

	// The survivors are the newest, not the oldest.
	for _, e := range entries {
		if e.Process == processName(0) {
			t.Fatal("the oldest entry survived; the newest should have")
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

func processName(i int) string {
	return "proc-" + itoa(i) + ".exe"
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd agent && go test ./... -run 'TestRepeatedKills|TestEntriesAreKeyed|TestAnEntrySpan|TestOverflow|TestStageIs|TestAckOfAStale|TestStageOfAnEmpty' -v`

Expected: FAIL to compile — `undefined: newBlockLog`, `undefined: maxPendingEntries`, `undefined: itoa`.

- [ ] **Step 3: Write the implementation**

Create `agent/blocklog.go`:

```go
package main

import (
	"sort"
	"strconv"
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
		b.close(key, e)
	}
	b.open[key] = &blockedEntry{
		Process: process, Reason: reason, Count: 1, FirstAt: now, LastAt: now,
	}
}

// close moves an entry out of open and into pending, dropping the oldest
// pending entry when the buffer is full.
func (b *blockLog) close(key string, e *blockedEntry) {
	delete(b.open, key)
	if len(b.pending) >= maxPendingEntries {
		b.pending = b.pending[1:]
		b.dropped++
	}
	b.pending = append(b.pending, *e)
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

	for key, e := range b.open {
		b.close(key, e)
	}
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

	// Map iteration above is unordered; the wire is deterministic so a test
	// and a human reading two syncs see the same thing twice.
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

func itoa(i int) string { return strconv.Itoa(i) }
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd agent && go test ./... -v`

Expected: PASS, including the pre-existing agent tests.

- [ ] **Step 5: Commit**

```bash
git add agent/blocklog.go agent/blocklog_test.go
git commit -m "feat(agent): aggregate killed processes into a bounded, resendable batch"
```

---

## Task 3: The agent ships the batch

**Files:**
- Modify: `agent/agent.go` — struct field, `enforce` records, `syncOnce` stages and acknowledges
- Modify: `agent/client.go:206-223` — `sync` carries the batch
- Modify: `agent/agent_test.go:42-84` — `fakeServer` records what arrived
- Test: `agent/agent_test.go`

**Interfaces:**
- Consumes: `newBlockLog`, `(*blockLog).record/stage/ack`, `blockedEntry` (Task 2).
- Produces: `(*apiClient).sync(agentToken string, runtime map[string]any, batchID string, blocked []blockedEntry) (*SyncResponse, error)` — the batch fields are omitted from the body when `batchID` is empty; `agent.blocks *blockLog`.

- [ ] **Step 1: Write the failing test**

Append to `agent/agent_test.go`. The helper follows the same setup the existing
first-run test uses (`agent/agent_test.go:106`): a temporary working directory,
an `.env`, then `step()`, which enrolls and returns 0 to mean "sync now".

```go
// enrolledTestAgent returns an agent that has already enrolled against a fake
// server, which is the starting point for everything about the batch.
func enrolledTestAgent(t *testing.T) (*agent, *fakeServer) {
	t.Helper()
	t.Chdir(t.TempDir())
	f := &fakeServer{enrollStatus: http.StatusCreated, syncStatus: http.StatusOK}
	ts := httptest.NewServer(f.handler())
	t.Cleanup(ts.Close)

	writeEnv(t, "SERVER_ADDRESS="+ts.URL+"\nBINDING_TOKEN=bt-secret\n")
	a, _ := newTestAgent(t, ts.URL)
	a.loadCredentials()
	if wait := a.step(); wait != 0 {
		t.Fatalf("enrollment did not succeed: wait=%s", wait)
	}
	return a, f
}

// The whole point of the feature, end to end on the agent's side: a kill is
// aggregated, shipped in the next sync body, and dropped once the server has
// taken it.
func TestKillsAreReportedOnTheNextSync(t *testing.T) {
	a, f := enrolledTestAgent(t)

	a.blocks.record("steam.exe", "blacklist")
	a.blocks.record("steam.exe", "blacklist")
	a.syncOnce()

	blocked, ok := f.lastSync["blocked"].([]any)
	if !ok || len(blocked) != 1 {
		t.Fatalf("sync body carried %#v, want one blocked entry", f.lastSync["blocked"])
	}
	entry := blocked[0].(map[string]any)
	if entry["process"] != "steam.exe" {
		t.Fatalf("process %v", entry["process"])
	}
	if entry["count"].(float64) != 2 {
		t.Fatalf("count %v, want 2", entry["count"])
	}
	if f.lastSync["batch_id"] == "" || f.lastSync["batch_id"] == nil {
		t.Fatal("the batch carried no id, so the server cannot deduplicate a retry")
	}

	// Acknowledged: the next sync carries nothing.
	a.syncOnce()
	if b, present := f.lastSync["blocked"]; present && b != nil && len(b.([]any)) != 0 {
		t.Fatalf("the acknowledged batch was sent again: %#v", b)
	}
}

// A sync that fails must not lose the batch, and the retry must be the same
// batch under the same id.
func TestAFailedSyncResendsTheSameBatch(t *testing.T) {
	a, f := enrolledTestAgent(t)

	a.blocks.record("steam.exe", "blacklist")
	f.syncStatus = http.StatusInternalServerError
	a.syncOnce()
	firstID := f.lastSync["batch_id"]

	f.syncStatus = http.StatusOK
	a.syncOnce()
	if f.lastSync["batch_id"] != firstID {
		t.Fatalf("retry used batch id %v, want the original %v", f.lastSync["batch_id"], firstID)
	}
	blocked := f.lastSync["blocked"].([]any)
	if len(blocked) != 1 {
		t.Fatalf("the retry carried %d entries, want 1", len(blocked))
	}
}

// Nothing killed, nothing sent: the body must stay the shape the pre-batch
// server understands.
func TestASyncWithNoKillsCarriesNoBatch(t *testing.T) {
	a, f := enrolledTestAgent(t)
	a.syncOnce()

	if _, present := f.lastSync["blocked"]; present {
		t.Fatalf("an empty batch was sent: %#v", f.lastSync)
	}
	if _, present := f.lastSync["batch_id"]; present {
		t.Fatal("an empty batch consumed a batch id")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd agent && go test ./... -run 'TestKillsAreReported|TestAFailedSyncResends|TestASyncWithNoKills' -v`

Expected: FAIL — `a.blocks undefined (type *agent has no field or method blocks)`.

- [ ] **Step 3: Add the field and construct it**

In `agent/agent.go`, add to the `agent` struct (after `state policyState`, around line 118):

```go
	// blocks aggregates what enforce killed, between syncs. Written by the
	// enforce goroutine, drained by the sync goroutine; it takes its own lock.
	blocks *blockLog
```

and in `newAgent`, alongside the other field initialisations:

```go
		blocks: newBlockLog(),
```

- [ ] **Step 4: Record every kill**

In `agent/agent.go`, `enforce` — the blacklist branch (around line 413):

```go
				if err := killProcess(app.Name); err == nil {
					a.blocks.record(app.Name, "blacklist")
					a.lg.Infof("Killed process: %s", app.Name)
				} else {
					a.lg.Errorf("Failed to kill process %s: %v", app.Name, err)
				}
```

and the whitelist branch (around line 431):

```go
				if err := killProcess(procName); err == nil {
					a.blocks.record(procName, "whitelist")
					a.lg.Infof("Killed non-whitelisted process: %s", procName)
				}
```

Only a successful kill is recorded. A failed one is a different event — the log answers "what was blocked", and a process the agent could not touch was not blocked.

The agent never records `"locked"`: it cannot tell a locked machine from a whitelist that allows nothing, since both arrive as `mode: "whitelist"` with an empty list. The server rewrites the reason, because only the server knows.

- [ ] **Step 5: Carry the batch in the client**

In `agent/client.go`, replace `sync` (line 206):

```go
func (c *apiClient) sync(agentToken string, runtime map[string]any, batchID string, blocked []blockedEntry) (*SyncResponse, error) {
	body := map[string]any{"runtime": runtime}
	// Omitted rather than sent empty: the body must stay byte-identical to
	// what the pre-batch server parses when there is nothing to report.
	if batchID != "" && len(blocked) > 0 {
		body["batch_id"] = batchID
		body["blocked"] = blocked
	}
	status, raw, err := c.post("/agent/sync", agentToken, body)
	if err != nil {
		return nil, err
	}
	switch status {
	case http.StatusOK:
		var resp SyncResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, fmt.Errorf("decode response: %w", err)
		}
		return &resp, nil
	case http.StatusUnauthorized:
		return nil, fmt.Errorf("%w: %s", errUnauthorized, serverMessage(raw))
	default:
		return nil, fmt.Errorf("server returned %d: %s", status, serverMessage(raw))
	}
}
```

- [ ] **Step 6: Stage and acknowledge in the sync loop**

In `agent/agent.go`, `syncOnce` (line 284), replace the first line and add the acknowledgement:

```go
func (a *agent) syncOnce() {
	batchID, blocked := a.blocks.stage()
	resp, err := a.client.sync(a.creds.AgentToken, a.runtimeTelemetry(), batchID, blocked)
	if err != nil {
		// The batch is deliberately NOT acknowledged here: it stays staged and
		// the next sync resends it under the same id, which is what lets the
		// server ignore a duplicate rather than count it twice.
		if errors.Is(err, errUnauthorized) {
```

...the existing error handling is unchanged...

and immediately after `a.state.set(resp)`:

```go
	a.state.set(resp)
	// The server has it. Anything recorded while the batch was in flight is
	// already accumulating for the next one.
	a.blocks.ack(batchID)
```

- [ ] **Step 7: Teach the fake server to record the batch**

`fakeServer.lastSync` is already a `map[string]any` decoded from the body, so `blocked` and `batch_id` arrive without any change. The one change needed is that a non-200 status must still decode the body, so `TestAFailedSyncResendsTheSameBatch` can read the id off the failed attempt. In `agent/agent_test.go`, the sync handler already decodes before branching on status — confirm the order is:

```go
	mux.HandleFunc("POST /agent/sync", func(w http.ResponseWriter, r *http.Request) {
		f.syncs++
		f.lastBearer = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		f.lastSync = map[string]any{}
		json.NewDecoder(r.Body).Decode(&f.lastSync)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.syncStatus)
```

It is. No change to the harness is required; this step is a verification, not an edit.

- [ ] **Step 8: Run the tests to verify they pass**

Run: `cd agent && gofmt -l . && go vet ./... && go test ./... -v`

Expected: `gofmt -l` prints nothing, all tests pass including the pre-existing enrollment and revocation tests.

- [ ] **Step 9: Update the docs**

In `specs/agent.md`, add a paragraph to the sync section: the agent aggregates successful kills by `(process, reason)`, holds at most 500 entries and drops the oldest with a `guardian.log_overflow` marker, ships at most 200 per sync inside the body as `blocked` with a `batch_id`, and keeps the batch staged until a `200` so a retry is the same batch under the same id. Note that pending entries live in memory only and are lost on restart — writing them to disk every second on every managed machine costs more than the last minute of a kill log is worth.

- [ ] **Step 10: Commit**

```bash
git add agent/agent.go agent/client.go agent/agent_test.go specs/agent.md
git commit -m "feat(agent): report killed processes in the sync body"
```

---

## Task 4: The server accepts the batch

**Files:**
- Modify: `server/handlers_agent.go:204-300` — `readRuntime` becomes `readSyncBody`; the batch is validated and written
- Test: `server/process_events_test.go`

**Interfaces:**
- Consumes: `db.RecordProcessEvent`, `db.SetLastEventBatch`, `db.Computer.LastEventBatch` (Task 1); `sanitizeText`, `sanitizeJSONObject`, `s.inAccount`, `computerFrom` (existing).
- Produces: nothing other tasks consume; `GET` handlers in Task 5 read the rows this writes.

- [ ] **Step 1: Write the failing tests**

Append to `server/process_events_test.go`:

```go
// syncWithBatch posts a sync body carrying a blocked batch and returns the
// status. Separate from agentSync, which posts telemetry only.
func syncWithBatch(t *testing.T, s *Server, agentToken, batchID string, blocked []map[string]any) int {
	t.Helper()
	body := map[string]any{"runtime": map[string]any{"uptime_s": 1}}
	if batchID != "" {
		body["batch_id"] = batchID
		body["blocked"] = blocked
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest("POST", "/agent/sync", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+agentToken)
	rr := httptest.NewRecorder()
	s.setupRoutes().ServeHTTP(rr, req)
	return rr.Code
}

func countProcessEvents(t *testing.T, s *Server, accountID uuid.UUID) int {
	t.Helper()
	var n int
	if err := s.inAccount(context.Background(), accountID, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT count(*) FROM process_events`).Scan(&n)
	}); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func TestSyncRecordsTheBlockedBatch(t *testing.T) {
	s := &Server{pool: testPool(t)}
	c := registerAndLogin(t, s, "parent@example.com")
	_, agentToken := enroll(t, s, mintBindingToken(t, s, c), "guid-1", "PC-1")
	account := accountIDOf(t, s, "parent@example.com")

	now := time.Now().UTC()
	code := syncWithBatch(t, s, agentToken, "batch-1", []map[string]any{{
		"process": "steam.exe", "reason": "blacklist", "count": 47,
		"first_at": now.Add(-time.Minute).Format(time.RFC3339),
		"last_at":  now.Format(time.RFC3339),
	}})
	if code != 200 {
		t.Fatalf("sync status %d", code)
	}
	if n := countProcessEvents(t, s, account); n != 1 {
		t.Fatalf("%d rows recorded, want 1", n)
	}
}

// The reason a batch id exists: a sync whose response was lost is retried, and
// the server has already committed the batch.
func TestARepeatedBatchIsIgnored(t *testing.T) {
	s := &Server{pool: testPool(t)}
	c := registerAndLogin(t, s, "parent@example.com")
	_, agentToken := enroll(t, s, mintBindingToken(t, s, c), "guid-1", "PC-1")
	account := accountIDOf(t, s, "parent@example.com")

	item := []map[string]any{{
		"process": "steam.exe", "reason": "blacklist", "count": 3,
		"first_at": time.Now().UTC().Format(time.RFC3339),
		"last_at":  time.Now().UTC().Format(time.RFC3339),
	}}
	syncWithBatch(t, s, agentToken, "batch-1", item)
	syncWithBatch(t, s, agentToken, "batch-1", item)

	if n := countProcessEvents(t, s, account); n != 1 {
		t.Fatalf("the duplicate batch was counted: %d rows, want 1", n)
	}
}

// Telemetry never fails a sync. Every one of these is malformed, and every one
// of them must still leave the machine with a policy.
func TestAMalformedBatchNeverFailsTheSync(t *testing.T) {
	s := &Server{pool: testPool(t)}
	c := registerAndLogin(t, s, "parent@example.com")
	_, agentToken := enroll(t, s, mintBindingToken(t, s, c), "guid-1", "PC-1")
	account := accountIDOf(t, s, "parent@example.com")

	cases := []struct {
		name string
		item map[string]any
	}{
		{"unknown reason", map[string]any{"process": "x.exe", "reason": "because", "count": 1}},
		{"the agent may not claim a lock", map[string]any{"process": "x.exe", "reason": "locked", "count": 1}},
		{"empty process", map[string]any{"process": "", "reason": "blacklist", "count": 1}},
		{"zero count", map[string]any{"process": "x.exe", "reason": "blacklist", "count": 0}},
		{"negative count", map[string]any{"process": "x.exe", "reason": "blacklist", "count": -5}},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			batch := "batch-" + strconv.Itoa(i)
			if code := syncWithBatch(t, s, agentToken, batch, []map[string]any{tc.item}); code != 200 {
				t.Fatalf("status %d, want 200", code)
			}
		})
	}
	if n := countProcessEvents(t, s, account); n != 0 {
		t.Fatalf("%d malformed rows were stored, want 0", n)
	}
}

// An agent's clock can be wrong by years. Its timestamps are stored and shown
// because they are what an operator wants to read, but they may not order the
// feed and they may not claim to be from 1999 or from next year.
func TestAbsurdAgentTimestampsAreClamped(t *testing.T) {
	s := &Server{pool: testPool(t)}
	c := registerAndLogin(t, s, "parent@example.com")
	_, agentToken := enroll(t, s, mintBindingToken(t, s, c), "guid-1", "PC-1")
	account := accountIDOf(t, s, "parent@example.com")

	syncWithBatch(t, s, agentToken, "batch-1", []map[string]any{{
		"process": "steam.exe", "reason": "blacklist", "count": 1,
		"first_at": "1999-01-01T00:00:00Z",
		"last_at":  "2099-01-01T00:00:00Z",
	}})

	ctx := context.Background()
	var firstAt, lastAt time.Time
	if err := s.inAccount(ctx, account, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT first_at, last_at FROM process_events`).Scan(&firstAt, &lastAt)
	}); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if firstAt.Year() < 2020 || lastAt.After(time.Now().Add(10*time.Minute)) {
		t.Fatalf("timestamps were not clamped: %s .. %s", firstAt, lastAt)
	}
}

// Only the server can tell a locked machine from a whitelist that allows
// nothing, so only the server may write that reason.
func TestKillsOnALockedMachineAreRecordedAsLocked(t *testing.T) {
	s := &Server{pool: testPool(t)}
	h := s.setupRoutes()
	c := registerAndLogin(t, s, "parent@example.com")
	_, agentToken := enroll(t, s, mintBindingToken(t, s, c), "guid-1", "PC-1")
	account := accountIDOf(t, s, "parent@example.com")

	var list struct {
		Computers []struct {
			ID string `json:"id"`
		} `json:"computers"`
	}
	decodeInto(t, doJSON(t, h, "GET", "/api/v1/computers", nil, c), &list)
	if rr := doJSON(t, h, "PATCH", "/api/v1/computers/"+list.Computers[0].ID,
		map[string]any{"blocked": true}, c); rr.Code != 200 {
		t.Fatalf("block: %d %s", rr.Code, rr.Body.String())
	}

	syncWithBatch(t, s, agentToken, "batch-1", []map[string]any{{
		"process": "notepad.exe", "reason": "whitelist", "count": 1,
		"first_at": time.Now().UTC().Format(time.RFC3339),
		"last_at":  time.Now().UTC().Format(time.RFC3339),
	}})

	ctx := context.Background()
	var reason string
	if err := s.inAccount(ctx, account, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT reason FROM process_events`).Scan(&reason)
	}); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if reason != "locked" {
		t.Fatalf("reason %q, want \"locked\"", reason)
	}
}
```

Add to that file's imports: `bytes`, `encoding/json`, `net/http/httptest`, `strconv`, `time`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd server && go test ./... -run 'TestSyncRecordsTheBlockedBatch|TestARepeatedBatch|TestAMalformedBatch|TestAbsurdAgentTimestamps|TestKillsOnALockedMachine' -v`

Expected: FAIL — `TestSyncRecordsTheBlockedBatch` reports 0 rows recorded; the malformed-batch test passes vacuously, which is why the others exist.

- [ ] **Step 3: Replace `readRuntime` with a single body read**

In `server/handlers_agent.go`, replace `readRuntime` (line 283) with:

```go
// agentSyncBody is everything an agent posts on a sync. It is decoded once:
// the body is a stream, and a second read would find it empty.
type agentSyncBody struct {
	Runtime json.RawMessage `json:"runtime"`
	BatchID string          `json:"batch_id"`
	Blocked []blockedItem   `json:"blocked"`
}

type blockedItem struct {
	Process string    `json:"process"`
	Reason  string    `json:"reason"`
	Count   int       `json:"count"`
	FirstAt time.Time `json:"first_at"`
	LastAt  time.Time `json:"last_at"`
}

const (
	// maxBatchItems bounds one sync's report. Past it the remainder is
	// dropped and logged, never returned as an error.
	maxBatchItems = 200
	// maxBatchIDLen bounds the opaque token the agent stamps its batch with.
	maxBatchIDLen = 64
	// maxProcessNameLen is generous for a Windows process name.
	maxProcessNameLen = 260
	// maxKillCount clamps a count. A machine claiming a hundred thousand kills
	// of one process between two syncs is broken; storing the claim verbatim
	// only spreads the breakage into the UI.
	maxKillCount = 100000
)

// readSyncBody collects everything the agent posted. Anything it cannot vouch
// for is dropped rather than rejected: no body, a body over the route's cap,
// not JSON, no runtime key, a malformed batch — every one of them yields a
// usable result. Telemetry never fails a sync.
func readSyncBody(r *http.Request) agentSyncBody {
	var body agentSyncBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return agentSyncBody{Runtime: []byte(`{}`)}
	}
	body.Runtime = sanitizeJSONObject(body.Runtime)
	if len(body.BatchID) > maxBatchIDLen {
		// An id that long is not one this server issued a receipt for; without
		// a usable id the batch cannot be deduplicated, so it is not stored.
		return agentSyncBody{Runtime: body.Runtime}
	}
	if len(body.Blocked) > maxBatchItems {
		slog.Warn("agent batch over the item cap", "items", len(body.Blocked))
		body.Blocked = body.Blocked[:maxBatchItems]
	}
	return body
}

// validBlockedItems drops what cannot be stored and normalises the rest. The
// agent may not claim 'locked': it cannot distinguish a lock from a whitelist
// that allows nothing, because both reach it as "whitelist, empty list". The
// caller rewrites the reason when the machine is actually blocked.
func validBlockedItems(items []blockedItem, now time.Time) []blockedItem {
	out := make([]blockedItem, 0, len(items))
	for _, it := range items {
		it.Process = sanitizeText(it.Process)
		if it.Process == "" {
			continue
		}
		if len(it.Process) > maxProcessNameLen {
			it.Process = it.Process[:maxProcessNameLen]
		}
		switch it.Reason {
		case "blacklist", "whitelist", "overflow":
		default:
			continue
		}
		if it.Count < 1 {
			continue
		}
		if it.Count > maxKillCount {
			it.Count = maxKillCount
		}
		it.FirstAt = clampAgentTime(it.FirstAt, now)
		it.LastAt = clampAgentTime(it.LastAt, now)
		if it.LastAt.Before(it.FirstAt) {
			it.LastAt = it.FirstAt
		}
		out = append(out, it)
	}
	return out
}

// clampAgentTime pins an agent-supplied timestamp into a plausible window. The
// feed is ordered by the server's own created_at precisely because this value
// cannot be trusted; it is kept because it is what an operator wants to read.
func clampAgentTime(t, now time.Time) time.Time {
	if t.IsZero() || t.Before(now.Add(-24*time.Hour)) || t.After(now.Add(5*time.Minute)) {
		return now
	}
	return t
}
```

Add `"time"` to the file's imports.

- [ ] **Step 4: Write the batch in the sync handler**

In `server/handlers_agent.go`, `handleAgentSync`, replace the telemetry block (line 223-229) with:

```go
	body := readSyncBody(r)
	items := validBlockedItems(body.Blocked, time.Now())

	// Telemetry and the kill batch are recorded in their own transaction,
	// deliberately outside the one that computes the policy. "Telemetry must
	// never stop enforcement" is only true if a failed telemetry write cannot
	// fail the request. A lost last_seen_at or a lost batch is a degraded
	// fleet view; a failed sync is a machine running with no policy at all.
	if err := s.inAccount(r.Context(), computer.AccountID, func(tx pgx.Tx) error {
		q := db.New(tx)
		if err := q.TouchComputer(r.Context(), db.TouchComputerParams{
			ID: computer.ID, Runtime: body.Runtime,
		}); err != nil {
			return err
		}
		// A batch already acknowledged is a retry of a sync whose response was
		// lost. The server has it; counting it again would inflate the log.
		if body.BatchID == "" || len(items) == 0 {
			return nil
		}
		if computer.LastEventBatch != nil && *computer.LastEventBatch == body.BatchID {
			return nil
		}
		for _, it := range items {
			reason := it.Reason
			// Only the server knows the machine is locked rather than merely
			// running a whitelist that allows nothing.
			if computer.Blocked && reason == "whitelist" {
				reason = "locked"
			}
			if err := q.RecordProcessEvent(r.Context(), db.RecordProcessEventParams{
				AccountID:  computer.AccountID,
				ComputerID: computer.ID,
				RoomID:     computer.RoomID,
				Process:    it.Process,
				Reason:     reason,
				Count:      int32(it.Count),
				FirstAt:    pgtype.Timestamptz{Time: it.FirstAt, Valid: true},
				LastAt:     pgtype.Timestamptz{Time: it.LastAt, Valid: true},
			}); err != nil {
				return err
			}
		}
		return q.SetLastEventBatch(r.Context(), db.SetLastEventBatchParams{
			ID: computer.ID, LastEventBatch: &body.BatchID,
		})
	}); err != nil {
		slog.Error("record agent telemetry", "computer_id", computer.ID, "error", err)
	}
```

Add `"github.com/jackc/pgx/v5/pgtype"` to the imports if it is not already there.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd server && go test ./... -run 'TestSyncRecords|TestARepeatedBatch|TestAMalformedBatch|TestAbsurdAgentTimestamps|TestKillsOnALockedMachine|TestSync' -v`

Expected: PASS, including the pre-existing `TestSyncOfUnassignedComputerEnforcesNothing` and `TestSyncRejectsUnknownToken`.

- [ ] **Step 6: Run the whole suite**

Run: `cd server && gofmt -l . && go vet ./... && go test ./...`

Expected: all green. `readRuntime` no longer exists, so any other caller would have failed to compile here.

- [ ] **Step 7: Update the docs**

In `specs/api.md`, `POST /agent/sync`: document the request body's new optional `batch_id` and `blocked` array with the field list and the clamping rules, and state that a repeated `batch_id` is ignored. Keep the existing sentence that telemetry never fails a sync and extend it to the batch.

- [ ] **Step 8: Commit**

```bash
git add server/handlers_agent.go server/process_events_test.go specs/api.md
git commit -m "feat(server): record the agent's blocked-process batch on sync"
```

---

## Task 5: The endpoints

**Files:**
- Create: `server/handlers_process_events.go`
- Modify: `server/routes.go:139-152`, `server/responses.go`, `server/authz_test.go:19-32`, `server/isolation_test.go`
- Test: `server/process_events_test.go`

**Interfaces:**
- Consumes: `db.ListRoomProcessEvents`, `db.ListComputerProcessEvents` (Task 1); `s.assertRoomVisible`, `tenantFrom`, `writeJSON`, `writeLookupError`, `encodeCursor`/`decodeCursor` (existing, `server/events.go:50-70`).
- Produces: `GET /api/v1/rooms/{roomID}/process-events`, `GET /api/v1/computers/{computerID}/process-events`, and the `ProcessEventResponse` type in `responses.go`.

- [ ] **Step 1: Write the failing tests**

Append to `server/process_events_test.go`:

```go
func TestRoomProcessEventsAreServed(t *testing.T) {
	s := &Server{pool: testPool(t)}
	h := s.setupRoutes()
	c := registerAndLogin(t, s, "parent@example.com")
	room := createRoom(t, s, c, "Класс 2")
	_, agentToken := enroll(t, s, mintBindingToken(t, s, c), "guid-1", "PC-1")

	var list struct {
		Computers []struct {
			ID string `json:"id"`
		} `json:"computers"`
	}
	decodeInto(t, doJSON(t, h, "GET", "/api/v1/computers", nil, c), &list)
	if rr := doJSON(t, h, "PATCH", "/api/v1/computers/"+list.Computers[0].ID,
		map[string]any{"room_id": room}, c); rr.Code != 200 {
		t.Fatalf("assign: %d %s", rr.Code, rr.Body.String())
	}

	now := time.Now().UTC().Format(time.RFC3339)
	syncWithBatch(t, s, agentToken, "batch-1", []map[string]any{
		{"process": "steam.exe", "reason": "blacklist", "count": 47, "first_at": now, "last_at": now},
		{"process": "discord.exe", "reason": "blacklist", "count": 2, "first_at": now, "last_at": now},
	})

	rr := doJSON(t, h, "GET", "/api/v1/rooms/"+room+"/process-events", nil, c)
	if rr.Code != 200 {
		t.Fatalf("status %d %s", rr.Code, rr.Body.String())
	}
	var out struct {
		ProcessEvents []struct {
			Process      string `json:"process"`
			Reason       string `json:"reason"`
			Count        int    `json:"count"`
			ComputerName string `json:"computer_name"`
		} `json:"process_events"`
	}
	decodeInto(t, rr, &out)
	if len(out.ProcessEvents) != 2 {
		t.Fatalf("got %d rows, want 2: %+v", len(out.ProcessEvents), out.ProcessEvents)
	}
	// Resolved server-side; the alternative is the cabinet issuing an N+1 of
	// lookups to render a list.
	if out.ProcessEvents[0].ComputerName != "PC-1" {
		t.Fatalf("computer_name %q, want PC-1", out.ProcessEvents[0].ComputerName)
	}
}

func TestProcessEventsFilterByProcessAndReason(t *testing.T) {
	s := &Server{pool: testPool(t)}
	h := s.setupRoutes()
	c := registerAndLogin(t, s, "parent@example.com")
	room := createRoom(t, s, c, "Класс 2")
	_, agentToken := enroll(t, s, mintBindingToken(t, s, c), "guid-1", "PC-1")

	var list struct {
		Computers []struct {
			ID string `json:"id"`
		} `json:"computers"`
	}
	decodeInto(t, doJSON(t, h, "GET", "/api/v1/computers", nil, c), &list)
	doJSON(t, h, "PATCH", "/api/v1/computers/"+list.Computers[0].ID, map[string]any{"room_id": room}, c)

	now := time.Now().UTC().Format(time.RFC3339)
	syncWithBatch(t, s, agentToken, "batch-1", []map[string]any{
		{"process": "steam.exe", "reason": "blacklist", "count": 1, "first_at": now, "last_at": now},
		{"process": "notepad.exe", "reason": "whitelist", "count": 1, "first_at": now, "last_at": now},
	})

	var out struct {
		ProcessEvents []struct {
			Process string `json:"process"`
		} `json:"process_events"`
	}
	decodeInto(t, doJSON(t, h, "GET",
		"/api/v1/rooms/"+room+"/process-events?process=steam.exe", nil, c), &out)
	if len(out.ProcessEvents) != 1 || out.ProcessEvents[0].Process != "steam.exe" {
		t.Fatalf("process filter returned %+v", out.ProcessEvents)
	}

	out.ProcessEvents = nil
	decodeInto(t, doJSON(t, h, "GET",
		"/api/v1/rooms/"+room+"/process-events?reason=whitelist", nil, c), &out)
	if len(out.ProcessEvents) != 1 || out.ProcessEvents[0].Process != "notepad.exe" {
		t.Fatalf("reason filter returned %+v", out.ProcessEvents)
	}
}

// Keyed on (created_at, id), not an offset: rows arrive while somebody pages.
func TestProcessEventsPageWithoutRepeatingOrSkipping(t *testing.T) {
	s := &Server{pool: testPool(t)}
	h := s.setupRoutes()
	c := registerAndLogin(t, s, "parent@example.com")
	room := createRoom(t, s, c, "Класс 2")
	_, agentToken := enroll(t, s, mintBindingToken(t, s, c), "guid-1", "PC-1")

	var list struct {
		Computers []struct {
			ID string `json:"id"`
		} `json:"computers"`
	}
	decodeInto(t, doJSON(t, h, "GET", "/api/v1/computers", nil, c), &list)
	doJSON(t, h, "PATCH", "/api/v1/computers/"+list.Computers[0].ID, map[string]any{"room_id": room}, c)

	now := time.Now().UTC().Format(time.RFC3339)
	const rows = 5
	batch := make([]map[string]any, 0, rows)
	for i := 0; i < rows; i++ {
		batch = append(batch, map[string]any{
			"process": "p" + strconv.Itoa(i) + ".exe", "reason": "blacklist",
			"count": 1, "first_at": now, "last_at": now,
		})
	}
	syncWithBatch(t, s, agentToken, "batch-1", batch)

	type page struct {
		ProcessEvents []struct {
			ID string `json:"id"`
		} `json:"process_events"`
		NextCursor string `json:"next_cursor"`
	}
	seen := map[string]bool{}
	cursor := ""
	for requests := 0; ; requests++ {
		if requests > 20 {
			t.Fatal("paging did not terminate")
		}
		path := "/api/v1/rooms/" + room + "/process-events?limit=2"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		rr := doJSON(t, h, "GET", path, nil, c)
		if rr.Code != 200 {
			t.Fatalf("page %d: %d %s", requests, rr.Code, rr.Body.String())
		}
		var p page
		decodeInto(t, rr, &p)
		for _, e := range p.ProcessEvents {
			if seen[e.ID] {
				t.Fatalf("row %s came back twice", e.ID)
			}
			seen[e.ID] = true
		}
		if p.NextCursor == "" {
			break
		}
		cursor = p.NextCursor
	}
	if len(seen) != rows {
		t.Fatalf("paged %d rows, want %d", len(seen), rows)
	}
}

// A machine in no room still has a history, and the computer screen is where
// it is read. The room query can never match it.
func TestComputerProcessEventsWorkWithoutARoom(t *testing.T) {
	s := &Server{pool: testPool(t)}
	h := s.setupRoutes()
	c := registerAndLogin(t, s, "parent@example.com")
	_, agentToken := enroll(t, s, mintBindingToken(t, s, c), "guid-1", "PC-1")

	var list struct {
		Computers []struct {
			ID string `json:"id"`
		} `json:"computers"`
	}
	decodeInto(t, doJSON(t, h, "GET", "/api/v1/computers", nil, c), &list)
	computerID := list.Computers[0].ID

	now := time.Now().UTC().Format(time.RFC3339)
	syncWithBatch(t, s, agentToken, "batch-1", []map[string]any{
		{"process": "steam.exe", "reason": "blacklist", "count": 1, "first_at": now, "last_at": now},
	})

	var out struct {
		ProcessEvents []struct {
			Process string `json:"process"`
		} `json:"process_events"`
	}
	rr := doJSON(t, h, "GET", "/api/v1/computers/"+computerID+"/process-events", nil, c)
	if rr.Code != 200 {
		t.Fatalf("status %d %s", rr.Code, rr.Body.String())
	}
	decodeInto(t, rr, &out)
	if len(out.ProcessEvents) != 1 {
		t.Fatalf("got %d rows, want 1", len(out.ProcessEvents))
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd server && go test ./... -run 'TestRoomProcessEventsAreServed|TestProcessEventsFilter|TestProcessEventsPage|TestComputerProcessEventsWork' -v`

Expected: FAIL with status `404` — the routes do not exist.

- [ ] **Step 3: Add the response type**

In `server/responses.go`, alongside the existing types:

`responses.go` already imports `time`, `uuid` and `pgtype`, so no import change is needed.

```go
// ProcessEventResponse is one row of the blocking log. Like every response
// type here it is the API's own shape, not a generated row, and it carries no
// account_id.
type ProcessEventResponse struct {
	ID           string    `json:"id"`
	ComputerID   string    `json:"computer_id"`
	ComputerName string    `json:"computer_name"`
	RoomID       *string   `json:"room_id"`
	Process      string    `json:"process"`
	Reason       string    `json:"reason"`
	Count        int       `json:"count"`
	FirstAt      time.Time `json:"first_at"`
	LastAt       time.Time `json:"last_at"`
	CreatedAt    time.Time `json:"created_at"`
}
```

- [ ] **Step 4: Write the handlers**

Create `server/handlers_process_events.go`:

```go
package main

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"server/internal/db"
)

const (
	defaultProcessEventLimit = 50
	maxProcessEventLimit     = 200
)

// processEventFilters is what the query string may narrow the log by. Every
// field is optional and they compose; an absent one is inert in SQL.
type processEventFilters struct {
	Process  *string
	Reason   *string
	Computer *uuid.UUID
	Since    pgtype.Timestamptz
	Until    pgtype.Timestamptz
	Limit    int32
	Before   pgtype.Timestamptz
	BeforeID *uuid.UUID
}

// readProcessEventFilters parses the query string. A malformed value is
// ignored rather than rejected: this is a log viewer, and a mistyped filter
// that returns the unfiltered feed is friendlier than a 400 with no rows.
// The one exception is the cursor, which is the server's own opaque token —
// a broken one means the client is out of step and should be told so.
func readProcessEventFilters(r *http.Request) (processEventFilters, error) {
	f := processEventFilters{Limit: defaultProcessEventLimit}
	q := r.URL.Query()

	if v := q.Get("process"); v != "" {
		f.Process = &v
	}
	if v := q.Get("reason"); v == "blacklist" || v == "whitelist" || v == "locked" || v == "overflow" {
		f.Reason = &v
	}
	if v := q.Get("computer_id"); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			f.Computer = &id
		}
	}
	if v := q.Get("since"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			f.Since = pgtype.Timestamptz{Time: t, Valid: true}
		}
	}
	if v := q.Get("until"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			f.Until = pgtype.Timestamptz{Time: t, Valid: true}
		}
	}
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			if n > maxProcessEventLimit {
				n = maxProcessEventLimit
			}
			f.Limit = int32(n)
		}
	}
	if v := q.Get("cursor"); v != "" {
		at, id, err := decodeCursor(v)
		if err != nil {
			return f, err
		}
		f.Before = at
		f.BeforeID = &id
	}
	return f, nil
}

func (s *Server) handleListRoomProcessEvents(w http.ResponseWriter, r *http.Request) {
	t, _ := tenantFrom(r.Context())
	roomID, err := uuid.Parse(chi.URLParam(r, "roomID"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "Not found"})
		return
	}
	filters, err := readProcessEventFilters(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Invalid cursor"})
		return
	}

	var rows []db.ListRoomProcessEventsRow
	err = s.inAccount(r.Context(), t.AccountID, func(tx pgx.Tx) error {
		q := db.New(tx)
		// The room must be visible to this caller, not merely in the account:
		// a guest sees only the rooms granted to them.
		if err := s.assertRoomVisible(r.Context(), q, t, roomID); err != nil {
			return err
		}
		var err error
		rows, err = q.ListRoomProcessEvents(r.Context(), db.ListRoomProcessEventsParams{
			RoomID: roomID, Process: filters.Process, Reason: filters.Reason,
			Computer: filters.Computer, Since: filters.Since, Until: filters.Until,
			BeforeTime: filters.Before, BeforeID: filters.BeforeID,
			RowLimit: filters.Limit,
		})
		return err
	})
	if err != nil {
		s.writeProcessEventError(w, err, "list room process events")
		return
	}
	writeProcessEventPage(w, roomRowsToResponses(rows), int(filters.Limit))
}

func (s *Server) handleListComputerProcessEvents(w http.ResponseWriter, r *http.Request) {
	t, _ := tenantFrom(r.Context())
	computerID, err := uuid.Parse(chi.URLParam(r, "computerID"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "Not found"})
		return
	}
	filters, err := readProcessEventFilters(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Invalid cursor"})
		return
	}

	var rows []db.ListComputerProcessEventsRow
	err = s.inAccount(r.Context(), t.AccountID, func(tx pgx.Tx) error {
		q := db.New(tx)
		// Reading the computer first is what makes a guest's reach the same
		// here as everywhere else: an unassigned machine, or one in a room
		// they were not granted, is not theirs to read the history of.
		computer, err := q.GetComputer(r.Context(), computerID)
		if err != nil {
			return err
		}
		if t.Role == roleMember {
			if computer.RoomID == nil {
				return pgx.ErrNoRows
			}
			if err := s.assertRoomVisible(r.Context(), q, t, *computer.RoomID); err != nil {
				return err
			}
		}
		rows, err = q.ListComputerProcessEvents(r.Context(), db.ListComputerProcessEventsParams{
			ComputerID: computerID, Process: filters.Process, Reason: filters.Reason,
			Since: filters.Since, Until: filters.Until,
			BeforeTime: filters.Before, BeforeID: filters.BeforeID,
			RowLimit: filters.Limit,
		})
		return err
	})
	if err != nil {
		s.writeProcessEventError(w, err, "list computer process events")
		return
	}
	writeProcessEventPage(w, computerRowsToResponses(rows), int(filters.Limit))
}

// writeProcessEventError keeps "not yours" and "not there" indistinguishable,
// the way every other lookup in this API does, and keeps a database failure a
// 500 rather than dressing it up as a 404.
func (s *Server) writeProcessEventError(w http.ResponseWriter, err error, what string) {
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "Not found"})
		return
	}
	slog.Error(what, "error", err)
	writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Internal error"})
}

// writeProcessEventPage emits the page and a cursor, present only when the
// page was full — its absence means the end of the feed.
func writeProcessEventPage(w http.ResponseWriter, out []ProcessEventResponse, limit int) {
	body := map[string]any{"process_events": out}
	if len(out) == limit && len(out) > 0 {
		last := out[len(out)-1]
		body["next_cursor"] = strconv.FormatInt(last.CreatedAt.UnixNano(), 10) + "_" + last.ID
	}
	writeJSON(w, http.StatusOK, body)
}

func roomRowsToResponses(rows []db.ListRoomProcessEventsRow) []ProcessEventResponse {
	out := make([]ProcessEventResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, ProcessEventResponse{
			ID: r.ID.String(), ComputerID: r.ComputerID.String(),
			ComputerName: r.ComputerName, RoomID: uuidPtrString(r.RoomID),
			Process: r.Process, Reason: r.Reason, Count: int(r.Count),
			FirstAt: r.FirstAt.Time, LastAt: r.LastAt.Time, CreatedAt: r.CreatedAt.Time,
		})
	}
	return out
}

func computerRowsToResponses(rows []db.ListComputerProcessEventsRow) []ProcessEventResponse {
	out := make([]ProcessEventResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, ProcessEventResponse{
			ID: r.ID.String(), ComputerID: r.ComputerID.String(),
			ComputerName: r.ComputerName, RoomID: uuidPtrString(r.RoomID),
			Process: r.Process, Reason: r.Reason, Count: int(r.Count),
			FirstAt: r.FirstAt.Time, LastAt: r.LastAt.Time, CreatedAt: r.CreatedAt.Time,
		})
	}
	return out
}

func uuidPtrString(id *uuid.UUID) *string {
	if id == nil {
		return nil
	}
	s := id.String()
	return &s
}
```

- [ ] **Step 5: Register the routes**

In `server/routes.go`, inside the guest-reachable group (after `r.Get("/rooms/{roomID}/members", …)` and after `r.Get("/computers/{computerID}", …)` respectively):

```go
			r.Get("/rooms/{roomID}/process-events", s.handleListRoomProcessEvents)
```

```go
			r.Get("/computers/{computerID}/process-events", s.handleListComputerProcessEvents)
```

Both are reads, both are gated by the room-visibility check inside the handler, so both belong on the guest-reachable side — the blocking log for a room somebody was deliberately given is exactly what they were given it for.

- [ ] **Step 6: Classify the routes in the authz table**

In `server/authz_test.go`, add to `guestReachableRoutes`:

```go
	"GET /api/v1/rooms/{roomID}/process-events":          true,
	"GET /api/v1/computers/{computerID}/process-events":  true,
```

- [ ] **Step 7: Add the isolation rows**

In `server/isolation_test.go`, add to the `cases` table:

```go
		{"GET", "/api/v1/rooms/" + roomA + "/process-events", nil},
		{"GET", "/api/v1/computers/" + computerA + "/process-events", nil},
```

- [ ] **Step 8: Run the tests to verify they pass**

Run: `cd server && go test ./... -run 'TestRoomProcessEvents|TestProcessEvents|TestComputerProcessEvents|TestCrossAccountAccessIsAlways404|TestEveryAPIRouteIsClassified|TestGuestIsRefusedEveryManagerRoute' -v`

Expected: PASS, all of them. `TestEveryAPIRouteIsClassified` is the one that would have failed had step 6 been skipped.

- [ ] **Step 9: Run the whole suite**

Run: `cd server && gofmt -l . && go vet ./... && go test ./...`

Expected: all green.

- [ ] **Step 10: Update the docs**

In `specs/api.md`, add both endpoints under the cabinet section: auth class (session, guest-reachable), the filter query parameters, the cursor semantics, and the response shape. In `CLAUDE.md`, add both to the **API Endpoints** list.

- [ ] **Step 11: Commit**

```bash
git add server/handlers_process_events.go server/responses.go server/routes.go \
        server/authz_test.go server/isolation_test.go server/process_events_test.go \
        specs/api.md CLAUDE.md
git commit -m "feat(server): serve the blocking log for a room and for a computer"
```

---

## Task 6: Retention, and the fake agent

**Files:**
- Create: `server/db/migrations/00018_process_event_retention.sql`
- Modify: `server/maintenance.go`, `server/maintenance_test.go`, `server/fakeagent/`
- Modify: `specs/server.md`

**Interfaces:**
- Consumes: `s.purgeOnce` (existing, `server/maintenance.go:39`).
- Produces: `purge_old_process_events(INTERVAL) RETURNS BIGINT`; `processEventRetention = 30 * 24 * time.Hour`.

- [ ] **Step 1: Write the failing test**

Append to `server/maintenance_test.go`:

```go
// The kill log gets its own retention: 30 days, not the audit feed's 180. It
// is machine output and there is far more of it, and "what was killed last
// season?" is not a question anybody asks.
func TestPurgeRemovesOldProcessEventsButKeepsRecentOnes(t *testing.T) {
	s := &Server{pool: testPool(t)}
	ctx := context.Background()

	c := registerAndLogin(t, s, "parent@example.com")
	enroll(t, s, mintBindingToken(t, s, c), "guid-1", "PC-1")
	account := accountIDOf(t, s, "parent@example.com")

	var computerID uuid.UUID
	if err := s.inAccount(ctx, account, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id FROM computers LIMIT 1`).Scan(&computerID)
	}); err != nil {
		t.Fatalf("read computer: %v", err)
	}

	// Written on the owner pool: created_at is backdated, which the app role's
	// own insert path never does.
	obs := observe(t)
	for _, age := range []string{"200 days", "1 day"} {
		if _, err := obs.Exec(ctx, `
			INSERT INTO process_events
			  (account_id, computer_id, process, reason, count, first_at, last_at, created_at)
			VALUES ($1, $2, 'steam.exe', 'blacklist', 1, now(), now(), now() - $3::interval)`,
			account, computerID, age); err != nil {
			t.Fatalf("seed %s: %v", age, err)
		}
	}

	s.purgeOnce(ctx)

	var left int
	if err := obs.QueryRow(ctx, `SELECT count(*) FROM process_events`).Scan(&left); err != nil {
		t.Fatalf("count: %v", err)
	}
	if left != 1 {
		t.Fatalf("%d rows left, want 1 (the recent one)", left)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd server && go test ./... -run TestPurgeRemovesOldProcessEvents -v`

Expected: FAIL — 2 rows left; nothing purges the table yet.

- [ ] **Step 3: Write the migration**

Create `server/db/migrations/00018_process_event_retention.sql`:

```sql
-- +goose Up
-- Retention for the blocking log. Same escape hatch, same reasoning as
-- purge_old_events in 00016: process_events carries an RLS policy keyed on
-- app.account_id, so a DELETE without a scope matches nothing, and a purge
-- that looped over every account would need to enumerate them. SECURITY
-- DEFINER runs as the table owner, and the only thing this function can do is
-- delete rows older than the interval it is given, returning a count. It
-- cannot read a single row out.
--
-- search_path is pinned because a SECURITY DEFINER function that resolves
-- unqualified names through the caller's search_path is a privilege-escalation
-- primitive.
-- +goose StatementBegin
CREATE FUNCTION purge_old_process_events(older_than INTERVAL) RETURNS BIGINT
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
    purged BIGINT;
BEGIN
    DELETE FROM public.process_events WHERE created_at < now() - older_than;
    GET DIAGNOSTICS purged = ROW_COUNT;
    RETURN purged;
END
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION purge_old_process_events(INTERVAL) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION purge_old_process_events(INTERVAL) TO guardian_app;

-- +goose Down
DROP FUNCTION purge_old_process_events(INTERVAL);
```

- [ ] **Step 4: Wire it into the hourly loop**

In `server/maintenance.go`, add to the constant block:

```go
	// processEventRetention is shorter than eventRetention on purpose. The
	// audit feed is a narrative worth keeping a season; the blocking log is
	// machine output, orders of magnitude larger, and nobody asks what was
	// killed six months ago.
	processEventRetention = 30 * 24 * time.Hour
```

and at the end of `purgeOnce`:

```go
	// Same RLS reasoning as the events purge above; migration 00018 provides
	// the function.
	var purgedKills int64
	if err := s.pool.QueryRow(ctx,
		`SELECT purge_old_process_events($1::interval)`, processEventRetention).Scan(&purgedKills); err != nil {
		slog.Error("purge old process events", "error", err)
	} else if purgedKills > 0 {
		slog.Info("purged old process events", "count", purgedKills,
			"retention", processEventRetention.String())
	}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `cd server && go test ./... -run TestPurgeRemoves -v`

Expected: PASS, together with the pre-existing events-purge test.

- [ ] **Step 6: Teach the fake agent to report kills**

`server/fakeagent` exists so the API can be exercised without a Windows machine, and the blocking log is now part of what there is to exercise.

In `server/fakeagent/main.go`, replace `sync` (line 91):

```go
// killCycle grows on every sync so a human watching the cabinet sees the count
// move, and batch is fresh each time so nothing is deduplicated away.
var killCycle int

func sync(server, token string) (string, error) {
	killCycle++
	now := time.Now().UTC().Format(time.RFC3339)
	payload, err := json.Marshal(map[string]any{
		"runtime":  map[string]any{"uptime_s": 1234, "user": "fake"},
		"batch_id": fmt.Sprintf("fake-batch-%d", killCycle),
		"blocked": []map[string]any{{
			"process": "steam.exe", "reason": "blacklist", "count": killCycle,
			"first_at": now, "last_at": now,
		}},
	})
	if err != nil {
		return "", err
	}
	req, _ := http.NewRequest("POST", server+"/agent/sync", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, raw)
	}
	return string(raw), nil
}
```

Add `bytes` to the imports; drop `strings` if nothing else in the file uses it (`go vet` will say).

- [ ] **Step 7: Verify the fake agent end to end**

Run, with the dev database up and the server running:

```sh
cd server && go run ./fakeagent
```

Expected: the agent enrolls and syncs, and `GET /api/v1/computers/{id}/process-events` returns the rows it reported. This is the manual check that the whole path works outside the test harness.

- [ ] **Step 8: Run the whole suite**

Run: `cd server && gofmt -l . && go vet ./... && go test ./...`

Expected: all green.

- [ ] **Step 9: Update the docs**

In `specs/server.md`, add `purge_old_process_events` to the **Maintenance** section next to `purge_old_events`, with the 30-day retention and the reason it differs. In `CLAUDE.md`, extend the `maintenance.go` bullet to mention both purges.

- [ ] **Step 10: Commit**

```bash
git add server/db/migrations/00018_process_event_retention.sql \
        server/maintenance.go server/maintenance_test.go server/fakeagent \
        specs/server.md CLAUDE.md
git commit -m "feat(server): purge blocking-log rows older than 30 days"
```

---

## Done when

- A kill on a managed machine appears in `GET /api/v1/rooms/{roomID}/process-events` within one sync interval, aggregated with a count.
- A sync retried after a lost response does not double the count.
- A malformed batch never fails a sync.
- `go test ./...` passes in both `server/` and `agent/`; `gofmt -l .` prints nothing in both; `sqlc diff` is clean.
- `specs/api.md`, `specs/server.md`, `specs/agent.md` and `CLAUDE.md` describe what was built.

## Not in this plan

Steps 2–9 of the spec's implementation order, each of which gets its own plan: mail and the account lifecycle · invitations · the `viewer` role and the route-group split · schedules and temporary suspension · templates · the command queue · agent versioning and updates · fleet management · the Security screen's server side.

The **Журнал** tab itself is UI. There is no `web/` directory yet; this plan builds the API the tab will call, and the tab lands with the cabinet.
