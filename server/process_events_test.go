package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

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
