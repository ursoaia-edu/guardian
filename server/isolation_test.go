package main

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"
)

// Every account-scoped endpoint, exercised by account B against account A's
// object. Anything other than 404 is a data leak. New endpoints belong in this
// table; a route absent from it is a route nobody proved is isolated.
func TestCrossAccountAccessIsAlways404(t *testing.T) {
	s := &Server{pool: testPool(t)}
	h := s.setupRoutes()

	ca := registerAndLogin(t, s, "a@example.com")
	roomA := createRoom(t, s, ca, "A's room")
	var appRow struct {
		ID string `json:"id"`
	}
	addApp := doJSON(t, h, "POST", "/api/v1/rooms/"+roomA+"/applications",
		map[string]string{"name": "steam.exe", "list": "blacklist"}, ca)
	if addApp.Code != 201 {
		t.Fatalf("setup: add application: %d %s", addApp.Code, addApp.Body.String())
	}
	decodeInto(t, addApp, &appRow)
	appA := appRow.ID
	// Without this the DELETE case below would 404 at the router on an empty id
	// and pass while proving nothing — a safety net that fails open is worse
	// than none, because it is trusted.
	if appA == "" {
		t.Fatal("setup: the application was created but returned no id")
	}
	enroll(t, s, mintBindingToken(t, s, ca), "guid-a", "PC-A")

	var listA struct {
		Computers []struct {
			ID string `json:"id"`
		} `json:"computers"`
	}
	decodeInto(t, doJSON(t, h, "GET", "/api/v1/computers", nil, ca), &listA)
	computerA := listA.Computers[0].ID

	// A real membership on A's room, so the DELETE case below tests removing
	// an actual row rather than a no-op on a nonexistent one.
	cg := registerAndLogin(t, s, "guest@example.com")
	if rr := doJSON(t, h, "POST", "/api/v1/rooms/"+roomA+"/members",
		map[string]string{"email": "guest@example.com"}, ca); rr.Code != 201 {
		t.Fatalf("setup: grant guest access to A's room: %d %s", rr.Code, rr.Body.String())
	}
	var guestMe struct {
		UserID string `json:"user_id"`
	}
	decodeInto(t, doJSON(t, h, "GET", "/api/v1/me", nil, cg), &guestMe)
	if guestMe.UserID == "" {
		t.Fatal("setup: the guest's own /me returned no user_id")
	}

	cb := registerAndLogin(t, s, "b@example.com")

	cases := []struct {
		method string
		path   string
		body   any
	}{
		{"GET", "/api/v1/rooms/" + roomA, nil},
		{"PATCH", "/api/v1/rooms/" + roomA, map[string]any{"name": "stolen"}},
		{"DELETE", "/api/v1/rooms/" + roomA, nil},
		{"GET", "/api/v1/rooms/" + roomA + "/applications", nil},
		{"POST", "/api/v1/rooms/" + roomA + "/applications", map[string]string{"name": "x.exe", "list": "blacklist"}},
		{"DELETE", "/api/v1/rooms/" + roomA + "/applications/" + appA, nil},
		{"GET", "/api/v1/rooms/" + roomA + "/members", nil},
		{"POST", "/api/v1/rooms/" + roomA + "/members", map[string]string{"email": "b@example.com"}},
		{"DELETE", "/api/v1/rooms/" + roomA + "/members/" + guestMe.UserID, nil},
		{"PATCH", "/api/v1/computers/" + computerA, map[string]any{"display_name": "stolen"}},
		{"PATCH", "/api/v1/computers/" + computerA, map[string]any{"blocked": true}},
	}

	for _, tc := range cases {
		// The body is part of the name: two rows PATCH the same computer path
		// with different fields, and identical subtest names would leave the
		// output unable to say which one failed.
		t.Run(fmt.Sprintf("%s %s %v", tc.method, tc.path, tc.body), func(t *testing.T) {
			rr := doJSON(t, h, tc.method, tc.path, tc.body, cb)
			if rr.Code != http.StatusNotFound {
				t.Fatalf("got %d, want 404 — account B reached account A's data: %s",
					rr.Code, rr.Body.String())
			}
		})
	}
}

