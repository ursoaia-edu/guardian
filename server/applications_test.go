package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAddAndListRoomApplications(t *testing.T) {
	s := &Server{pool: testPool(t)}
	c := registerAndLogin(t, s, "parent@example.com")
	room := createRoom(t, s, c, "Kids room")

	rr := doJSON(t, s.setupRoutes(), "POST", "/api/v1/rooms/"+room+"/applications",
		map[string]string{"name": "steam.exe", "list": "blacklist"}, c)
	if rr.Code != 201 {
		t.Fatalf("add: %d %s", rr.Code, rr.Body.String())
	}

	rr = doJSON(t, s.setupRoutes(), "GET", "/api/v1/rooms/"+room+"/applications", nil, c)
	var out struct {
		Applications []struct {
			Name string `json:"name"`
			List string `json:"list"`
		} `json:"applications"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Applications) != 1 || out.Applications[0].Name != "steam.exe" {
		t.Fatalf("unexpected applications: %+v", out.Applications)
	}
}

func TestApplicationsOfAnotherAccountsRoomAreNotFound(t *testing.T) {
	s := &Server{pool: testPool(t)}
	ca := registerAndLogin(t, s, "a@example.com")
	roomA := createRoom(t, s, ca, "A's room")

	cb := registerAndLogin(t, s, "b@example.com")
	rr := doJSON(t, s.setupRoutes(), "POST", "/api/v1/rooms/"+roomA+"/applications",
		map[string]string{"name": "steam.exe", "list": "blacklist"}, cb)
	if rr.Code != 404 {
		t.Fatalf("account B wrote into account A's room: %d", rr.Code)
	}
}

// A blocklist you cannot remove from is not a blocklist a parent can use, and
// removing an entry is the most likely action right after adding one.
func TestDeleteRoomApplication(t *testing.T) {
	s := &Server{pool: testPool(t)}
	h := s.setupRoutes()
	c := registerAndLogin(t, s, "parent@example.com")
	room := createRoom(t, s, c, "Kids room")

	var app struct {
		ID string `json:"id"`
	}
	decodeInto(t, doJSON(t, h, "POST", "/api/v1/rooms/"+room+"/applications",
		map[string]string{"name": "steam.exe", "list": "blacklist"}, c), &app)
	if app.ID == "" {
		t.Fatal("add returned no application id")
	}

	if rr := doJSON(t, h, "DELETE", "/api/v1/rooms/"+room+"/applications/"+app.ID, nil, c); rr.Code != 204 {
		t.Fatalf("delete: %d %s", rr.Code, rr.Body.String())
	}
	// Deleting the same entry twice is a 404, not a second 204: the second call
	// removed nothing and should say so.
	if rr := doJSON(t, h, "DELETE", "/api/v1/rooms/"+room+"/applications/"+app.ID, nil, c); rr.Code != 404 {
		t.Fatalf("second delete: %d, want 404", rr.Code)
	}

	// The now-empty list must serialise as [] rather than null — this is the
	// only test that exercises orEmpty against a genuinely empty collection.
	listed := doJSON(t, h, "GET", "/api/v1/rooms/"+room+"/applications", nil, c)
	if !strings.Contains(listed.Body.String(), `"applications":[]`) {
		t.Fatalf("empty list did not serialise as an empty array: %s", listed.Body.String())
	}
}
