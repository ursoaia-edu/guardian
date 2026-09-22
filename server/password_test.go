package main

import (
	"context"
	"testing"
)

const newPassword = "a-much-better-password"

func TestForgotSendsALinkThatResetsThePassword(t *testing.T) {
	sender := &recordingSender{}
	s := &Server{pool: testPool(t), mail: sender, cabinetOrigin: "https://guardian.example"}
	h := s.setupRoutes()
	registerAndLogin(t, s, "parent@example.com")

	if rr := doJSON(t, h, "POST", "/api/v1/auth/password/forgot",
		map[string]string{"email": "parent@example.com"}, nil); rr.Code != 204 {
		t.Fatalf("forgot: %d %s", rr.Code, rr.Body.String())
	}
	token := sender.waitForLink(t, "parent@example.com", "https://guardian.example/#/reset/")

	if rr := doJSON(t, h, "POST", "/api/v1/auth/password/reset",
		map[string]string{"token": token, "password": newPassword}, nil); rr.Code != 204 {
		t.Fatalf("reset: %d %s", rr.Code, rr.Body.String())
	}

	// The new one works and the old one does not.
	if rr := doJSON(t, h, "POST", "/api/v1/auth/login",
		map[string]string{"email": "parent@example.com", "password": newPassword}, nil); rr.Code != 200 {
		t.Fatalf("login with the new password: %d", rr.Code)
	}
	if rr := doJSON(t, h, "POST", "/api/v1/auth/login",
		map[string]string{"email": "parent@example.com", "password": existingPassword}, nil); rr.Code == 200 {
		t.Fatal("the old password still works")
	}
}

// Account enumeration: the registration endpoint already treats it as a bug to
// avoid, and a reset endpoint that leaks it undoes that.
func TestForgotAnswersTheSameForAnUnknownAddress(t *testing.T) {
	sender := &recordingSender{}
	s := &Server{pool: testPool(t), mail: sender, cabinetOrigin: "https://guardian.example"}
	h := s.setupRoutes()
	registerAndLogin(t, s, "known@example.com")

	known := doJSON(t, h, "POST", "/api/v1/auth/password/forgot", map[string]string{"email": "known@example.com"}, nil)
	unknown := doJSON(t, h, "POST", "/api/v1/auth/password/forgot", map[string]string{"email": "nobody@example.com"}, nil)

	if known.Code != 204 || unknown.Code != 204 {
		t.Fatalf("statuses differ: known %d, unknown %d", known.Code, unknown.Code)
	}
	if known.Body.String() != unknown.Body.String() {
		t.Fatalf("bodies differ: %q vs %q", known.Body.String(), unknown.Body.String())
	}
	// And nothing was sent to the address that does not exist.
	for _, m := range sender.messages() {
		if m.To == "nobody@example.com" {
			t.Fatal("mailed an address with no account")
		}
	}
}

// A reset is the one moment somebody might be taking an account back from
// whoever has been in it.
func TestACompletedResetEndsEverySession(t *testing.T) {
	sender := &recordingSender{}
	s := &Server{pool: testPool(t), mail: sender, cabinetOrigin: "https://guardian.example"}
	h := s.setupRoutes()
	first := registerAndLogin(t, s, "parent@example.com")

	doJSON(t, h, "POST", "/api/v1/auth/password/forgot", map[string]string{"email": "parent@example.com"}, nil)
	token := sender.waitForLink(t, "parent@example.com", "https://guardian.example/#/reset/")
	doJSON(t, h, "POST", "/api/v1/auth/password/reset",
		map[string]string{"token": token, "password": newPassword}, nil)

	if rr := doJSON(t, h, "GET", "/api/v1/me", nil, first); rr.Code != 401 {
		t.Fatalf("a session survived the reset: %d", rr.Code)
	}
}

func TestAResetTokenWorksOnlyOnceAndExpires(t *testing.T) {
	sender := &recordingSender{}
	s := &Server{pool: testPool(t), mail: sender, cabinetOrigin: "https://guardian.example"}
	h := s.setupRoutes()
	registerAndLogin(t, s, "parent@example.com")
	doJSON(t, h, "POST", "/api/v1/auth/password/forgot", map[string]string{"email": "parent@example.com"}, nil)
	token := sender.waitForLink(t, "parent@example.com", "https://guardian.example/#/reset/")

	doJSON(t, h, "POST", "/api/v1/auth/password/reset", map[string]string{"token": token, "password": newPassword}, nil)
	rr := doJSON(t, h, "POST", "/api/v1/auth/password/reset",
		map[string]string{"token": token, "password": "another-password-entirely"}, nil)
	if rr.Code != 400 {
		t.Fatalf("a spent reset token was accepted: %d", rr.Code)
	}

	// And an expired one is refused. Backdating is done on the pool, which the
	// handler path never does.
	doJSON(t, h, "POST", "/api/v1/auth/password/forgot", map[string]string{"email": "parent@example.com"}, nil)
	fresh := sender.waitForLink(t, "parent@example.com", "https://guardian.example/#/reset/")
	if _, err := s.pool.Exec(context.Background(),
		`UPDATE email_tokens SET expires_at = now() - interval '1 minute' WHERE purpose = 'reset'`); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	if rr := doJSON(t, h, "POST", "/api/v1/auth/password/reset",
		map[string]string{"token": fresh, "password": newPassword}, nil); rr.Code != 400 {
		t.Fatalf("an expired reset token was accepted: %d", rr.Code)
	}
}

func TestChangingThePasswordKeepsTheCallersOwnSession(t *testing.T) {
	s := &Server{pool: testPool(t), mail: &recordingSender{}, cabinetOrigin: "https://guardian.example"}
	h := s.setupRoutes()
	mine := registerAndLogin(t, s, "parent@example.com")
	// A second sign-in for the same person: a phone, say.
	other := loginAs(t, s, "parent@example.com", existingPassword)

	if rr := doJSON(t, h, "POST", "/api/v1/account/password",
		map[string]string{"current": existingPassword, "new": newPassword}, mine); rr.Code != 204 {
		t.Fatalf("change: %d %s", rr.Code, rr.Body.String())
	}

	if rr := doJSON(t, h, "GET", "/api/v1/me", nil, mine); rr.Code != 200 {
		t.Fatalf("the caller was signed out by their own password change: %d", rr.Code)
	}
	if rr := doJSON(t, h, "GET", "/api/v1/me", nil, other); rr.Code != 401 {
		t.Fatalf("the other device kept its session: %d", rr.Code)
	}
}

func TestChangingThePasswordNeedsTheCurrentOne(t *testing.T) {
	s := &Server{pool: testPool(t), mail: &recordingSender{}, cabinetOrigin: "https://guardian.example"}
	h := s.setupRoutes()
	c := registerAndLogin(t, s, "parent@example.com")

	if rr := doJSON(t, h, "POST", "/api/v1/account/password",
		map[string]string{"current": "not-the-password", "new": newPassword}, c); rr.Code != 403 {
		t.Fatalf("changed the password without the current one: %d", rr.Code)
	}
	if rr := doJSON(t, h, "POST", "/api/v1/account/password",
		map[string]string{"current": existingPassword, "new": "short"}, c); rr.Code != 400 {
		t.Fatalf("accepted a password below the minimum: %d", rr.Code)
	}
}
