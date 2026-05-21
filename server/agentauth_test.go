package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func agentSync(t *testing.T, s *Server, agentToken string) (int, ClientSyncResponse) {
	t.Helper()
	req := httptest.NewRequest("GET", "/agent/sync", nil)
	req.Header.Set("Authorization", "Bearer "+agentToken)
	rr := httptest.NewRecorder()
	s.setupRoutes().ServeHTTP(rr, req)
	var out ClientSyncResponse
	json.Unmarshal(rr.Body.Bytes(), &out)
	return rr.Code, out
}

func TestSyncRejectsUnknownToken(t *testing.T) {
	s := &Server{pool: testPool(t)}
	code, _ := agentSync(t, s, "not-a-real-token")
	if code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", code)
	}
}

func TestSyncOfUnassignedComputerEnforcesNothing(t *testing.T) {
	s := &Server{pool: testPool(t)}
	c := registerAndLogin(t, s, "parent@example.com")
	binding := mintBindingToken(t, s, c)
	_, agentToken := enroll(t, s, binding, "guid-1", "PC-1")

	code, out := agentSync(t, s, agentToken)
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	if out.Mode != "free" {
		t.Fatalf("mode is %q; a computer in no room must enforce nothing", out.Mode)
	}
	if len(out.Applications) != 0 {
		t.Fatalf("got %d applications for an unassigned computer", len(out.Applications))
	}
}

func TestSyncReturnsTheRoomPolicy(t *testing.T) {
	s := &Server{pool: testPool(t)}
	h := s.setupRoutes()
	c := registerAndLogin(t, s, "parent@example.com")
	room := createRoom(t, s, c, "Kids room")
	doJSON(t, h, "POST", "/api/v1/rooms/"+room+"/applications",
		map[string]string{"name": "steam.exe", "list": "blacklist"}, c)
	doJSON(t, h, "PATCH", "/api/v1/rooms/"+room, map[string]any{"protection_enabled": true}, c)

	binding := mintBindingToken(t, s, c)
	_, agentToken := enroll(t, s, binding, "guid-1", "PC-1")

	var id string
	rr := doJSON(t, h, "GET", "/api/v1/computers", nil, c)
	var list struct {
		Computers []struct {
			ID string `json:"id"`
		} `json:"computers"`
	}
	json.Unmarshal(rr.Body.Bytes(), &list)
	id = list.Computers[0].ID
	doJSON(t, h, "PATCH", "/api/v1/computers/"+id, map[string]any{"room_id": room}, c)

	code, out := agentSync(t, s, agentToken)
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	if out.Mode != "blacklist" {
		t.Fatalf("mode is %q, want blacklist", out.Mode)
	}
	if len(out.Applications) != 1 || out.Applications[0].Name != "steam.exe" {
		t.Fatalf("applications: %+v", out.Applications)
	}
}
