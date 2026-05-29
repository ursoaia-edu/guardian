package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// addGuest registers a second user and grants them one room of the first
// account. It returns the guest's session cookie.
func addGuest(t *testing.T, s *Server, owner *http.Cookie, roomID, email string) *http.Cookie {
	t.Helper()
	h := s.setupRoutes()
	body := map[string]string{"email": email, "password": "a-long-enough-password"}
	if rr := doJSON(t, h, "POST", "/api/v1/auth/register", body, nil); rr.Code != 201 {
		t.Fatalf("register guest: %d %s", rr.Code, rr.Body.String())
	}
	if rr := doJSON(t, h, "POST", "/api/v1/rooms/"+roomID+"/members",
		map[string]string{"email": email}, owner); rr.Code != 201 {
		t.Fatalf("add member: %d %s", rr.Code, rr.Body.String())
	}
	rr := doJSON(t, h, "POST", "/api/v1/auth/login", body, nil)
	for _, c := range rr.Result().Cookies() {
		if c.Name == sessionCookieName {
			return c
		}
	}
	t.Fatal("guest login set no cookie")
	return nil
}

// asAccount issues a request as a user acting in a named account. A guest owns
// an account of their own — registration gives everyone one — so reaching the
// account whose room was shared with them means saying which account they mean.
func asAccount(t *testing.T, h http.Handler, method, path string, body any, c *http.Cookie, account uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		r = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, r)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(accountHeader, account.String())
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// Selecting an account is a choice among proven memberships, never a way to
// reach a new one. Without this test the header would be an authorisation hole
// wearing the clothes of a convenience feature.
func TestAccountHeaderCannotReachAnotherAccount(t *testing.T) {
	s := &Server{pool: testPool(t)}
	h := s.setupRoutes()
	ca := registerAndLogin(t, s, "a@example.com")
	registerAndLogin(t, s, "b@example.com")
	accountB := accountIDOf(t, s, "b@example.com")

	if rr := asAccount(t, h, "GET", "/api/v1/rooms", nil, ca, accountB); rr.Code != 404 {
		t.Fatalf("account A selected account B and got %d, want 404", rr.Code)
	}
}

func TestGuestSeesOnlyTheGrantedRoom(t *testing.T) {
	s := &Server{pool: testPool(t)}
	h := s.setupRoutes()
	owner := registerAndLogin(t, s, "parent@example.com")
	shared := createRoom(t, s, owner, "Shared room")
	createRoom(t, s, owner, "Private room")

	guest := addGuest(t, s, owner, shared, "grandma@example.com")
	ownerAccount := accountIDOf(t, s, "parent@example.com")

	var out struct {
		Rooms []struct {
			Name string `json:"name"`
		} `json:"rooms"`
	}
	decodeInto(t, asAccount(t, h, "GET", "/api/v1/rooms", nil, guest, ownerAccount), &out)
	if len(out.Rooms) != 1 || out.Rooms[0].Name != "Shared room" {
		t.Fatalf("guest sees %+v; only the granted room may be visible", out.Rooms)
	}
}

func TestGuestCannotOpenAnUngrantedRoom(t *testing.T) {
	s := &Server{pool: testPool(t)}
	h := s.setupRoutes()
	owner := registerAndLogin(t, s, "parent@example.com")
	shared := createRoom(t, s, owner, "Shared room")
	private := createRoom(t, s, owner, "Private room")
	guest := addGuest(t, s, owner, shared, "grandma@example.com")
	ownerAccount := accountIDOf(t, s, "parent@example.com")

	if rr := asAccount(t, h, "GET", "/api/v1/rooms/"+private, nil, guest, ownerAccount); rr.Code != 404 {
		t.Fatalf("guest opened an ungranted room: %d", rr.Code)
	}
}