// Nothing in TestCrossAccountAccessIsAlways404 asserts that the rightful
// owner still gets a success response on those same paths, so a refactor
// that broke a route for everyone — not just for account B — would leave
// that suite green: every case would still be a 404. This branch broke a
// route for everyone three separate times during execution and each time
// the isolation suite noticed nothing, because it only ever checks the
// negative. This test is the positive control: account A, acting as its
// own room's owner, walks the same paths and must succeed on every one.
//
// Ordered so destructive calls (the DELETEs) run last, after every
// non-destructive case that depends on the room/application/membership
// still existing has already run.
func TestOwnerCanReachEveryRouteTheIsolationSuiteChecks(t *testing.T) {
	s := &Server{pool: testPool(t)}
	h := s.setupRoutes()

	ca := registerAndLogin(t, s, "a@example.com")
	roomA := createRoom(t, s, ca, "A's room")
	enroll(t, s, mintBindingToken(t, s, ca), "guid-owner", "PC-OWNER")

	cg := registerAndLogin(t, s, "guest-owner-test@example.com")

	var listA struct {
		Computers []struct {
			ID string `json:"id"`
		} `json:"computers"`
	}
	decodeInto(t, doJSON(t, h, "GET", "/api/v1/computers", nil, ca), &listA)
	if len(listA.Computers) != 1 {
		t.Fatalf("setup: expected one computer, got %d", len(listA.Computers))
	}
	computerA := listA.Computers[0].ID

	var appRow struct {
		ID string `json:"id"`
	}
	decodeInto(t, doJSON(t, h, "POST", "/api/v1/rooms/"+roomA+"/applications",
		map[string]string{"name": "steam.exe", "list": "blacklist"}, ca), &appRow)
	if appRow.ID == "" {
		t.Fatal("setup: the application was created but returned no id")
	}
	appA := appRow.ID

	if rr := doJSON(t, h, "POST", "/api/v1/rooms/"+roomA+"/members",
		map[string]string{"email": "guest-owner-test@example.com"}, ca); rr.Code != http.StatusCreated {
		t.Fatalf("setup: grant guest access to A's room: %d %s", rr.Code, rr.Body.String())
	}
	var guestMe struct {
		UserID string `json:"user_id"`
	}
	decodeInto(t, doJSON(t, h, "GET", "/api/v1/me", nil, cg), &guestMe)
	if guestMe.UserID == "" {
		t.Fatal("setup: the guest's own /me returned no user_id")
	}

	cases := []struct {
		method string
		path   string
		body   any
		want   int
	}{
		{"GET", "/api/v1/rooms/" + roomA, nil, http.StatusOK},
		{"PATCH", "/api/v1/rooms/" + roomA, map[string]any{"name": "renamed"}, http.StatusOK},
		{"GET", "/api/v1/rooms/" + roomA + "/applications", nil, http.StatusOK},
		{"GET", "/api/v1/rooms/" + roomA + "/members", nil, http.StatusOK},
		{"PATCH", "/api/v1/computers/" + computerA, map[string]any{"display_name": "kids-pc"}, http.StatusOK},
		{"PATCH", "/api/v1/computers/" + computerA, map[string]any{"blocked": true}, http.StatusOK},
		{"PATCH", "/api/v1/computers/" + computerA, map[string]any{"blocked": false}, http.StatusOK},
		// Destructive: order matters from here down.
		{"DELETE", "/api/v1/rooms/" + roomA + "/applications/" + appA, nil, http.StatusNoContent},
		{"DELETE", "/api/v1/rooms/" + roomA + "/members/" + guestMe.UserID, nil, http.StatusNoContent},
		{"DELETE", "/api/v1/rooms/" + roomA, nil, http.StatusNoContent},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s %s %v", tc.method, tc.path, tc.body), func(t *testing.T) {
			rr := doJSON(t, h, tc.method, tc.path, tc.body, ca)
			if rr.Code != tc.want {
				t.Fatalf("got %d, want %d — the owner was denied its own data: %s",
					rr.Code, tc.want, rr.Body.String())
			}
		})
	}
}

