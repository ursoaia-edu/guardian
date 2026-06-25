package main

import (
	"net/http"
	"testing"
)

func TestHealthReportsOKWithADatabase(t *testing.T) {
	s := &Server{pool: testPool(t)}
	rr := doJSON(t, s.setupRoutes(), "GET", "/health", nil, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("health with a live database got %d: %s", rr.Code, rr.Body.String())
	}
}

// The whole point of pinging: a server that cannot reach Postgres must not
// tell its orchestrator it is fine.
func TestHealthReportsAnUnreachableDatabase(t *testing.T) {
	pool := testPool(t)
	s := &Server{pool: pool}
	h := s.setupRoutes()
	pool.Close() // idempotent, so testPool's own Cleanup is unaffected

	rr := doJSON(t, h, "GET", "/health", nil, nil)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("health with a closed pool got %d, want 503: %s", rr.Code, rr.Body.String())
	}
}
