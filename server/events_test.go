package main

import (
	"encoding/json"
	"testing"
)

func TestEnrollmentIsRecorded(t *testing.T) {
	s := &Server{pool: testPool(t)}
	c := registerAndLogin(t, s, "parent@example.com")
	binding := mintBindingToken(t, s, c)
	enroll(t, s, binding, "guid-1", "PC-1")

	rr := doJSON(t, s.setupRoutes(), "GET", "/api/v1/events", nil, c)
	if rr.Code != 200 {
		t.Fatalf("status %d", rr.Code)
	}
	var out struct {
		Events []struct {
			Type string `json:"type"`
		} `json:"events"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Events) != 1 || out.Events[0].Type != "computer.enrolled" {
		t.Fatalf("events: %+v", out.Events)
	}
}

func TestEventsOfAnotherAccountAreInvisible(t *testing.T) {
	s := &Server{pool: testPool(t)}
	ca := registerAndLogin(t, s, "a@example.com")
	enroll(t, s, mintBindingToken(t, s, ca), "guid-a", "PC-A")

	cb := registerAndLogin(t, s, "b@example.com")
	rr := doJSON(t, s.setupRoutes(), "GET", "/api/v1/events", nil, cb)
	var out struct {
		Events []json.RawMessage `json:"events"`
	}
	json.Unmarshal(rr.Body.Bytes(), &out)
	if len(out.Events) != 0 {
		t.Fatalf("account B sees %d of account A's events", len(out.Events))
	}
}
