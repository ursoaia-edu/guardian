package main

import (
	"fmt"
	"net/http"
	"testing"
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
