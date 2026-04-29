package main

import (
	"net/http"
	"testing"
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
