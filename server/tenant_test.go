package main

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// seedAccount inserts a user and an account, returning the account id. The
// account insert runs inside inUser because that is the only way it is allowed
// to happen since migration 00014: the pre-scope INSERT policy on accounts
// requires owner_user_id = current_user_id(), so this mirrors what
// handleRegister does rather than working around it.
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
	if err := s.inUser(ctx, userID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`INSERT INTO accounts (name, owner_user_id) VALUES ($1, $2) RETURNING id`,
			name, userID).Scan(&accountID)
	}); err != nil {
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
		// Assert the scope itself, not just the query result: without this,
		// the test would also pass if inAccount failed to set app.account_id
		// at all, since an unscoped connection reads accounts via the
		// accounts_prescope policy too. Only TestRLSHidesOtherAccounts would
		// then catch the regression.
		var scope uuid.UUID
		if err := tx.QueryRow(context.Background(),
			`SELECT current_setting('app.account_id', true)::uuid`).Scan(&scope); err != nil {
			return fmt.Errorf("read scope: %w", err)
		}
		if scope != a {
			t.Fatalf("transaction scope = %s, want %s", scope, a)
		}
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

// TestRLSRejectsCrossAccountInsert is the write-path counterpart of
// TestRLSHidesOtherAccounts: a connection scoped to account A must not be
// able to insert an account_members row for account B, even though it knows
// B's id. This is what ruling R5 removed the unscoped
// account_members_insert policy to protect: without account_members_isolation's
// WITH CHECK, a scoped connection could still grant itself membership in any
// account it can name.
func TestRLSRejectsCrossAccountInsert(t *testing.T) {
	s := &Server{pool: testPool(t)}
	a := seedAccount(t, s, "a@example.com", "Account A")
	b := seedAccount(t, s, "b@example.com", "Account B")

	err := s.inAccount(context.Background(), a, func(tx pgx.Tx) error {
		var attackerUserID uuid.UUID
		if err := tx.QueryRow(context.Background(),
			`INSERT INTO users (email, password_hash) VALUES ($1, 'x') RETURNING id`,
			"attacker@example.com").Scan(&attackerUserID); err != nil {
			return fmt.Errorf("seed attacker user: %w", err)
		}
		_, err := tx.Exec(context.Background(),
			`INSERT INTO account_members (account_id, user_id, role) VALUES ($1, $2, 'owner')`,
			b, attackerUserID)
		return err
	})
	if err == nil {
		t.Fatal("insert into account_members for a foreign account succeeded; RLS is not enforced")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Fatalf("expected a row-level-security violation (SQLSTATE 42501), got: %v", err)
	}
}

// The write half of migration 00014. An account may only be created by the
// user who will own it: an unscoped connection cannot create one at all, and a
// connection scoped to one user cannot create an account owned by another.
// Without this, any code path that reached the pre-scope insert policy could
// mint an account belonging to somebody else.
func TestAccountInsertRequiresTheOwningUsersScope(t *testing.T) {
	s := &Server{pool: testPool(t)}
	ctx := context.Background()

	var owner, other uuid.UUID
	for _, seed := range []struct {
		email string
		into  *uuid.UUID
	}{{"owner@example.com", &owner}, {"other@example.com", &other}} {
		if err := s.pool.QueryRow(ctx,
			`INSERT INTO users (email, password_hash) VALUES ($1, 'x') RETURNING id`,
			seed.email).Scan(seed.into); err != nil {
			t.Fatalf("seed user %s: %v", seed.email, err)
		}
	}

	insert := func(scope *uuid.UUID, ownerID uuid.UUID) error {
		stmt := `INSERT INTO accounts (name, owner_user_id) VALUES ('Acme', $1)`
		if scope == nil {
			_, err := s.pool.Exec(ctx, stmt, ownerID)
			return err
		}
		return s.inUser(ctx, *scope, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, stmt, ownerID)
			return err
		})
	}

	isRLSViolation := func(err error) bool {
		var pgErr *pgconn.PgError
		return errors.As(err, &pgErr) && pgErr.Code == "42501"
	}

	if err := insert(nil, owner); !isRLSViolation(err) {
		t.Fatalf("an unscoped connection created an account: %v", err)
	}
	if err := insert(&other, owner); !isRLSViolation(err) {
		t.Fatalf("a connection scoped to one user created an account owned by another: %v", err)
	}
	// The positive control: the owner's own scope works, so the two rejections
	// above are the policy at work and not a broken statement.
	if err := insert(&owner, owner); err != nil {
		t.Fatalf("the owning user could not create their own account: %v", err)
	}
}
