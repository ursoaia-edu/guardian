package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"server/internal/db"
)

func TestCreateAccountWithOwner(t *testing.T) {
	s := &Server{pool: testPool(t)}
	ctx := context.Background()

	q := db.New(s.pool)
	user, err := q.CreateUser(ctx, db.CreateUserParams{
		Email:        "owner@example.com",
		PasswordHash: "x",
		Name:         "Owner",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Scoped to the user, as handleRegister does: since migration 00014 the
	// pre-scope INSERT policy on accounts requires owner_user_id =
	// current_user_id(), so an unscoped CreateAccount is refused outright.
	var account db.Account
	if err := s.inUser(ctx, user.ID, func(tx pgx.Tx) error {
		var err error
		account, err = db.New(tx).CreateAccount(ctx, db.CreateAccountParams{
			Name:        "Acme",
			OwnerUserID: user.ID,
		})
		return err
	}); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	err = s.inAccount(ctx, account.ID, func(tx pgx.Tx) error {
		return db.New(tx).AddAccountMember(ctx, db.AddAccountMemberParams{
			AccountID: account.ID,
			UserID:    user.ID,
			Role:      "owner",
		})
	})
	if err != nil {
		t.Fatalf("AddAccountMember: %v", err)
	}

	// ListAccessibleAccounts, not ListAccountsForUser: it is what production
	// (SessionAuth, handleMe) actually calls, and it is the union that also
	// covers room-guest access — ListAccountsForUser only ever saw
	// account_members and had no production caller.
	var accounts []db.ListAccessibleAccountsRow
	if err := s.inUser(ctx, user.ID, func(tx pgx.Tx) error {
		var err error
		accounts, err = db.New(tx).ListAccessibleAccounts(ctx, user.ID)
		return err
	}); err != nil {
		t.Fatalf("ListAccessibleAccounts: %v", err)
	}
	if len(accounts) != 1 || accounts[0].AccountID != account.ID || accounts[0].Role != "owner" {
		t.Fatalf("expected exactly the created account as owner, got %+v", accounts)
	}
}

// No handler serialises db.User today — that is exactly why this is worth
// guarding now, the same way TestComputerResponseHidesTheTokenHash guards
// computers.token_hash: sqlc.yaml's json:"-" override on password_hash has
// to survive a regeneration before plan 2 adds the first user-shaped
// endpoint, or the hash ships the moment one does.
func TestUserJSONHidesThePasswordHash(t *testing.T) {
	u := db.User{ID: [16]byte{1}, Email: "parent@example.com", PasswordHash: "$argon2id$secret-hash-material"}
	out, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), "password_hash") || strings.Contains(string(out), "secret-hash-material") {
		t.Fatalf("password hash leaked into db.User's JSON encoding: %s", out)
	}
}