// Collection endpoints must not leak either: they return account B's own
// (empty) collections rather than account A's rows.
func TestCollectionsAreEmptyForANewAccount(t *testing.T) {
	s := &Server{pool: testPool(t)}
	h := s.setupRoutes()
	ca := registerAndLogin(t, s, "a@example.com")
	createRoom(t, s, ca, "A's room")
	enroll(t, s, mintBindingToken(t, s, ca), "guid-a", "PC-A")

	cb := registerAndLogin(t, s, "b@example.com")
	for _, tc := range []struct{ path, key string }{
		{"/api/v1/rooms", "rooms"},
		{"/api/v1/computers", "computers"},
		{"/api/v1/events", "events"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			rr := doJSON(t, h, "GET", tc.path, nil, cb)
			if rr.Code != 200 {
				t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
			}
			var out map[string][]any
			decodeInto(t, rr, &out)
			// The key has to be asserted present, not merely iterated: a
			// response that dropped it entirely would iterate nothing and pass,
			// which is the same vacuous green this whole suite exists to avoid.
			rows, ok := out[tc.key]
			if !ok {
				t.Fatalf("%s returned no %q key: %s", tc.path, tc.key, rr.Body.String())
			}
			if len(rows) != 0 {
				t.Fatalf("%s returned %d %s rows belonging to another account", tc.path, len(rows), tc.key)
			}
		})
	}
}

// The one cross-account reference that arrives in a request BODY rather than a
// path parameter, which is why the table above cannot express it: account B
// naming account A's room while patching its own computer. Two things must stop
// it — the GetRoom guard in the handler, and the composite foreign key from
// migration 00012 — and before this test existed neither was covered.
func TestComputerCannotBeMovedIntoAnotherAccountsRoom(t *testing.T) {
	s := &Server{pool: testPool(t)}
	h := s.setupRoutes()

	ca := registerAndLogin(t, s, "a@example.com")
	roomA := createRoom(t, s, ca, "A's room")

	cb := registerAndLogin(t, s, "b@example.com")
	enroll(t, s, mintBindingToken(t, s, cb), "guid-b", "PC-B")
	var listB struct {
		Computers []struct {
			ID string `json:"id"`
		} `json:"computers"`
	}
	decodeInto(t, doJSON(t, h, "GET", "/api/v1/computers", nil, cb), &listB)
	if len(listB.Computers) != 1 {
		t.Fatalf("account B has %d computers, want 1", len(listB.Computers))
	}

	rr := doJSON(t, h, "PATCH", "/api/v1/computers/"+listB.Computers[0].ID,
		map[string]any{"room_id": roomA}, cb)
	if rr.Code != 404 {
		t.Fatalf("account B attached its computer to account A's room: %d %s", rr.Code, rr.Body.String())
	}

	// And the machine is still where it was — a 404 that nevertheless performed
	// the move would be the worst outcome available.
	var after struct {
		Computers []struct {
			RoomID *string `json:"room_id"`
		} `json:"computers"`
	}
	decodeInto(t, doJSON(t, h, "GET", "/api/v1/computers", nil, cb), &after)
	if after.Computers[0].RoomID != nil {
		t.Fatalf("the computer was moved anyway, into room %s", *after.Computers[0].RoomID)
	}
}

