package main

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// seedAccount inserts a user and an account, returning the account id.
func seedAccount(t *testing.T, s *Server, email, name string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var userID, accountID uuid.UUID
	err := s.pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash) VALUES ($1, 'x') RETURNING id`,
		email).Scan(&userID)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	err = s.pool.QueryRow(ctx,
		`INSERT INTO accounts (name, owner_user_id) VALUES ($1, $2) RETURNING id`,
		name, userID).Scan(&accountID)
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}
	return accountID
}

func TestRLSHidesOtherAccounts(t *testing.T) {
	s := &Server{pool: testPool(t)}
	a := seedAccount(t, s, "a@example.com", "Account A")
	b := seedAccount(t, s, "b@example.com", "Account B")

	var seen int
	err := s.inAccount(context.Background(), a, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(),
			`SELECT count(*) FROM accounts WHERE id = $1`, b).Scan(&seen)
	})
	if err != nil {
		t.Fatalf("inAccount: %v", err)
	}
	if seen != 0 {
		t.Fatalf("account A saw %d rows of account B; RLS is not enforced", seen)
	}
}

func TestRLSShowsOwnAccount(t *testing.T) {
	s := &Server{pool: testPool(t)}
	a := seedAccount(t, s, "a@example.com", "Account A")

	var seen int
	err := s.inAccount(context.Background(), a, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(),
			`SELECT count(*) FROM accounts WHERE id = $1`, a).Scan(&seen)
	})
	if err != nil {
		t.Fatalf("inAccount: %v", err)
	}
	if seen != 1 {
		t.Fatalf("account A saw %d rows of itself, want 1", seen)
	}
}
