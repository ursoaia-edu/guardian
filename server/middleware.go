package main

import (
	"context"
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
			writeJSON(w, http.StatusUnauthorized, ErrorResponse{Error: "Session expired"})
			return
		}
		accounts, err := q.ListAccountsForUser(ctx, session.UserID)
		if err != nil || len(accounts) == 0 {
			slog.Warn("session without an account", "user_id", session.UserID, "error", err)
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

		ctx = withTenant(ctx, Tenant{AccountID: accounts[0].ID, UserID: session.UserID})
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