func TestGuestSeesOnlyTheGrantedRoomsComputers(t *testing.T) {
	s := &Server{pool: testPool(t)}
	h := s.setupRoutes()
	owner := registerAndLogin(t, s, "parent@example.com")
	shared := createRoom(t, s, owner, "Shared room")
	guest := addGuest(t, s, owner, shared, "grandma@example.com")

	// One machine in the shared room, one left in the pool.
	enroll(t, s, mintBindingToken(t, s, owner), "guid-1", "PC-SHARED")
	enroll(t, s, mintBindingToken(t, s, owner), "guid-2", "PC-POOL")
	var pool struct {
		Computers []struct {
			ID       string `json:"id"`
			Hostname string `json:"hostname"`
		} `json:"computers"`
	}
	decodeInto(t, doJSON(t, h, "GET", "/api/v1/computers", nil, owner), &pool)
	for _, c := range pool.Computers {
		if c.Hostname == "PC-SHARED" {
			doJSON(t, h, "PATCH", "/api/v1/computers/"+c.ID, map[string]any{"room_id": shared}, owner)
		}
	}

	var seen struct {
		Computers []struct {
			Hostname string `json:"hostname"`
		} `json:"computers"`
	}
	ownerAccount := accountIDOf(t, s, "parent@example.com")
	decodeInto(t, asAccount(t, h, "GET", "/api/v1/computers", nil, guest, ownerAccount), &seen)
	if len(seen.Computers) != 1 || seen.Computers[0].Hostname != "PC-SHARED" {
		t.Fatalf("guest sees %+v; the unassigned pool must stay hidden", seen.Computers)
	}
}

func TestGuestCannotMintBindingTokens(t *testing.T) {
	s := &Server{pool: testPool(t)}
	h := s.setupRoutes()
	owner := registerAndLogin(t, s, "parent@example.com")
	shared := createRoom(t, s, owner, "Shared room")
	guest := addGuest(t, s, owner, shared, "grandma@example.com")

	ownerAccount := accountIDOf(t, s, "parent@example.com")
	if rr := asAccount(t, h, "POST", "/api/v1/binding-tokens", nil, guest, ownerAccount); rr.Code != 403 {
		t.Fatalf("guest minted a binding token: %d", rr.Code)
	}
}

func TestAddingAnUnknownEmailIsNotFound(t *testing.T) {
	s := &Server{pool: testPool(t)}
	owner := registerAndLogin(t, s, "parent@example.com")
	room := createRoom(t, s, owner, "Shared room")

	rr := doJSON(t, s.setupRoutes(), "POST", "/api/v1/rooms/"+room+"/members",
		map[string]string{"email": "nobody@example.com"}, owner)
	if rr.Code != 404 {
		t.Fatalf("status %d, want 404", rr.Code)
	}
}

// Revocation has to actually revoke. A grant that cannot be taken back outlives
// the reason it was given, and the only remedy left would be deleting the room
// the family actually uses.
func TestRemovingARoomMemberRevokesAccess(t *testing.T) {
	s := &Server{pool: testPool(t)}
	h := s.setupRoutes()
	owner := registerAndLogin(t, s, "parent@example.com")
	shared := createRoom(t, s, owner, "Shared room")
	guest := addGuest(t, s, owner, shared, "grandma@example.com")

	// The guest can see the room while the grant stands.
	var before struct {
		Rooms []json.RawMessage `json:"rooms"`
	}
	ownerAccount := accountIDOf(t, s, "parent@example.com")
	decodeInto(t, asAccount(t, h, "GET", "/api/v1/rooms", nil, guest, ownerAccount), &before)
	if len(before.Rooms) != 1 {
		t.Fatalf("guest sees %d rooms before revocation, want 1", len(before.Rooms))
	}

	var members struct {
		Members []struct {
			ID string `json:"id"`
		} `json:"members"`
	}
	decodeInto(t, doJSON(t, h, "GET", "/api/v1/rooms/"+shared+"/members", nil, owner), &members)
	if len(members.Members) != 1 {
		t.Fatalf("expected one member, got %d", len(members.Members))
	}

	if rr := doJSON(t, h, "DELETE",
		"/api/v1/rooms/"+shared+"/members/"+members.Members[0].ID, nil, owner); rr.Code != 204 {
		t.Fatalf("remove member: %d %s", rr.Code, rr.Body.String())
	}

	// The grant is gone, so naming that account is no longer something the
	// guest may do at all — the header now selects an account they have no
	// membership in, which is a 404 like any other unreachable account.
	if rr := asAccount(t, h, "GET", "/api/v1/rooms", nil, guest, ownerAccount); rr.Code != 404 {
		t.Fatalf("a removed guest can still act in the account: %d", rr.Code)
	}
	// And their own account, which they fall back to, holds none of its rooms.
	var after struct {
		Rooms []json.RawMessage `json:"rooms"`
	}
	decodeInto(t, doJSON(t, h, "GET", "/api/v1/rooms", nil, guest), &after)
	if len(after.Rooms) != 0 {
		t.Fatalf("a removed guest still sees %d rooms", len(after.Rooms))
	}
}
