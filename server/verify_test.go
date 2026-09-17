package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// existingPassword is what registerAndLogin (session_test.go) registers with.
// The reset and change tests in password_test.go need to know the password
// they are replacing, so it is declared here, in the earlier task's file.
const existingPassword = "a-long-enough-password"

// tokenFromLink pulls the opaque token out of the one link in an email.
func tokenFromLink(t *testing.T, body, prefix string) string {
	t.Helper()
	i := strings.Index(body, prefix)
	if i < 0 {
		t.Fatalf("no %q in:\n%s", prefix, body)
	}
	rest := body[i+len(prefix):]
	if j := strings.IndexAny(rest, " \r\n"); j >= 0 {
		rest = rest[:j]
	}
	if rest == "" {
		t.Fatalf("empty token in:\n%s", body)
	}
	return rest
}

func TestRegistrationSendsAVerificationLinkThatWorks(t *testing.T) {
	sender := &recordingSender{}
	s := &Server{pool: testPool(t), mail: sender, cabinetOrigin: "https://guardian.example"}
	h := s.setupRoutes()

	registerAndLogin(t, s, "parent@example.com")

	msg := sender.lastTo(t, "parent@example.com")
	token := tokenFromLink(t, msg.Body, "https://guardian.example/#/verify/")

	if rr := doJSON(t, h, "POST", "/api/v1/auth/verify", map[string]string{"token": token}, nil); rr.Code != 204 {
		t.Fatalf("verify: %d %s", rr.Code, rr.Body.String())
	}

	var verified bool
	ctx := context.Background()
	if err := s.pool.QueryRow(ctx,
		`SELECT email_verified_at IS NOT NULL FROM users WHERE email = 'parent@example.com'`).Scan(&verified); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !verified {
		t.Fatal("the column that has never been written still has not been written")
	}
}

// Single use. A verification link in a mailbox somebody else later reads must
// not still work.
func TestAVerificationTokenWorksOnlyOnce(t *testing.T) {
	sender := &recordingSender{}
	s := &Server{pool: testPool(t), mail: sender, cabinetOrigin: "https://guardian.example"}
	h := s.setupRoutes()
	registerAndLogin(t, s, "parent@example.com")
	token := tokenFromLink(t, sender.lastTo(t, "parent@example.com").Body, "https://guardian.example/#/verify/")

	doJSON(t, h, "POST", "/api/v1/auth/verify", map[string]string{"token": token}, nil)
	rr := doJSON(t, h, "POST", "/api/v1/auth/verify", map[string]string{"token": token}, nil)
	if rr.Code != 400 {
		t.Fatalf("a spent token was accepted again: %d", rr.Code)
	}
}

func TestAnUnknownVerificationTokenIsRefused(t *testing.T) {
	s := &Server{pool: testPool(t), mail: &recordingSender{}, cabinetOrigin: "https://guardian.example"}
	h := s.setupRoutes()
	for _, token := range []string{"", "not-a-token", strings.Repeat("a", 64)} {
		if rr := doJSON(t, h, "POST", "/api/v1/auth/verify", map[string]string{"token": token}, nil); rr.Code != 400 {
			t.Errorf("token %q answered %d, want 400", token, rr.Code)
		}
	}
}

// The gate. An unverified account can do everything except the two things that
// reach outside it; installers are the one that exists today.
func TestAnUnverifiedAccountCannotMintABindingToken(t *testing.T) {
	sender := &recordingSender{}
	s := &Server{pool: testPool(t), mail: sender, cabinetOrigin: "https://guardian.example"}
	h := s.setupRoutes()
	c := registerAndLogin(t, s, "parent@example.com")

	if rr := doJSON(t, h, "POST", "/api/v1/binding-tokens", nil, c); rr.Code != 403 {
		t.Fatalf("an unverified account minted an installer token: %d %s", rr.Code, rr.Body.String())
	}

	// Everything else still works: blocking the whole cabinet would mean a
	// customer who mistypes their address cannot see what they bought.
	if rr := doJSON(t, h, "GET", "/api/v1/rooms", nil, c); rr.Code != 200 {
		t.Fatalf("an unverified account could not read its rooms: %d", rr.Code)
	}
	if rr := doJSON(t, h, "POST", "/api/v1/rooms", map[string]string{"name": "Kids"}, c); rr.Code != 201 {
		t.Fatalf("an unverified account could not create a room: %d", rr.Code)
	}

	token := tokenFromLink(t, sender.lastTo(t, "parent@example.com").Body, "https://guardian.example/#/verify/")
	doJSON(t, h, "POST", "/api/v1/auth/verify", map[string]string{"token": token}, nil)

	if rr := doJSON(t, h, "POST", "/api/v1/binding-tokens", nil, c); rr.Code != 201 {
		t.Fatalf("a verified account still could not mint: %d %s", rr.Code, rr.Body.String())
	}
}

