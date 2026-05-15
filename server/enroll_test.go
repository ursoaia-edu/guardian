package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"
)

func mintBindingToken(t *testing.T, s *Server, c *http.Cookie) string {
	t.Helper()
	rr := doJSON(t, s.setupRoutes(), "POST", "/api/v1/binding-tokens", nil, c)
	if rr.Code != 201 {
		t.Fatalf("mint: %d %s", rr.Code, rr.Body.String())
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out.Token
}

func enroll(t *testing.T, s *Server, binding, guid, hostname string) (int, string) {
	t.Helper()
	rr := doJSON(t, s.setupRoutes(), "POST", "/agent/enroll", map[string]any{
		"binding_token": binding,
		"machine_guid":  guid,
		"hostname":      hostname,
		"os_name":       "Windows 11",
		"os_build":      "22631",
		"arch":          "amd64",
		"agent_version": "2.1.0",
		"hardware":      map[string]any{"cpu": "i5-8400", "ram_gb": 16},
	}, nil)
	var out struct {
		AgentToken string `json:"agent_token"`
	}
	json.Unmarshal(rr.Body.Bytes(), &out)
	return rr.Code, out.AgentToken
}

func TestEnrollCreatesComputer(t *testing.T) {
	s := &Server{pool: testPool(t)}
	c := registerAndLogin(t, s, "parent@example.com")
	binding := mintBindingToken(t, s, c)

	code, agentToken := enroll(t, s, binding, "guid-1", "DESKTOP-8H3K")
	if code != 201 {
		t.Fatalf("enroll: %d", code)
	}
	if len(agentToken) != 64 {
		t.Fatalf("agent token is %d chars, want 64", len(agentToken))
	}

	// computers carries only computers_isolation (no unscoped "prescope" SELECT
	// hole the way accounts/account_members deliberately have, per
	// 00003_rls.sql), so an unscoped s.pool query would always see zero rows
	// here regardless of what handleEnroll inserted. The check runs inside the
	// account's own scope instead, the same way tenant_test.go and
	// computers_test.go already verify tenant-scoped state.
	accID := accountIDOf(t, s, "parent@example.com")
	var n int
	err := s.inAccount(context.Background(), accID, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(),
			`SELECT count(*) FROM computers WHERE hostname = $1`, "DESKTOP-8H3K").Scan(&n)
	})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("computers with that hostname: %d, want 1", n)
	}
}

func TestReenrollDoesNotDuplicate(t *testing.T) {
	s := &Server{pool: testPool(t)}
	c := registerAndLogin(t, s, "parent@example.com")
	binding := mintBindingToken(t, s, c)

	_, first := enroll(t, s, binding, "guid-1", "DESKTOP-8H3K")
	code, second := enroll(t, s, binding, "guid-1", "DESKTOP-RENAMED")
	if code != 201 {
		t.Fatalf("re-enroll: %d", code)
	}
	if first == second {
		t.Fatal("re-enrollment reused the old agent token; it must be rotated")
	}

	// See the comment in TestEnrollCreatesComputer: computers has no unscoped
	// read path, so this check runs inside the account's own scope.
	accID := accountIDOf(t, s, "parent@example.com")
	var n int
	err := s.inAccount(context.Background(), accID, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT count(*) FROM computers`).Scan(&n)
	})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("pool holds %d machines after a reinstall, want 1", n)
	}
}

func TestEnrollRejectsUnknownToken(t *testing.T) {
	s := &Server{pool: testPool(t)}
	registerAndLogin(t, s, "parent@example.com")
	code, _ := enroll(t, s, "0000000000000000000000000000000000000000000000000000000000000000",
		"guid-x", "PC-X")
	if code != 401 {
		t.Fatalf("status %d, want 401", code)
	}
}

// The kill switch has to actually kill. Nothing else exercises
// GetActiveBindingToken's `revoked_at IS NULL` filter, so without this test that
// clause could be deleted and every other test would still pass.
func TestRevokedBindingTokenStopsEnrolling(t *testing.T) {
	s := &Server{pool: testPool(t)}
	c := registerAndLogin(t, s, "parent@example.com")
	binding := mintBindingToken(t, s, c)

	if code, _ := enroll(t, s, binding, "guid-1", "PC-1"); code != 201 {
		t.Fatalf("enroll before revocation: %d", code)
	}
	if rr := doJSON(t, s.setupRoutes(), "DELETE", "/api/v1/binding-tokens", nil, c); rr.Code != 204 {
		t.Fatalf("revoke: %d %s", rr.Code, rr.Body.String())
	}
	if code, _ := enroll(t, s, binding, "guid-2", "PC-2"); code != 401 {
		t.Fatalf("a revoked binding token still enrolled a machine: %d", code)
	}
}

func TestEnrollRejectsOverPlanLimit(t *testing.T) {
	s := &Server{pool: testPool(t)}
	c := registerAndLogin(t, s, "parent@example.com")
	binding := mintBindingToken(t, s, c)
	// The default plan allows 3 computers (migration 00002).
	for i, guid := range []string{"g1", "g2", "g3"} {
		if code, _ := enroll(t, s, binding, guid, "PC"); code != 201 {
			t.Fatalf("enroll %d: %d", i, code)
		}
	}
	if code, _ := enroll(t, s, binding, "g4", "PC4"); code != 402 {
		t.Fatalf("fourth machine got %d, want 402", code)
	}
}
