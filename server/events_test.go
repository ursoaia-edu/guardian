package main

import (
	"encoding/json"
	"fmt"
	"net/http"
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
	// Minting the binding token is an event too, so this asks whether the
	// enrollment is in the feed rather than whether it is alone in it.
	var found bool
	for _, e := range out.Events {
		if e.Type == "computer.enrolled" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the enrollment is not in the feed: %+v", out.Events)
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

// The feed is paged by (created_at, id), not by offset: rows arrive while
// somebody is reading, and an offset would repeat or skip them. This walks a
// feed of known size two rows at a time and demands every event exactly once.
func TestEventsPageWithoutRepeatingOrSkipping(t *testing.T) {
	s := &Server{pool: testPool(t)}
	h := s.setupRoutes()
	c := registerAndLogin(t, s, "parent@example.com")

	const rooms = 5
	for i := 0; i < rooms; i++ {
		createRoom(t, s, c, fmt.Sprintf("Room %d", i))
	}

	type page struct {
		Events []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"events"`
		NextCursor string `json:"next_cursor"`
	}

	seen := map[string]bool{}
	cursor := ""
	for requests := 0; ; requests++ {
		if requests > 20 {
			t.Fatal("paging did not terminate")
		}
		path := "/api/v1/events?limit=2"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		rr := doJSON(t, h, "GET", path, nil, c)
		if rr.Code != 200 {
			t.Fatalf("page %d: %d %s", requests, rr.Code, rr.Body.String())
		}
		var p page
		decodeInto(t, rr, &p)
		if len(p.Events) > 2 {
			t.Fatalf("limit=2 returned %d events", len(p.Events))
		}
		for _, e := range p.Events {
			if seen[e.ID] {
				t.Fatalf("event %s (%s) was returned on two different pages", e.ID, e.Type)
			}
			seen[e.ID] = true
		}
		if p.NextCursor == "" {
			break
		}
		cursor = p.NextCursor
	}

	if len(seen) != rooms {
		t.Fatalf("paged through %d events, want the %d room.created rows", len(seen), rooms)
	}
}

func TestEventsRejectsAMalformedCursor(t *testing.T) {
	s := &Server{pool: testPool(t)}
	c := registerAndLogin(t, s, "parent@example.com")
	rr := doJSON(t, s.setupRoutes(), "GET", "/api/v1/events?cursor=nonsense", nil, c)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("a malformed cursor got %d, want 400", rr.Code)
	}
}