// A link eaten by a spam filter must not be the end of the story, because
// verification gates handing out installers.
func TestVerificationCanBeResent(t *testing.T) {
	sender := &recordingSender{}
	s := &Server{pool: testPool(t), mail: sender, cabinetOrigin: "https://guardian.example"}
	h := s.setupRoutes()
	c := registerAndLogin(t, s, "parent@example.com")

	before := len(sender.messages())
	if rr := doJSON(t, h, "POST", "/api/v1/account/verify/resend", nil, c); rr.Code != 204 {
		t.Fatalf("resend: %d %s", rr.Code, rr.Body.String())
	}
	for i := 0; i < 200 && len(sender.messages()) == before; i++ {
		waitABit()
	}
	if len(sender.messages()) <= before {
		t.Fatal("resend sent nothing")
	}

	// The newest link works, which also proves the old one was replaced rather
	// than accumulating.
	msgs := sender.messages()
	token := tokenFromLink(t, msgs[len(msgs)-1].Body, "https://guardian.example/#/verify/")
	if rr := doJSON(t, h, "POST", "/api/v1/auth/verify", map[string]string{"token": token}, nil); rr.Code != 204 {
		t.Fatalf("the resent link did not work: %d", rr.Code)
	}
}

// Nobody else's address. The token is bearer proof of one mailbox; a session
// is irrelevant to it.
func TestVerifyingIsNotDoneWithASession(t *testing.T) {
	sender := &recordingSender{}
	s := &Server{pool: testPool(t), mail: sender, cabinetOrigin: "https://guardian.example"}
	h := s.setupRoutes()
	registerAndLogin(t, s, "a@example.com")
	other := registerAndLogin(t, s, "b@example.com")
	token := tokenFromLink(t, sender.lastTo(t, "a@example.com").Body, "https://guardian.example/#/verify/")

	// B's session, A's token: A gets verified, because the token is what
	// proves the mailbox.
	if rr := doJSON(t, h, "POST", "/api/v1/auth/verify", map[string]string{"token": token}, other); rr.Code != 204 {
		t.Fatalf("verify with another session: %d", rr.Code)
	}
	ctx := context.Background()
	var aVerified, bVerified bool
	if err := s.pool.QueryRow(ctx,
		`SELECT
		   (SELECT email_verified_at IS NOT NULL FROM users WHERE email='a@example.com'),
		   (SELECT email_verified_at IS NOT NULL FROM users WHERE email='b@example.com')`).
		Scan(&aVerified, &bVerified); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !aVerified || bVerified {
		t.Fatalf("verified the wrong user: a=%v b=%v", aVerified, bVerified)
	}
}

// Mail is best-effort by design, and registration is not.
func TestRegistrationSucceedsWhenMailFails(t *testing.T) {
	sender := &recordingSender{err: http.ErrServerClosed}
	s := &Server{pool: testPool(t), mail: sender, cabinetOrigin: "https://guardian.example"}
	h := s.setupRoutes()

	rr := doJSON(t, h, "POST", "/api/v1/auth/register",
		map[string]string{"email": "parent@example.com", "password": existingPassword}, nil)
	if rr.Code != 201 {
		t.Fatalf("registration failed because mail did: %d %s", rr.Code, rr.Body.String())
	}
}
