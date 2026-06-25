package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A body over the cap must be a 400, not a memory-amplification 500: it is
// rejected before it ever reaches a JSON decoder.
func TestOversizedLoginBodyIsRejected(t *testing.T) {
	s := &Server{pool: testPool(t)}
	h := s.setupRoutes()

	huge := strings.Repeat("a", maxUnauthenticatedBodyBytes+1)
	body := `{"email":"a@example.com","password":"` + huge + `"}`

	req := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("oversized login body got %d, want 400: %s", rr.Code, rr.Body.String())
	}
}

func TestOversizedEnrollBodyIsRejected(t *testing.T) {
	s := &Server{pool: testPool(t)}
	h := s.setupRoutes()

	huge := strings.Repeat("a", maxUnauthenticatedBodyBytes+1)
	body := `{"machine_guid":"x","hardware":{"note":"` + huge + `"}}`

	req := httptest.NewRequest("POST", "/agent/enroll", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("oversized enroll body got %d, want 400: %s", rr.Code, rr.Body.String())
	}
}

// loginAttemptFrom sends one login attempt carrying the given IP in
// X-Forwarded-For. Whether the server believes that header is decided by
// Server.trustedProxies: the proxied tests below set it to 1, the spoofing
// test leaves it at the zero default.
func loginAttemptFrom(h http.Handler, ip string) int {
	body, _ := json.Marshal(map[string]string{"email": "nobody@example.com", "password": "whatever-wrong"})
	req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", ip)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr.Code
}

// The (loginRateLimit+1)th login attempt from the same resolved client IP
// within the window must be turned away rather than spend a full argon2id
// verification — the sharp edge R9 deliberately left open (every attempt,
// hit or miss, costs full argon2id work) closed by a limiter instead.
func TestLoginIsRateLimitedPerIP(t *testing.T) {
	s := &Server{pool: testPool(t), trustedProxies: 1}
	h := s.setupRoutes()

	var lastCode int
	for i := 0; i < loginRateLimit+1; i++ {
		lastCode = loginAttemptFrom(h, "203.0.113.7")
	}
	if lastCode != http.StatusTooManyRequests {
		t.Fatalf("request %d from the same IP got %d, want 429", loginRateLimit+1, lastCode)
	}
}

// A second, distinct client IP must not be caught by the first one's limit —
// otherwise "per IP" is a lie and one abusive client locks out every other
// family sharing the service.
func TestLoginRateLimitIsPerIPNotGlobal(t *testing.T) {
	s := &Server{pool: testPool(t), trustedProxies: 1}
	h := s.setupRoutes()

	var exhausted int
	for i := 0; i < loginRateLimit+1; i++ {
		exhausted = loginAttemptFrom(h, "203.0.113.1")
	}
	if exhausted != http.StatusTooManyRequests {
		t.Fatalf("first IP: got %d, want 429", exhausted)
	}

	if code := loginAttemptFrom(h, "203.0.113.2"); code == http.StatusTooManyRequests {
		t.Fatal("a second, distinct client IP was rate-limited by the first one's traffic")
	}
}

// With no proxy configured, X-Forwarded-For is whatever the client typed. If
// the limiter believed it anyway, a fresh forged address per request would
// hand every attempt its own untouched bucket — the limiter would exist and
// protect nothing. Every attempt here arrives from the same TCP peer
// (httptest's fixed RemoteAddr) under a different forged header, and the
// limit must still trip.
func TestSpoofedXFFDoesNotEscapeTheLimitWithoutAProxy(t *testing.T) {
	s := &Server{pool: testPool(t)} // trustedProxies == 0
	h := s.setupRoutes()

	var lastCode int
	for i := 0; i < loginRateLimit+1; i++ {
		lastCode = loginAttemptFrom(h, fmt.Sprintf("203.0.113.%d", i+1))
	}
	if lastCode != http.StatusTooManyRequests {
		t.Fatalf("request %d with a forged X-Forwarded-For got %d, want 429: the limiter trusted a header nobody vouched for", loginRateLimit+1, lastCode)
	}
}
