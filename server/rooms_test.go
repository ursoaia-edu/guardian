package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

func createRoom(t *testing.T, s *Server, c *http.Cookie, name string) string {
	t.Helper()
	rr := doJSON(t, s.setupRoutes(), "POST", "/api/v1/rooms", map[string]string{"name": name}, c)
	if rr.Code != 201 {
		t.Fatalf("create room: %d %s", rr.Code, rr.Body.String())
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode room: %v", err)
	}
	return out.ID
}

func TestCreateAndListRooms(t *testing.T) {
	s := &Server{pool: testPool(t)}
	c := registerAndLogin(t, s, "parent@example.com")
	createRoom(t, s, c, "Kids room")

	rr := doJSON(t, s.setupRoutes(), "GET", "/api/v1/rooms", nil, c)
	if rr.Code != 200 {
		t.Fatalf("list: %d", rr.Code)
	}
	var out struct {
		Rooms []struct {
			Name string `json:"name"`
			Mode string `json:"mode"`
		} `json:"rooms"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Rooms) != 1 || out.Rooms[0].Name != "Kids room" {
		t.Fatalf("unexpected rooms: %+v", out.Rooms)
	}
	if out.Rooms[0].Mode != "blacklist" {
		t.Fatalf("default mode is %q, want blacklist", out.Rooms[0].Mode)
	}
}

func TestRoomOfAnotherAccountIsNotFound(t *testing.T) {
	s := &Server{pool: testPool(t)}
	ca := registerAndLogin(t, s, "a@example.com")
	roomA := createRoom(t, s, ca, "A's room")

	cb := registerAndLogin(t, s, "b@example.com")
	rr := doJSON(t, s.setupRoutes(), "GET", "/api/v1/rooms/"+roomA, nil, cb)
	if rr.Code != 404 {
		t.Fatalf("account B got %d for account A's room, want 404", rr.Code)
	}
}

// Deleting is the second write path this task adds, and RLS has to hide a
// foreign room from it just as thoroughly as from a read — otherwise one
// account can destroy another's rooms while being told they do not exist.
func TestDeleteRoom(t *testing.T) {
	s := &Server{pool: testPool(t)}
	h := s.setupRoutes()
	ca := registerAndLogin(t, s, "a@example.com")
	roomA := createRoom(t, s, ca, "A's room")

	cb := registerAndLogin(t, s, "b@example.com")
	if rr := doJSON(t, h, "DELETE", "/api/v1/rooms/"+roomA, nil, cb); rr.Code != 404 {
		t.Fatalf("account B got %d deleting account A's room, want 404", rr.Code)
	}

	// The room must still be there: a 404 that actually deleted the row would
	// be the worst possible outcome, and only this second check catches it.
	if rr := doJSON(t, h, "GET", "/api/v1/rooms/"+roomA, nil, ca); rr.Code != 200 {
		t.Fatalf("account A's room is gone after B's delete attempt: %d", rr.Code)
	}

	if rr := doJSON(t, h, "DELETE", "/api/v1/rooms/"+roomA, nil, ca); rr.Code != 204 {
		t.Fatalf("owner delete: %d", rr.Code)
	}
	if rr := doJSON(t, h, "GET", "/api/v1/rooms/"+roomA, nil, ca); rr.Code != 404 {
		t.Fatalf("room still readable after deletion: %d", rr.Code)
	}
}
