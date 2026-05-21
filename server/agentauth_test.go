package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
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

// assignToRoom moves the account's only computer into a room, returning its id.
func assignToRoom(t *testing.T, s *Server, c *http.Cookie, room string) string {
	t.Helper()
	h := s.setupRoutes()
	var list struct {
		Computers []struct {
			ID string `json:"id"`
		} `json:"computers"`
	}
	decodeInto(t, doJSON(t, h, "GET", "/api/v1/computers", nil, c), &list)
	if len(list.Computers) != 1 {
		t.Fatalf("expected one computer, got %d", len(list.Computers))
	}
	id := list.Computers[0].ID
	if rr := doJSON(t, h, "PATCH", "/api/v1/computers/"+id,
		map[string]any{"room_id": room}, c); rr.Code != 200 {
		t.Fatalf("assign: %d %s", rr.Code, rr.Body.String())
	}
	return id
}

// Protection off is one of the three fail-closed branches and the one a parent
// toggles most often — it is the master switch for a whole room.
func TestSyncWithProtectionOffEnforcesNothing(t *testing.T) {
	s := &Server{pool: testPool(t)}
	h := s.setupRoutes()
	c := registerAndLogin(t, s, "parent@example.com")
	room := createRoom(t, s, c, "Kids room")
	doJSON(t, h, "POST", "/api/v1/rooms/"+room+"/applications",
		map[string]string{"name": "steam.exe", "list": "blacklist"}, c)

	binding := mintBindingToken(t, s, c)
	_, agentToken := enroll(t, s, binding, "guid-1", "PC-1")
	assignToRoom(t, s, c, room)

	// The room has a rule but protection was never switched on.
	code, out := agentSync(t, s, agentToken)
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	if out.Mode != "free" || len(out.Applications) != 0 {
		t.Fatalf("protection is off but the agent was told to enforce %q with %d applications",
			out.Mode, len(out.Applications))
	}
}

// Blocking a computer must LOCK it, not unmanage it. Expressed in the wire
// format the agent already speaks: whitelist mode with an empty list allows
// nothing but the processes the agent protects unconditionally. If this ever
// returns "free", pressing "block this computer" in the cabinet switches
// protection off — the exact opposite of the button.
func TestSyncOfBlockedComputerLocksIt(t *testing.T) {
	s := &Server{pool: testPool(t)}
	h := s.setupRoutes()
	c := registerAndLogin(t, s, "parent@example.com")
	room := createRoom(t, s, c, "Kids room")
	doJSON(t, h, "PATCH", "/api/v1/rooms/"+room, map[string]any{"protection_enabled": true}, c)

	binding := mintBindingToken(t, s, c)
	_, agentToken := enroll(t, s, binding, "guid-1", "PC-1")
	id := assignToRoom(t, s, c, room)

	if rr := doJSON(t, h, "PATCH", "/api/v1/computers/"+id,
		map[string]any{"blocked": true}, c); rr.Code != 200 {
		t.Fatalf("block: %d %s", rr.Code, rr.Body.String())
	}

	code, out := agentSync(t, s, agentToken)
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	if out.Mode != "whitelist" {
		t.Fatalf("a blocked computer was told to run in %q mode; blocking must lock it", out.Mode)
	}
	if len(out.Applications) != 0 {
		t.Fatalf("a locked computer was given %d allowed applications", len(out.Applications))
	}
}

// Telemetry must never be able to stop enforcement. Both of these values are
// valid JSON that Postgres refuses in a jsonb column; before readRuntime parsed
// rather than merely validated, either one turned a sync into a permanent 500
// and left the machine with no policy at all.
func TestMalformedRuntimeDoesNotBreakSync(t *testing.T) {
	s := &Server{pool: testPool(t)}
	h := s.setupRoutes()
	c := registerAndLogin(t, s, "parent@example.com")
	room := createRoom(t, s, c, "Kids room")
	doJSON(t, h, "PATCH", "/api/v1/rooms/"+room, map[string]any{"protection_enabled": true}, c)
	doJSON(t, h, "POST", "/api/v1/rooms/"+room+"/applications",
		map[string]string{"name": "steam.exe", "list": "blacklist"}, c)

	binding := mintBindingToken(t, s, c)
	_, agentToken := enroll(t, s, binding, "guid-1", "PC-1")
	assignToRoom(t, s, c, room)

	// Built from bytes rather than written as an escape: a literal NUL escape
	// in source is exactly the thing editors and tooling silently rewrite.
	nulEscape := string([]byte{'\\', 'u', '0', '0', '0', '0'})

	for _, runtime := range []string{
		`{"user":"a` + nulEscape + `b"}`,                // legal JSON, rejected by jsonb
		`{"user":"` + string([]byte{0xff, 0xfe}) + `"}`, // invalid UTF-8 bytes
		`"not an object"`,
		`{ broken`,
	} {
		req := httptest.NewRequest("GET", "/agent/sync?runtime="+url.QueryEscape(runtime), nil)
		req.Header.Set("Authorization", "Bearer "+agentToken)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != 200 {
			t.Fatalf("runtime %q broke the sync: %d %s", runtime, rec.Code, rec.Body.String())
		}
		var out ClientSyncResponse
		decodeInto(t, rec, &out)
		if out.Mode != "blacklist" || len(out.Applications) != 1 {
			t.Fatalf("runtime %q cost the machine its policy: mode %q, %d applications",
				runtime, out.Mode, len(out.Applications))
		}
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
