package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"server/internal/db"
)

// sessionTokenFrom reads the session token from the cookie (browser) or the
// Authorization header (mobile). One token, two delivery forms.
func sessionTokenFrom(r *http.Request) string {
	if c, err := r.Cookie(sessionCookieName); err == nil && c.Value != "" {
		return c.Value
	}
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return ""
}

// SessionAuth resolves the session, picks the account the user belongs to, and
// puts a Tenant in the context. account_id is derived here and nowhere else.
func (s *Server) SessionAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		plain := sessionTokenFrom(r)
		if plain == "" {
			writeJSON(w, http.StatusUnauthorized, ErrorResponse{Error: "Not signed in"})
			return
		}
		ctx := r.Context()
		q := db.New(s.pool)

		session, err := q.GetSession(ctx, hashToken(plain))
		if err != nil {
			// No rows is a genuinely unknown or expired token. Anything else is
			// a database problem, not a fact about this session — reporting it
			// as an expired session tells every signed-in user their session
			// expired during an outage, and hides the outage in the logs behind
			// what looks like mass sign-out. Same rule as writeLookupError.
			if !errors.Is(err, pgx.ErrNoRows) {
				slog.Error("look up session", "error", err)
				writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Internal error"})
				return
			}
			writeJSON(w, http.StatusUnauthorized, ErrorResponse{Error: "Not signed in"})
			return
		}
		accounts, err := q.ListAccessibleAccounts(ctx, session.UserID)
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				slog.Error("list accessible accounts", "user_id", session.UserID, "error", err)
				writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Internal error"})
				return
			}
			slog.Warn("session without an account", "user_id", session.UserID)
			writeJSON(w, http.StatusForbidden, ErrorResponse{Error: "No account"})
			return
		}
		if len(accounts) == 0 {
			slog.Warn("session without an account", "user_id", session.UserID)
			writeJSON(w, http.StatusForbidden, ErrorResponse{Error: "No account"})
			return
		}
		// Sliding renewal: an active parent is never signed out mid-use.
		if err := q.TouchSession(ctx, db.TouchSessionParams{
			TokenHash: session.TokenHash,
			ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(sessionTTL), Valid: true},
		}); err != nil {
			slog.Error("touch session", "error", err)
		}

		// Task 6 picked accounts[0] and noted that was "exactly right while every
		// user has one account". This task ends that: a guest registers normally,
		// so they OWN an account of their own and are a MEMBER of the one whose
		// room was shared with them — and `owner` sorts first, so the default
		// would send an invited grandmother to her own empty account and she
		// would never see the room she was given. The caller therefore names the
		// account this request acts in.
		//
		// This is not "account_id from the request" in the sense the global
		// constraint forbids. The constraint exists so a caller cannot reach an
		// account they have no claim on; here the header can only SELECT among
		// memberships the session already proved, and an unmatched value is a
		// 404 — whether an account exists is not something a non-member is
		// entitled to learn.
		selected := accounts[0]
		if requested := r.Header.Get(accountHeader); requested != "" {
			id, err := uuid.Parse(requested)
			if err != nil {
				writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "Account not found"})
				return
			}
			found := false
			for _, a := range accounts {
				if a.AccountID == id {
					selected, found = a, true
					break
				}
			}
			if !found {
				slog.Warn("account selection rejected", "user_id", session.UserID)
				writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "Account not found"})
				return
			}
		}

		ctx = withTenant(ctx, Tenant{
			AccountID: selected.AccountID,
			UserID:    session.UserID,
			Role:      selected.Role,
		})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// AgentAuth resolves the agent's own token to a computer, and from it the
// account. The agent never names its account or its identity.
func (s *Server) AgentAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		if !strings.HasPrefix(h, "Bearer ") {
			writeJSON(w, http.StatusUnauthorized, ErrorResponse{Error: "Unauthorized"})
			return
		}
		hash := hashToken(strings.TrimPrefix(h, "Bearer "))

		// Two steps on purpose. The first is the only unscoped read in the agent
		// path and it returns a bare account id — see 00010_agent_lookup.sql for
		// why that is not an unscoped SELECT policy on computers. Raw pgx rather
		// than a generated query: this is an authentication primitive, not a
		// domain query, and it has no place in the sqlc surface.
		// Scanned into a POINTER on purpose. An unknown token makes the function
		// return SQL NULL, and pgx scans NULL into a plain uuid.UUID without
		// error — leaving the zero uuid and no signal. The pointer stays nil
		// instead, so an unknown token is rejected here, in one round trip,
		// rather than falling through to a second lookup that was always going
		// to find nothing. That also keeps a genuine database failure a 5xx
		// instead of dressing it up as a bad credential.
		var accountID *uuid.UUID
		if err := s.pool.QueryRow(r.Context(),
			`SELECT account_for_agent_token($1)`, hash).Scan(&accountID); err != nil {
			slog.Error("resolve agent token", "remote", r.RemoteAddr, "error", err)
			writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Internal error"})
			return
		}
		if accountID == nil {
			slog.Warn("agent with an unknown token", "remote", r.RemoteAddr)
			writeJSON(w, http.StatusUnauthorized, ErrorResponse{Error: "Unauthorized"})
			return
		}

		// Everything after this point is scoped like any other query.
		var computer db.Computer
		if err := s.inAccount(r.Context(), *accountID, func(tx pgx.Tx) error {
			var err error
			computer, err = db.New(tx).GetComputerByTokenHash(r.Context(), hash)
			return err
		}); err != nil {
			// Reaching here means the token resolved to an account a moment ago
			// but the row is now unreadable — a deleted computer, or a failure.
			// Either way the agent is not authenticated, but the error is worth
			// keeping: without it this line cannot be told apart from a bogus
			// token in the logs.
			slog.Warn("agent token resolved but its computer did not", "remote", r.RemoteAddr, "error", err)
			writeJSON(w, http.StatusUnauthorized, ErrorResponse{Error: "Unauthorized"})
			return
		}
		ctx := withTenant(r.Context(), Tenant{AccountID: computer.AccountID})
		ctx = context.WithValue(ctx, computerKey, computer)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
