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
	decodeInto(t, doJSON(t, h, "POST", "/api/v1/rooms/"+roomA+"/applications",
		map[string]string{"name": "steam.exe", "list": "blacklist"}, ca), &appRow)
	appA := appRow.ID
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
		t.Run(fmt.Sprintf("%s %s", tc.method, tc.path), func(t *testing.T) {
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
	for _, path := range []string{"/api/v1/rooms", "/api/v1/computers", "/api/v1/events"} {
		t.Run(path, func(t *testing.T) {
			var out map[string][]any
			decodeInto(t, doJSON(t, h, "GET", path, nil, cb), &out)
			for key, rows := range out {
				if len(rows) != 0 {
					t.Fatalf("%s returned %d %s rows belonging to another account", path, len(rows), key)
				}
			}
		})
	}
}
