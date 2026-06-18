package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// registerAndLogin creates an account and returns its session cookie.
func registerAndLogin(t *testing.T, s *Server, email string) *http.Cookie {
	t.Helper()
	h := s.setupRoutes()
	body := map[string]string{"email": email, "password": "a-long-enough-password"}
	if rr := doJSON(t, h, "POST", "/api/v1/auth/register", body, nil); rr.Code != 201 {
		t.Fatalf("register: %d %s", rr.Code, rr.Body.String())
	}
	rr := doJSON(t, h, "POST", "/api/v1/auth/login", body, nil)
	if rr.Code != 200 {
		t.Fatalf("login: %d %s", rr.Code, rr.Body.String())
	}
	for _, c := range rr.Result().Cookies() {
		if c.Name == sessionCookieName {
			return c
		}
	}
	t.Fatal("login did not set a session cookie")
	return nil
}

func TestLoginSetsHttpOnlyCookie(t *testing.T) {
	s := &Server{pool: testPool(t)}
	c := registerAndLogin(t, s, "parent@example.com")
	if !c.HttpOnly {
		t.Fatal("session cookie is not HttpOnly")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Fatal("session cookie is not SameSite=Lax")
	}
}

func TestLoginRejectsWrongPassword(t *testing.T) {
	s := &Server{pool: testPool(t)}
	h := s.setupRoutes()
	doJSON(t, h, "POST", "/api/v1/auth/register",
		map[string]string{"email": "p@example.com", "password": "a-long-enough-password"}, nil)
	rr := doJSON(t, h, "POST", "/api/v1/auth/login",
		map[string]string{"email": "p@example.com", "password": "wrong-password-here"}, nil)
	if rr.Code != 401 {
		t.Fatalf("status %d, want 401", rr.Code)
	}
}

func TestMeRequiresSession(t *testing.T) {
	s := &Server{pool: testPool(t)}
	rr := doJSON(t, s.setupRoutes(), "GET", "/api/v1/me", nil, nil)
	if rr.Code != 401 {
		t.Fatalf("status %d, want 401", rr.Code)
	}
}

func TestMeReturnsTheAccount(t *testing.T) {
	s := &Server{pool: testPool(t)}
	c := registerAndLogin(t, s, "parent@example.com")
	rr := doJSON(t, s.setupRoutes(), "GET", "/api/v1/me", nil, c)
	if rr.Code != 200 {
		t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
	}
}

func TestLogoutRevokesTheSession(t *testing.T) {
	s := &Server{pool: testPool(t)}
	h := s.setupRoutes()
	c := registerAndLogin(t, s, "parent@example.com")
	if rr := doJSON(t, h, "POST", "/api/v1/auth/logout", nil, c); rr.Code != 204 {
		t.Fatalf("logout: %d", rr.Code)
	}
	if rr := doJSON(t, h, "GET", "/api/v1/me", nil, c); rr.Code != 401 {
		t.Fatalf("revoked session still works: %d", rr.Code)
	}
}

// Expiry is enforced by `AND expires_at > now()` in GetSession, and nothing
// else guards it. Without this test that clause can be deleted and every other
// test still passes.
func TestExpiredSessionIsRejected(t *testing.T) {
	s := &Server{pool: testPool(t)}
	c := registerAndLogin(t, s, "parent@example.com")

	// Back-date the session through the owner connection: guardian_app can
	// update sessions, but doing it here keeps the test honest about what it
	// is simulating — the passage of time, not an application action.
	pool, err := pgxpool.New(context.Background(), os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("connect as owner: %v", err)
	}
	defer pool.Close()
	if _, err := pool.Exec(context.Background(),
		`UPDATE sessions SET expires_at = now() - interval '1 minute'`); err != nil {
		t.Fatalf("expire the session: %v", err)
	}

	if rr := doJSON(t, s.setupRoutes(), "GET", "/api/v1/me", nil, c); rr.Code != 401 {
		t.Fatalf("expired session was accepted: %d", rr.Code)
	}
}

// The Authorization header is a second, independent delivery path for the same
// session, and it is the one the mobile client uses.
func TestBearerTokenAuthenticates(t *testing.T) {
	s := &Server{pool: testPool(t)}
	h := s.setupRoutes()
	body := map[string]string{"email": "parent@example.com", "password": "a-long-enough-password"}
	if rr := doJSON(t, h, "POST", "/api/v1/auth/register", body, nil); rr.Code != 201 {
		t.Fatalf("register: %d %s", rr.Code, rr.Body.String())
	}
	rr := doJSON(t, h, "POST", "/api/v1/auth/login", body, nil)
	var out struct {
		Token string `json:"token"`
	}
	decodeInto(t, rr, &out)
	if out.Token == "" {
		t.Fatal("login returned no bearer token")
	}

	req := httptest.NewRequest("GET", "/api/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+out.Token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("bearer token rejected: %d %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest("GET", "/api/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+strings.Repeat("0", 64))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("forged bearer token accepted: %d", rec.Code)
	}
}

// The property every remaining task depends on: a session resolves to its own
// user's account and to no one else's.
func TestSessionResolvesToItsOwnAccount(t *testing.T) {
	s := &Server{pool: testPool(t)}
	h := s.setupRoutes()
	ca := registerAndLogin(t, s, "a@example.com")
	cb := registerAndLogin(t, s, "b@example.com")

	var a, b struct {
		AccountID string `json:"account_id"`
	}
	decodeInto(t, doJSON(t, h, "GET", "/api/v1/me", nil, ca), &a)
	decodeInto(t, doJSON(t, h, "GET", "/api/v1/me", nil, cb), &b)

	if a.AccountID == "" || b.AccountID == "" {
		t.Fatalf("missing account id: %q %q", a.AccountID, b.AccountID)
	}
	if a.AccountID == b.AccountID {
		t.Fatalf("two accounts resolved to the same id %s", a.AccountID)
	}
}

// A database outage during session lookup is not the same fact as "this
// session expired", and must not be reported as one: the same defect R9,
// R10 and R15 already closed in handleLogin, writeLookupError and
// handleEnroll would otherwise survive on the one path every authenticated
// request takes, telling every signed-in parent their session expired during
// a thirty-second Postgres blip and sending them all back to the sign-in
// screen at once.
func TestSessionAuthReportsOutageAsAnOutage(t *testing.T) {
	s := &Server{pool: testPool(t)}
	c := registerAndLogin(t, s, "parent@example.com")

	// Simulate the database becoming unreachable mid-session: GetSession's
	// query now fails with something other than pgx.ErrNoRows.
	s.pool.Close()

	rr := doJSON(t, s.setupRoutes(), "GET", "/api/v1/me", nil, c)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("a database outage during session lookup got %d %q, want 500",
			rr.Code, rr.Body.String())
	}
}
