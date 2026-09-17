package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"server/internal/db"
)

// verifyTokenTTL is how long a confirmation link lives. Longer than a reset
// link because it is far less dangerous — it proves a mailbox, it does not
// open an account — and because a registration email is often read the next
// day.
const verifyTokenTTL = 48 * time.Hour

// sendVerification mints a link and mails it, superseding any previous one.
// It is called after the caller's transaction has committed.
func (s *Server) sendVerification(ctx context.Context, userID uuid.UUID, email, name string) {
	plain, hash := newToken()
	q := db.New(s.pool)
	if err := q.DeleteEmailTokensFor(ctx, db.DeleteEmailTokensForParams{
		UserID: userID, Purpose: "verify",
	}); err != nil {
		slog.Error("supersede verification tokens", "error", err)
		return
	}
	if err := q.CreateEmailToken(ctx, db.CreateEmailTokenParams{
		TokenHash: hash, UserID: userID, Purpose: "verify", Email: email,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(verifyTokenTTL), Valid: true},
	}); err != nil {
		slog.Error("create verification token", "error", err)
		return
	}
	s.sendMail(verifyEmail(s.cabinetOrigin, email, name, plain))
}

// emailVerified is the gate in front of the two actions that reach outside the
// account. It writes the 403 itself and returns false, so a caller is one `if`
// — the shape that gets copied correctly into the third caller, whenever
// invitations land.
func (s *Server) emailVerified(w http.ResponseWriter, r *http.Request, t Tenant) bool {
	user, err := db.New(s.pool).GetUserByID(r.Context(), t.UserID)
	if err != nil {
		slog.Error("read user for the verification gate", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Internal error"})
		return false
	}
	if !user.EmailVerifiedAt.Valid {
		writeJSON(w, http.StatusForbidden, ErrorResponse{
			Error: "Confirm your email address before installing the agent on a computer"})
		return false
	}
	return true
}

type tokenRequest struct {
	Token string `json:"token"`
}

// handleVerifyEmail consumes a confirmation link. Unauthenticated on purpose:
// the token is what proves the mailbox, and the link is often opened in a
// browser that has never signed in.
func (s *Server) handleVerifyEmail(w http.ResponseWriter, r *http.Request) {
	var req tokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Token == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "This confirmation link is not valid"})
		return
	}

	ctx := r.Context()
	row, err := db.New(s.pool).ConsumeEmailToken(ctx, db.ConsumeEmailTokenParams{
		TokenHash: hashToken(req.Token), Purpose: "verify",
	})
	if err != nil {
		// Unknown, spent and expired are one answer. Distinguishing them tells
		// somebody holding a stolen link which kind of wrong it is.
		if !errors.Is(err, pgx.ErrNoRows) {
			slog.Error("consume verification token", "error", err)
		}
		writeJSON(w, http.StatusBadRequest,
			ErrorResponse{Error: "This confirmation link is not valid or has already been used"})
		return
	}

	if _, err := db.New(s.pool).MarkEmailVerified(ctx, db.MarkEmailVerifiedParams{
		ID: row.UserID, Email: row.Email,
	}); err != nil {
		slog.Error("mark email verified", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Internal error"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleResendVerification sends the link again to the signed-in user's own
// address. Rate-limited at the route, and a no-op for an address that is
// already confirmed.
func (s *Server) handleResendVerification(w http.ResponseWriter, r *http.Request) {
	t, ok := mustTenant(w, r)
	if !ok {
		return
	}
	user, err := db.New(s.pool).GetUserByID(r.Context(), t.UserID)
	if err != nil {
		slog.Error("read user for resend", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Internal error"})
		return
	}
	if user.EmailVerifiedAt.Valid {
		// Already done. Answering 204 keeps the cabinet's code path simple and
		// tells a re-clicker nothing they did not know.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.sendVerification(r.Context(), user.ID, user.Email, user.Name)
	w.WriteHeader(http.StatusNoContent)
}
