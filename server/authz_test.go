package main

import (
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// guestReachableRoutes is the API's guest surface, written out. Every route
// under /api/v1 that is NOT in this set must sit behind ManagerOnly, and
// TestEveryAPIRouteIsClassified holds the router to exactly that.
//
// This is the list that rulings R21 and R22 were both about: a handler added
// after the guest role existed, without the guard. Being a table, adding a
// route now breaks a test until somebody decides which side it belongs on.
var guestReachableRoutes = map[string]bool{
	"GET /api/v1/me":                     true,
	"POST /api/v1/account/verify/resend": true,
	// A guest changes their own password like anybody else: the route touches
	// the caller's own user row and no account-wide state, which is why it is
	// not manager-only despite the /account/ prefix.
	"POST /api/v1/account/password":                      true,
	"GET /api/v1/rooms":                                  true,
	"GET /api/v1/rooms/{roomID}":                         true,
	"PATCH /api/v1/rooms/{roomID}":                       true,
	"GET /api/v1/rooms/{roomID}/applications":            true,
	"POST /api/v1/rooms/{roomID}/applications":           true,
	"PATCH /api/v1/rooms/{roomID}/applications/{appID}":  true,
	"DELETE /api/v1/rooms/{roomID}/applications/{appID}": true,
	"GET /api/v1/rooms/{roomID}/members":                 true,
	"GET /api/v1/rooms/{roomID}/process-events":          true,
	"GET /api/v1/computers":                              true,
	"GET /api/v1/computers/{computerID}":                 true,
	"PATCH /api/v1/computers/{computerID}":               true,
	"GET /api/v1/computers/{computerID}/process-events":  true,
}

// managerOnlyRoutes is the account-wide surface. It is a list of concrete
// requests rather than patterns because TestGuestIsRefusedEveryManagerRoute
// actually issues them.
func managerOnlyRoutes(roomID, userID, computerID string) []struct {
	pattern string
	method  string
	path    string
	body    any
} {
	return []struct {
		pattern string
		method  string
		path    string
		body    any
	}{
		{"POST /api/v1/rooms", "POST", "/api/v1/rooms", map[string]string{"name": "x"}},
		{"DELETE /api/v1/rooms/{roomID}", "DELETE", "/api/v1/rooms/" + roomID, nil},
		{"POST /api/v1/rooms/{roomID}/members", "POST", "/api/v1/rooms/" + roomID + "/members",
			map[string]string{"email": "someone@example.com"}},
		{"DELETE /api/v1/rooms/{roomID}/members/{userID}", "DELETE",
			"/api/v1/rooms/" + roomID + "/members/" + userID, nil},
		{"GET /api/v1/installer", "GET", "/api/v1/installer", nil},
		{"POST /api/v1/binding-tokens", "POST", "/api/v1/binding-tokens", nil},
		{"DELETE /api/v1/binding-tokens", "DELETE", "/api/v1/binding-tokens", nil},
		{"GET /api/v1/events", "GET", "/api/v1/events", nil},
		{"DELETE /api/v1/computers/{computerID}", "DELETE", "/api/v1/computers/" + computerID, nil},
		{"GET /api/v1/account/members", "GET", "/api/v1/account/members", nil},
		{"POST /api/v1/account/members", "POST", "/api/v1/account/members",
			map[string]string{"email": "someone@example.com"}},
		{"DELETE /api/v1/account/members/{userID}", "DELETE",
			"/api/v1/account/members/" + userID, nil},
	}
}

// A guest holds a real, granted room in the account it is acting in, so a 403
// here can only come from the role check — not from the room being invisible,
// which is what the isolation suite already covers.
func TestGuestIsRefusedEveryManagerRoute(t *testing.T) {
	s := &Server{pool: testPool(t)}
	h := s.setupRoutes()
	owner := registerAndLogin(t, s, "parent@example.com")
	shared := createRoom(t, s, owner, "Shared room")
	guest := addGuest(t, s, owner, shared, "grandma@example.com")
	ownerAccount := accountIDOf(t, s, "parent@example.com")

	var me struct {
		UserID string `json:"user_id"`
	}
	decodeInto(t, doJSON(t, h, "GET", "/api/v1/me", nil, guest), &me)

	enroll(t, s, mintBindingToken(t, s, owner), "guid-authz", "PC-AUTHZ")
	var list struct {
		Computers []struct {
			ID string `json:"id"`
		} `json:"computers"`
	}
	decodeInto(t, doJSON(t, h, "GET", "/api/v1/computers", nil, owner), &list)
	if len(list.Computers) != 1 {
		t.Fatalf("setup: expected one computer, got %d", len(list.Computers))
	}

	for _, tc := range managerOnlyRoutes(shared, me.UserID, list.Computers[0].ID) {
		t.Run(tc.pattern, func(t *testing.T) {
			rr := asAccount(t, h, tc.method, tc.path, tc.body, guest, ownerAccount)
			if rr.Code != http.StatusForbidden {
				t.Fatalf("a room guest got %d from an account-wide route, want 403: %s",
					rr.Code, rr.Body.String())
			}
		})
	}

	// Not vacuous: the same guest, in the same account, still reaches its own
	// room. A ManagerOnly accidentally applied to everything would otherwise
	// pass every case above.
	if rr := asAccount(t, h, "GET", "/api/v1/rooms/"+shared, nil, guest, ownerAccount); rr.Code != 200 {
		t.Fatalf("the guest lost access to their own room: %d %s", rr.Code, rr.Body.String())
	}
}

// The classification itself. Walking the router means a route added to
// routes.go and to neither list fails here, which is the property the
// per-handler guard could not have: nothing about forgetting a line is
// visible, but a missing row in a table is.
func TestEveryAPIRouteIsClassified(t *testing.T) {
	s := &Server{}

	managed := map[string]bool{}
	for _, tc := range managerOnlyRoutes("r", "u", "c") {
		managed[tc.pattern] = true
	}

	var unclassified []string
	err := chi.Walk(s.setupRoutes(),
		func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
			route = strings.TrimSuffix(route, "/")
			if !strings.HasPrefix(route, "/api/v1/") && route != "/api/v1" {
				return nil // unauthenticated and agent routes are not role-gated
			}
			if strings.HasPrefix(route, "/api/v1/auth/") {
				return nil // no session yet, so no role to check
			}
			key := method + " " + route
			if !guestReachableRoutes[key] && !managed[key] {
				unclassified = append(unclassified, key)
			}
			return nil
		})
	if err != nil {
		t.Fatalf("walk routes: %v", err)
	}
	if len(unclassified) > 0 {
		sort.Strings(unclassified)
		t.Fatalf("these routes are in neither guestReachableRoutes nor managerOnlyRoutes, "+
			"so nobody has said whether a room guest may reach them:\n  %s",
			strings.Join(unclassified, "\n  "))
	}

	// And the reverse: a route removed from the router but left in a list
	// would make the table a lie.
	registered := map[string]bool{}
	_ = chi.Walk(s.setupRoutes(),
		func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
			registered[method+" "+strings.TrimSuffix(route, "/")] = true
			return nil
		})
	for key := range guestReachableRoutes {
		if !registered[key] {
			t.Errorf("guestReachableRoutes names %q, which the router does not serve", key)
		}
	}
	for key := range managed {
		if !registered[key] {
			t.Errorf("managerOnlyRoutes names %q, which the router does not serve", key)
		}
	}
}
