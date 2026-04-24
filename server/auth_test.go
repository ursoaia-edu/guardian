package main

import (
	"context"
	"testing"
)

func TestPasswordRoundTrip(t *testing.T) {
	hash, err := hashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}
	if !verifyPassword(hash, "correct horse battery staple") {
		t.Fatal("correct password was rejected")
	}
	if verifyPassword(hash, "wrong password") {
		t.Fatal("wrong password was accepted")
	}
}

func TestPasswordHashesAreSalted(t *testing.T) {
	a, _ := hashPassword("same")
	b, _ := hashPassword("same")
	if a == b {
		t.Fatal("two hashes of the same password are identical; the salt is missing")
	}
}

func TestTokenHashIsStable(t *testing.T) {
	plain, hash := newToken()
	if len(plain) != 64 {
		t.Fatalf("token is %d chars, want 64", len(plain))
	}
	if hashToken(plain) != hash {
		t.Fatal("hashToken does not reproduce the hash returned by newToken")
	}
}

func TestRegisterCreatesAccountAndOwner(t *testing.T) {
	s := &Server{pool: testPool(t)}
	rr := doJSON(t, s.setupRoutes(), "POST", "/api/v1/auth/register", map[string]string{
		"email":    "parent@example.com",
		"password": "correct horse battery staple",
		"name":     "Parent",
	}, nil)
	if rr.Code != 201 {
		t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
	}

	var role string
	err := s.pool.QueryRow(context.Background(), `
		SELECT m.role FROM account_members m
		JOIN users u ON u.id = m.user_id
		WHERE u.email = $1`, "parent@example.com").Scan(&role)
	if err != nil {
		t.Fatalf("membership lookup: %v", err)
	}
	if role != "owner" {
		t.Fatalf("role is %q, want owner", role)
	}
}

func TestRegisterRejectsDuplicateEmail(t *testing.T) {
	s := &Server{pool: testPool(t)}
	body := map[string]string{"email": "dup@example.com", "password": "a-long-enough-password"}
	doJSON(t, s.setupRoutes(), "POST", "/api/v1/auth/register", body, nil)
	rr := doJSON(t, s.setupRoutes(), "POST", "/api/v1/auth/register", body, nil)
	if rr.Code != 409 {
		t.Fatalf("status %d, want 409", rr.Code)
	}
}

func TestRegisterRejectsShortPassword(t *testing.T) {
	s := &Server{pool: testPool(t)}
	rr := doJSON(t, s.setupRoutes(), "POST", "/api/v1/auth/register", map[string]string{
		"email": "short@example.com", "password": "abc",
	}, nil)
	if rr.Code != 400 {
		t.Fatalf("status %d, want 400", rr.Code)
	}
}