// Deleting a room that still holds machines has to work, and the machines have
// to survive it unassigned. This is the behaviour the composite foreign key
// most easily breaks: the default SET NULL nulls every referencing column, so
// it would try to write account_id = NULL and the delete would fail outright.
// Nothing else in the suite deletes a non-empty room.
func TestDeletingARoomUnassignsItsComputers(t *testing.T) {
	s := &Server{pool: testPool(t)}
	h := s.setupRoutes()
	c := registerAndLogin(t, s, "parent@example.com")
	room := createRoom(t, s, c, "Kids room")
	enroll(t, s, mintBindingToken(t, s, c), "guid-1", "PC-1")

	var list struct {
		Computers []struct {
			ID string `json:"id"`
		} `json:"computers"`
	}
	decodeInto(t, doJSON(t, h, "GET", "/api/v1/computers", nil, c), &list)
	if rr := doJSON(t, h, "PATCH", "/api/v1/computers/"+list.Computers[0].ID,
		map[string]any{"room_id": room}, c); rr.Code != 200 {
		t.Fatalf("assign: %d %s", rr.Code, rr.Body.String())
	}

	if rr := doJSON(t, h, "DELETE", "/api/v1/rooms/"+room, nil, c); rr.Code != 204 {
		t.Fatalf("deleting a room with a computer in it: %d %s", rr.Code, rr.Body.String())
	}

	var after struct {
		Computers []struct {
			RoomID *string `json:"room_id"`
		} `json:"computers"`
	}
	decodeInto(t, doJSON(t, h, "GET", "/api/v1/computers", nil, c), &after)
	if len(after.Computers) != 1 {
		t.Fatalf("the computer went with the room: %d left", len(after.Computers))
	}
	if after.Computers[0].RoomID != nil {
		t.Fatalf("the computer still points at a deleted room: %s", *after.Computers[0].RoomID)
	}
}

// The composed pre-scope exposure, closed by migration 00014.
//
// Every other table in this schema fails closed when a handler forgets to
// scope itself: no scope, no rows. Four tables could not, because a scope
// legitimately does not exist yet when registration creates the first account
// and when a session asks which accounts it may enter — and until 00014 those
// policies keyed on nothing at all, so an unscoped connection could read every
// customer's account name, membership and room-sharing graph in one join.
//
// This is the direct test of that: real rows exist, belonging to three
// different users, and a connection that never scoped itself sees none of them.
func TestUnscopedConnectionCannotReadTheFleet(t *testing.T) {
	s := &Server{pool: testPool(t)}
	h := s.setupRoutes()
	ctx := context.Background()

	ca := registerAndLogin(t, s, "a@example.com")
	roomA := createRoom(t, s, ca, "A's room")
	cg := registerAndLogin(t, s, "guest@example.com")
	if rr := doJSON(t, h, "POST", "/api/v1/rooms/"+roomA+"/members",
		map[string]string{"email": "guest@example.com"}, ca); rr.Code != http.StatusCreated {
		t.Fatalf("setup: share the room: %d %s", rr.Code, rr.Body.String())
	}
	registerAndLogin(t, s, "b@example.com")

	for _, table := range []string{"accounts", "account_members", "room_members"} {
		var n int
		if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 0 {
			t.Fatalf("a connection that never scoped itself read %d rows from %s", n, table)
		}
	}

	// Not vacuous: scoped to one user, the same tables return that user's own
	// rows and nobody else's. Three accounts and one room grant exist above.
	var me struct {
		UserID string `json:"user_id"`
	}
	decodeInto(t, doJSON(t, h, "GET", "/api/v1/me", nil, cg), &me)
	guestID, err := uuidFromString(me.UserID)
	if err != nil {
		t.Fatalf("parse the guest's user id: %v", err)
	}

	var members, grants int
	if err := s.inUser(ctx, guestID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM account_members").Scan(&members); err != nil {
			return err
		}
		return tx.QueryRow(ctx, "SELECT count(*) FROM room_members").Scan(&grants)
	}); err != nil {
		t.Fatalf("scoped read: %v", err)
	}
	if members != 1 {
		t.Fatalf("the guest sees %d account_members rows, want only their own 1", members)
	}
	if grants != 1 {
		t.Fatalf("the guest sees %d room_members rows, want only their own 1", grants)
	}
}
