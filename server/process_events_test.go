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
