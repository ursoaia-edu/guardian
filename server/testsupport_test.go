package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var migrateOnce sync.Once

// doJSON sends v as a JSON body and returns the recorded response. When
// cookie is non-nil it is attached, which is how session-authenticated tests
// call the API.
func doJSON(t *testing.T, h http.Handler, method, path string, v any, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var body io.Reader
	if v != nil {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		body = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, body)
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func decodeInto(t *testing.T, rr *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rr.Body.Bytes(), v); err != nil {
		t.Fatalf("decode %s: %v", rr.Body.String(), err)
	}
}

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

// The application role holds DML on the schema and nothing more, so the plain
// path a new account takes has to work end to end: insert the user, then the
// account they own, scoped to that user the way handleRegister does.
func TestAppRoleCanInsertAccount(t *testing.T) {
	s := &Server{pool: testPool(t)}
	ctx := context.Background()

	var userID uuid.UUID
	err := s.pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash) VALUES ($1, $2) RETURNING id`,
		"owner@example.com", "x").Scan(&userID)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	var accountID uuid.UUID
	if err := s.inUser(ctx, userID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`INSERT INTO accounts (name, owner_user_id) VALUES ($1, $2) RETURNING id`,
			"Acme", userID).Scan(&accountID)
	}); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	if accountID == uuid.Nil {
		t.Fatal("expected an account id")
	}
}

// uuidFromString keeps the uuid dependency out of the isolation suite's own
// imports, where it would be the only use.
func uuidFromString(s string) (uuid.UUID, error) { return uuid.Parse(s) }

// observe returns a pool connected as the OWNER role, which owns the tables
// and is therefore not subject to RLS. Tests use it to check what is actually
// in the database, independently of the scoping the application applies.
//
// Reading account-scoped tables back on the application pool is not an
// alternative: outside a scope it returns nothing, by design — since migration
// 00014 that is true of accounts and account_members too, which used to be
// readable unscoped and were the last two tables where a test could get away
// with it.
func observe(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), ownerDSN(t))
	if err != nil {
		t.Fatalf("connect as owner: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}
