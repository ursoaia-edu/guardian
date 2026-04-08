package main

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

var migrateOnce sync.Once

func ownerDSN(t *testing.T) string {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; start server/docker-compose.dev.yml")
	}
	return dsn
}

// testPool returns a pool connected as guardian_app, with migrations applied
// once per test binary and every table emptied for this test.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	owner := ownerDSN(t)
	migrateOnce.Do(func() {
		if err := runMigrations(context.Background(), owner); err != nil {
			t.Fatalf("migrations failed: %v", err)
		}
	})

	appDSN := os.Getenv("TEST_APP_DATABASE_URL")
	if appDSN == "" {
		t.Skip("TEST_APP_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), appDSN)
	if err != nil {
		t.Fatalf("connect as guardian_app: %v", err)
	}
	t.Cleanup(pool.Close)
	truncateAll(t, owner)
	return pool
}

func truncateAll(t *testing.T, ownerDSN string) {
	t.Helper()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, ownerDSN)
	if err != nil {
		t.Fatalf("connect as owner: %v", err)
	}
	defer pool.Close()

	rows, err := pool.Query(ctx, `
		SELECT tablename FROM pg_tables
		WHERE schemaname = 'public' AND tablename <> 'goose_db_version'`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan table name: %v", err)
		}
		names = append(names, n)
	}
	rows.Close()
	for _, n := range names {
		if _, err := pool.Exec(ctx, "TRUNCATE TABLE "+n+" CASCADE"); err != nil {
			t.Fatalf("truncate %s: %v", n, err)
		}
	}
}

func TestMigrationsCreateUsersTable(t *testing.T) {
	pool := testPool(t)
	var exists bool
	err := pool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables
		 WHERE table_schema = 'public' AND table_name = 'users')`).Scan(&exists)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if !exists {
		t.Fatal("users table was not created")
	}
}

func TestAppRoleCanInsertAccount(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	var userID string
	err := pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash) VALUES ($1, $2) RETURNING id`,
		"owner@example.com", "x").Scan(&userID)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	var accountID string
	err = pool.QueryRow(ctx,
		`INSERT INTO accounts (name, owner_user_id) VALUES ($1, $2) RETURNING id`,
		"Acme", userID).Scan(&accountID)
	if err != nil {
		t.Fatalf("insert account: %v", err)
	}
	if accountID == "" {
		t.Fatal("expected an account id")
	}
}
