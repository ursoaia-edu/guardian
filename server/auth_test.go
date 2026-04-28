package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"testing"

	"golang.org/x/crypto/argon2"
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

// A hash made with different cost parameters must still verify. This is the
// property that lets the constants be retuned later without invalidating every
// password already in the database; the hash below is deliberately made with
// parameters that differ from the current constants.
func TestPasswordVerifiesAgainstItsOwnParameters(t *testing.T) {
	salt := []byte("0123456789abcdef")
	const (
		otherMemory  = 32 * 1024
		otherTime    = 2
		otherThreads = 1
	)
	key := argon2.IDKey([]byte("hunter2"), salt, otherTime, otherMemory, otherThreads, argonKeyLen)
	encoded := fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, otherMemory, otherTime, otherThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key))

	if !verifyPassword(encoded, "hunter2") {
		t.Fatal("a hash carrying its own parameters did not verify; verifyPassword is using the package constants instead")
	}
	if verifyPassword(encoded, "wrong") {
		t.Fatal("wrong password accepted")
	}
}

func TestVerifyPasswordRejectsMalformedHashes(t *testing.T) {
	for _, encoded := range []string{
		"",
		"not-a-hash",
		"argon2id$c2FsdA$a2V5",                  // the old, parameterless shape
		"$argon2id$v=19$m=65536,t=1,p=4$c2FsdA", // truncated
		"$argon2i$v=19$m=65536,t=1,p=4$c2FsdA$a2V5", // wrong variant
		"$argon2id$v=1$m=65536,t=1,p=4$c2FsdA$a2V5", // unsupported version
		"$argon2id$v=19$m=0,t=0,p=0$c2FsdA$a2V5",    // degenerate parameters
		"$argon2id$v=19$m=65536,t=1,p=4$!!!$a2V5",   // salt is not base64
		"$argon2id$v=19$m=65536,t=1,p=4$c2FsdA$",    // empty key
	} {
		if verifyPassword(encoded, "anything") {
			t.Fatalf("malformed hash %q verified", encoded)
		}
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
