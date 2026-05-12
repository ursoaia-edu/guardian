package main

import (
	"encoding/json"
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
