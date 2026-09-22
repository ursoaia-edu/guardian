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

// loginAs signs an existing user in again, for tests about a second device.
func loginAs(t *testing.T, s *Server, email, password string) *http.Cookie {
	t.Helper()
	rr := doJSON(t, s.setupRoutes(), "POST", "/api/v1/auth/login",
		map[string]string{"email": email, "password": password}, nil)
	if rr.Code != 200 {
		t.Fatalf("login as %s: %d %s", email, rr.Code, rr.Body.String())
	}
	for _, c := range rr.Result().Cookies() {
		if c.Name == sessionCookieName {
			return c
		}
	}
	t.Fatalf("login as %s set no session cookie", email)
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
	// The token in the body is what the mobile client asks for by name.
	mobile := map[string]string{"email": "parent@example.com", "password": "a-long-enough-password", "client": clientMobile}
	rr := doJSON(t, h, "POST", "/api/v1/auth/login", mobile, nil)
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

// A browser login must not be able to read its own session token out of the
// response: returning it there hands script on the cabinet's origin the exact
// credential the HttpOnly cookie exists to keep away from it.
func TestBrowserLoginDoesNotReturnTheTokenInTheBody(t *testing.T) {
	s := &Server{pool: testPool(t)}
	h := s.setupRoutes()
	body := map[string]string{"email": "parent@example.com", "password": "a-long-enough-password"}
	if rr := doJSON(t, h, "POST", "/api/v1/auth/register", body, nil); rr.Code != 201 {
		t.Fatalf("register: %d %s", rr.Code, rr.Body.String())
	}

	rr := doJSON(t, h, "POST", "/api/v1/auth/login", body, nil)
	if rr.Code != 200 {
		t.Fatalf("login: %d %s", rr.Code, rr.Body.String())
	}
	var out map[string]string
	decodeInto(t, rr, &out)
	if out["token"] != "" {
		t.Fatal("a login with no client kind returned the session token in the body")
	}

	// ...and the cookie it did set still works, so nothing was traded away.
	var cookie *http.Cookie
	for _, c := range rr.Result().Cookies() {
		if c.Name == sessionCookieName {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("login set no session cookie")
	}
	if me := doJSON(t, h, "GET", "/api/v1/me", nil, cookie); me.Code != 200 {
		t.Fatalf("the cookie from a browser login was rejected: %d %s", me.Code, me.Body.String())
	}
}

// The cap applies to authenticated routes too, not just the three
// unauthenticated ones it was originally written for.
func TestOversizedBodyIsRejectedOnAnAuthenticatedRoute(t *testing.T) {
	s := &Server{pool: testPool(t)}
	h := s.setupRoutes()
	c := registerAndLogin(t, s, "parent@example.com")

	huge := strings.Repeat("a", maxRequestBodyBytes+1)
	rr := doJSON(t, h, "POST", "/api/v1/rooms", map[string]string{"name": huge}, c)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("an oversized authenticated body got %d, want 400: %s", rr.Code, rr.Body.String())
	}
}

// The cabinet names which of the caller's proven memberships a request acts in
// with X-Guardian-Account. A browser will not send a header the preflight did
// not allow, so leaving it out of AllowedHeaders breaks the guest flow
// entirely — and does it silently, in the browser, where no server test would
// notice.
func TestPreflightAllowsTheAccountHeader(t *testing.T) {
	t.Setenv("CABINET_ORIGIN", "https://cabinet.example.com")
	s := &Server{pool: testPool(t)}

	req := httptest.NewRequest("OPTIONS", "/api/v1/rooms", nil)
	req.Header.Set("Origin", "https://cabinet.example.com")
	req.Header.Set("Access-Control-Request-Method", "GET")
	req.Header.Set("Access-Control-Request-Headers", accountHeader)
	rr := httptest.NewRecorder()
	s.setupRoutes().ServeHTTP(rr, req)

	allowed := rr.Header().Get("Access-Control-Allow-Headers")
	if !strings.Contains(strings.ToLower(allowed), strings.ToLower(accountHeader)) {
		t.Fatalf("preflight did not allow %s; Access-Control-Allow-Headers = %q", accountHeader, allowed)
	}
	if rr.Header().Get("Access-Control-Allow-Origin") != "https://cabinet.example.com" {
		t.Fatalf("preflight did not allow the cabinet origin: %q", rr.Header().Get("Access-Control-Allow-Origin"))
	}
}
