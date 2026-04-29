package main

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

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
