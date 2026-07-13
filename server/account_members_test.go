package main

import (
	"net/http"
	"testing"
)

// The admin role has existed in the schema and in the manager check since the
// beginning with no way to grant it: an account had exactly one manager,
// permanently. This is the whole path — grant, act, revoke.
func TestAdminCanBeGrantedAndRevoked(t *testing.T) {
	s := &Server{pool: testPool(t)}
	h := s.setupRoutes()
	owner := registerAndLogin(t, s, "parent@example.com")
	account := accountIDOf(t, s, "parent@example.com")

	// A second person with an account of their own, as everyone has.
	partner := registerAndLogin(t, s, "partner@example.com")
	if rr := asAccount(t, h, "POST", "/api/v1/rooms", map[string]string{"name": "Nope"},
		partner, account); rr.Code != http.StatusNotFound {
		t.Fatalf("a stranger reached the account before being added: %d %s", rr.Code, rr.Body.String())
	}

	if rr := doJSON(t, h, "POST", "/api/v1/account/members",
		map[string]string{"email": "partner@example.com"}, owner); rr.Code != http.StatusCreated {
		t.Fatalf("add admin: %d %s", rr.Code, rr.Body.String())
	}

	// An admin manages the account: a manager-only route, in the other
	// person's account, named with the header.
	if rr := asAccount(t, h, "POST", "/api/v1/rooms", map[string]string{"name": "Study"},
		partner, account); rr.Code != http.StatusCreated {
		t.Fatalf("the new admin could not create a room: %d %s", rr.Code, rr.Body.String())
	}

	var members struct {
		Members []struct {
			ID    string `json:"id"`
			Email string `json:"email"`
			Role  string `json:"role"`
		} `json:"members"`
	}
	decodeInto(t, doJSON(t, h, "GET", "/api/v1/account/members", nil, owner), &members)
	if len(members.Members) != 2 {
		t.Fatalf("account holds %d members, want the owner and the admin: %+v", len(members.Members), members.Members)
	}
	if members.Members[0].Role != roleOwner {
		t.Fatalf("the owner is not listed first: %+v", members.Members)
	}

	var adminID string
	for _, m := range members.Members {
		if m.Email == "partner@example.com" {
			adminID = m.ID
			if m.Role != roleAdmin {
				t.Fatalf("the added member has role %q, want admin", m.Role)
			}
		}
	}
	if adminID == "" {
		t.Fatal("the added admin is not in the member list")
	}

	if rr := doJSON(t, h, "DELETE", "/api/v1/account/members/"+adminID, nil, owner); rr.Code != http.StatusNoContent {
		t.Fatalf("remove admin: %d %s", rr.Code, rr.Body.String())
	}
	// Revoked means revoked: the account is no longer theirs to name.
	if rr := asAccount(t, h, "POST", "/api/v1/rooms", map[string]string{"name": "After"},
		partner, account); rr.Code != http.StatusNotFound {
		t.Fatalf("a removed admin still reached the account: %d %s", rr.Code, rr.Body.String())
	}
}

// An account with no owner has nobody who can be billed or delete it, and
// ownership is not transferable in v1 — so the owner row is refused in SQL
// rather than by a check some later handler could skip.
func TestTheOwnerCannotBeRemoved(t *testing.T) {
	s := &Server{pool: testPool(t)}
	h := s.setupRoutes()
	owner := registerAndLogin(t, s, "parent@example.com")

	var me struct {
		UserID string `json:"user_id"`
	}
	decodeInto(t, doJSON(t, h, "GET", "/api/v1/me", nil, owner), &me)

	if rr := doJSON(t, h, "DELETE", "/api/v1/account/members/"+me.UserID, nil, owner); rr.Code != http.StatusNotFound {
		t.Fatalf("the owner removed themselves: %d %s", rr.Code, rr.Body.String())
	}
	// Still in charge.
	if rr := doJSON(t, h, "POST", "/api/v1/rooms", map[string]string{"name": "Still here"}, owner); rr.Code != http.StatusCreated {
		t.Fatalf("the owner lost their account: %d %s", rr.Code, rr.Body.String())
	}
}

// /me feeds an account switcher, so each account must appear once, with a
// name. The UNION behind it yields one row per PATH to an account, so a user
// who is both an admin of an account and a guest in one of its rooms used to
// see it listed twice.
func TestMeListsEachAccountOnceWithItsName(t *testing.T) {
	s := &Server{pool: testPool(t)}
	h := s.setupRoutes()
	owner := registerAndLogin(t, s, "parent@example.com")
	account := accountIDOf(t, s, "parent@example.com")
	room := createRoom(t, s, owner, "Kids room")

	partner := registerAndLogin(t, s, "partner@example.com")
	if rr := doJSON(t, h, "POST", "/api/v1/account/members",
		map[string]string{"email": "partner@example.com"}, owner); rr.Code != http.StatusCreated {
		t.Fatalf("add admin: %d %s", rr.Code, rr.Body.String())
	}
	// The same person is now also a guest of one of that account's rooms:
	// two paths to one account.
	if rr := doJSON(t, h, "POST", "/api/v1/rooms/"+room+"/members",
		map[string]string{"email": "partner@example.com"}, owner); rr.Code != http.StatusCreated {
		t.Fatalf("grant room: %d %s", rr.Code, rr.Body.String())
	}

	var me struct {
		Accounts []struct {
			AccountID string `json:"account_id"`
			Name      string `json:"name"`
			Role      string `json:"role"`
		} `json:"accounts"`
	}
	decodeInto(t, doJSON(t, h, "GET", "/api/v1/me", nil, partner), &me)

	seen := map[string]string{}
	for _, a := range me.Accounts {
		if prev, dup := seen[a.AccountID]; dup {
			t.Fatalf("account %s listed twice (%s and %s)", a.AccountID, prev, a.Role)
		}
		seen[a.AccountID] = a.Role
		if a.Name == "" {
			t.Fatalf("account %s has no name, so a switcher cannot label it", a.AccountID)
		}
	}
	if len(me.Accounts) != 2 {
		t.Fatalf("expected their own account and the shared one, got %+v", me.Accounts)
	}
	// The stronger role wins: admin, not guest.
	if seen[account.String()] != roleAdmin {
		t.Fatalf("the shared account resolved to role %q, want admin", seen[account.String()])
	}
}
