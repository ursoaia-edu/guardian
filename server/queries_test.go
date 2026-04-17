package main

import (
	"context"
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

	account, err := q.CreateAccount(ctx, db.CreateAccountParams{
		Name:        "Acme",
		OwnerUserID: user.ID,
	})
	if err != nil {
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

	accounts, err := q.ListAccountsForUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("ListAccountsForUser: %v", err)
	}
	if len(accounts) != 1 || accounts[0].ID != account.ID {
		t.Fatalf("expected exactly the created account, got %+v", accounts)
	}
}
