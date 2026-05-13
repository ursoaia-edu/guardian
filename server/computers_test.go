package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"server/internal/db"
)

// insertComputer puts a machine straight into the pool, standing in for an
// enrollment that Task 10 has not built yet.
func insertComputer(t *testing.T, s *Server, accountID uuid.UUID, guid, hostname string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := s.inAccount(context.Background(), accountID, func(tx pgx.Tx) error {
		c, err := db.New(tx).UpsertComputerByGUID(context.Background(), db.UpsertComputerByGUIDParams{
			AccountID: accountID, MachineGuid: guid, Hostname: hostname,
			OsName: "Windows 11", OsBuild: "22631", Arch: "amd64",
			AgentVersion: "2.1.0", TokenHash: hashToken(guid),
		})
		id = c.ID
		return err
	})
	if err != nil {
		t.Fatalf("insert computer: %v", err)
	}
	return id
}

func accountIDOf(t *testing.T, s *Server, email string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := s.pool.QueryRow(context.Background(), `
		SELECT m.account_id FROM account_members m
		JOIN users u ON u.id = m.user_id WHERE u.email = $1`, email).Scan(&id)
	if err != nil {
		t.Fatalf("account lookup: %v", err)
	}
	return id
}

func TestListComputersShowsOnlyOwnPool(t *testing.T) {
	s := &Server{pool: testPool(t)}
	ca := registerAndLogin(t, s, "a@example.com")
	registerAndLogin(t, s, "b@example.com")
	insertComputer(t, s, accountIDOf(t, s, "a@example.com"), "guid-a", "PC-A")
	insertComputer(t, s, accountIDOf(t, s, "b@example.com"), "guid-b", "PC-B")

	rr := doJSON(t, s.setupRoutes(), "GET", "/api/v1/computers", nil, ca)
	var out struct {
		Computers []struct {
			Hostname string `json:"hostname"`
		} `json:"computers"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Computers) != 1 || out.Computers[0].Hostname != "PC-A" {
		t.Fatalf("account A sees %+v; it must see only its own machine", out.Computers)
	}
}

func TestSetDisplayNameAndRoom(t *testing.T) {
	s := &Server{pool: testPool(t)}
	c := registerAndLogin(t, s, "parent@example.com")
	room := createRoom(t, s, c, "Kids room")
	id := insertComputer(t, s, accountIDOf(t, s, "parent@example.com"), "guid-1", "DESKTOP-8H3K")

	rr := doJSON(t, s.setupRoutes(), "PATCH", "/api/v1/computers/"+id.String(),
		map[string]any{"display_name": "Masha's laptop", "room_id": room}, c)
	if rr.Code != 200 {
		t.Fatalf("patch: %d %s", rr.Code, rr.Body.String())
	}

	// Read back through the OWNER connection, not s.pool. `computers` has no
	// pre-scope policy — only `accounts` and `account_members` do — so an
	// unscoped query as guardian_app sees zero rows and this assertion would
	// fail against a perfectly correct implementation. That RLS bites here is
	// the system working, not a problem to route around in production code.
	owner, err := pgxpool.New(context.Background(), os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("connect as owner: %v", err)
	}
	defer owner.Close()

	var name string
	var roomID *uuid.UUID
	if err := owner.QueryRow(context.Background(),
		`SELECT display_name, room_id FROM computers WHERE id = $1`, id).Scan(&name, &roomID); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if name != "Masha's laptop" {
		t.Fatalf("display_name is %q", name)
	}
	if roomID == nil || roomID.String() != room {
		t.Fatalf("room_id is %v, want %s", roomID, room)
	}
}

// The agent's credential digest must never leave the database. It is a one-way
// hash of a 32-byte random token, so exposure is not immediately exploitable —
// but a credential digest handed to every cabinet caller (including, from
// Task 14, a room guest) is the kind of leak that is only ever noticed after it
// matters. The sqlc struct tag is what enforces this; this test is what proves
// the tag still does its job.
func TestComputerResponseHidesTheTokenHash(t *testing.T) {
	s := &Server{pool: testPool(t)}
	c := registerAndLogin(t, s, "parent@example.com")
	id := insertComputer(t, s, accountIDOf(t, s, "parent@example.com"), "guid-1", "PC-1")

	for _, rr := range []*httptest.ResponseRecorder{
		doJSON(t, s.setupRoutes(), "GET", "/api/v1/computers", nil, c),
		doJSON(t, s.setupRoutes(), "PATCH", "/api/v1/computers/"+id.String(),
			map[string]any{"display_name": "Renamed"}, c),
	} {
		if strings.Contains(rr.Body.String(), "token_hash") {
			t.Fatalf("token hash leaked into an API response: %s", rr.Body.String())
		}
	}
}

func TestPatchComputerOfAnotherAccountIsNotFound(t *testing.T) {
	s := &Server{pool: testPool(t)}
	registerAndLogin(t, s, "a@example.com")
	cb := registerAndLogin(t, s, "b@example.com")
	idA := insertComputer(t, s, accountIDOf(t, s, "a@example.com"), "guid-a", "PC-A")

	rr := doJSON(t, s.setupRoutes(), "PATCH", "/api/v1/computers/"+idA.String(),
		map[string]any{"display_name": "stolen"}, cb)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("account B patched account A's machine: %d", rr.Code)
	}
}
