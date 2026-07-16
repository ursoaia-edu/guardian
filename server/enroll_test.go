package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
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

	// The comparison above proves nothing on its own: two independent newToken()
	// calls differ whatever the database did, so an upsert that forgot
	// token_hash would hand the agent a credential that authenticates nothing
	// and this test would still pass. Read the stored digest back and check it
	// is the new token's, not the old one's.
	accountID := accountIDOf(t, s, "parent@example.com")
	var n int
	var storedHash string
	err := s.inAccount(context.Background(), accountID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(context.Background(),
			`SELECT count(*) FROM computers`).Scan(&n); err != nil {
			return err
		}
		return tx.QueryRow(context.Background(),
			`SELECT token_hash FROM computers WHERE machine_guid = $1`, "guid-1").Scan(&storedHash)
	})
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if n != 1 {
		t.Fatalf("pool holds %d machines after a reinstall, want 1", n)
	}
	if storedHash != hashToken(second) {
		t.Fatal("the stored digest is not the newly issued token's; the upsert did not rotate it")
	}
	if storedHash == hashToken(first) {
		t.Fatal("the old token still authenticates after a reinstall")
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

// RevokeAllBindingTokens is a table-wide UPDATE with no account predicate: the
// whole of its safety is one RLS policy. If that policy ever stops covering
// UPDATE, one customer pressing "invalidate my installers" silently invalidates
// every customer's, and nothing else in the suite would notice.
func TestRevocationDoesNotTouchAnotherAccount(t *testing.T) {
	s := &Server{pool: testPool(t)}
	ca := registerAndLogin(t, s, "a@example.com")
	cb := registerAndLogin(t, s, "b@example.com")
	bindingA := mintBindingToken(t, s, ca)
	bindingB := mintBindingToken(t, s, cb)

	if rr := doJSON(t, s.setupRoutes(), "DELETE", "/api/v1/binding-tokens", nil, ca); rr.Code != 204 {
		t.Fatalf("revoke as A: %d", rr.Code)
	}

	if code, _ := enroll(t, s, bindingA, "guid-a", "PC-A"); code != 401 {
		t.Fatalf("account A's own token survived its revocation: %d", code)
	}
	if code, _ := enroll(t, s, bindingB, "guid-b", "PC-B"); code != 201 {
		t.Fatalf("account A's revocation killed account B's token: %d", code)
	}
}

// A machine's own passport must never be able to make enrollment permanently
// impossible. readRuntime already guards this on the sync path (see its
// comment in handlers_agent.go): a JSON string carrying a NUL escape is legal
// JSON but jsonb refuses it, and a JSON string carrying invalid UTF-8 bytes is
// legal JSON but a text column refuses that too. A Windows machine on a
// non-UTF-8 codepage can put either into its hostname or its hardware
// inventory. Before this fix, either one 500'd the enrollment forever — the
// machine retries the exact same passport and gets the exact same 500 every
// time, so it can never join the fleet.
//
// Built from bytes rather than written as literals, same as
// TestMalformedRuntimeDoesNotBreakSync: an editor or formatter would silently
// rewrite a literal NUL escape or literal invalid-UTF-8 bytes in source.
func TestHostilePassportStillEnrolls(t *testing.T) {
	s := &Server{pool: testPool(t)}
	c := registerAndLogin(t, s, "parent@example.com")
	binding := mintBindingToken(t, s, c)

	nulEscape := string([]byte{'\\', 'u', '0', '0', '0', '0'})
	invalidUTF8 := string([]byte{0xff, 0xfe})

	body := []byte(`{` +
		`"binding_token":"` + binding + `",` +
		`"machine_guid":"guid-hostile",` +
		`"hostname":"DESKTOP-` + invalidUTF8 + `",` +
		`"os_name":"Windows 11",` +
		`"os_build":"22631",` +
		`"arch":"amd64",` +
		`"agent_version":"2.1.0",` +
		`"hardware":{"cpu":"i5` + nulEscape + `8400"}` +
		`}`)

	req := httptest.NewRequest("POST", "/agent/enroll", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.setupRoutes().ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("a hostile passport got %d, want 201: %s", rr.Code, rr.Body.String())
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

// The plan limit has to hold when a fleet is installed at once, which is the
// realistic case: an admin runs the installer across a lab in one sitting and
// every machine enrolls within the same second.
//
// Counting the seats and taking one are two statements. Without a lock held
// across them, concurrent enrollments all read the same count, all decide
// there is room, and all insert — the account ends up over its plan with no
// error anywhere. The lock in LockAccountComputerLimit is what makes the pair
// atomic, and this test is what says so: the default limit is 3, twice as many
// machines arrive together, and exactly 3 may get in.
func TestConcurrentEnrollmentsCannotExceedThePlanLimit(t *testing.T) {
	s := &Server{pool: testPool(t)}
	h := s.setupRoutes()
	c := registerAndLogin(t, s, "parent@example.com")
	binding := mintBindingToken(t, s, c)

	const limit = 3 // accounts.computer_limit default
	const attempts = 6

	var wg sync.WaitGroup
	codes := make([]int, attempts)
	start := make(chan struct{})
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body, _ := json.Marshal(map[string]any{
				"binding_token": binding,
				"machine_guid":  fmt.Sprintf("guid-%d", i),
				"hostname":      fmt.Sprintf("PC-%d", i),
			})
			req := httptest.NewRequest("POST", "/agent/enroll", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			<-start // release them together, so they contend for real
			h.ServeHTTP(rr, req)
			codes[i] = rr.Code
		}(i)
	}
	close(start)
	wg.Wait()

	var enrolled, refused int
	for i, code := range codes {
		switch code {
		case http.StatusCreated:
			enrolled++
		case http.StatusPaymentRequired:
			refused++
		default:
			t.Fatalf("enrollment %d answered %d, want 201 or 402", i, code)
		}
	}
	if enrolled != limit || refused != attempts-limit {
		t.Fatalf("%d enrolled and %d refused; the plan allows %d", enrolled, refused, limit)
	}

	// And the database agrees: the seats are what was actually taken, not
	// merely what the responses claimed.
	var count int
	if err := observe(t).QueryRow(context.Background(),
		`SELECT count(*) FROM computers`).Scan(&count); err != nil {
		t.Fatalf("count computers: %v", err)
	}
	if count != limit {
		t.Fatalf("the account holds %d computers on a plan of %d", count, limit)
	}
}

// A re-enrollment mints a new token for a machine that already had one, which
// stops whatever agent was holding the old one. That is the normal shape of a
// reinstall — and also what an account-wide binding token in the wrong hands
// can do to a machine that is running perfectly well. It is recorded as its
// own kind of event so the owner can see it happen.
func TestReenrollmentIsRecordedAsATokenRotation(t *testing.T) {
	s := &Server{pool: testPool(t)}
	h := s.setupRoutes()
	c := registerAndLogin(t, s, "parent@example.com")
	binding := mintBindingToken(t, s, c)

	enroll(t, s, binding, "guid-1", "PC-1")
	_, second := enroll(t, s, binding, "guid-1", "PC-1")
	if second == "" {
		t.Fatal("re-enrollment returned no token")
	}

	var out struct {
		Events []struct {
			Type string `json:"type"`
		} `json:"events"`
	}
	decodeInto(t, doJSON(t, h, "GET", "/api/v1/events", nil, c), &out)

	var enrolled, rotated int
	for _, e := range out.Events {
		switch e.Type {
		case "computer.enrolled":
			enrolled++
		case "computer.token_rotated":
			rotated++
		}
	}
	if enrolled != 1 || rotated != 1 {
		t.Fatalf("feed has %d enrollments and %d rotations, want one of each: %+v", enrolled, rotated, out.Events)
	}
}
