package main

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"server/internal/db"
)

// resetTokenTTL is short on purpose: a reset link is a bearer credential for
// an entire account, and an hour is long enough to walk to a laptop.
const resetTokenTTL = time.Hour

type forgotRequest struct {
	Email string `json:"email"`
}

type resetRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

type changePasswordRequest struct {
	Current string `json:"current"`
	New     string `json:"new"`
}

// handleForgotPassword mails a reset link, and answers 204 either way.
//
// The 204-always is not politeness: this endpoint would otherwise be an
// account-enumeration oracle, undoing the same care the registration endpoint
// takes. The dummy hash on the miss path is what keeps the timing from leaking
// what the status code does not — argon2id is the expensive part of the real
// path, so skipping it would make a miss measurably faster.
func (s *Server) handleForgotPassword(w http.ResponseWriter, r *http.Request) {
	var req forgotRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Invalid JSON"})
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))

	ctx := r.Context()
	user, err := db.New(s.pool).GetUserByEmail(ctx, email)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			slog.Error("look up user for reset", "error", err)
		}
		// Spend the same work the found path spends.
		_, _ = hashPassword("dummy-work-so-the-timing-matches")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	plain, hash := newToken()
	q := db.New(s.pool)
	if err := q.DeleteEmailTokensFor(ctx, db.DeleteEmailTokensForParams{
		UserID: user.ID, Purpose: "reset",
	}); err != nil {
		slog.Error("supersede reset tokens", "error", err)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := q.CreateEmailToken(ctx, db.CreateEmailTokenParams{
		TokenHash: hash, UserID: user.ID, Purpose: "reset", Email: user.Email,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(resetTokenTTL), Valid: true},
	}); err != nil {
		slog.Error("create reset token", "error", err)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.sendMail(resetEmail(s.cabinetOrigin, user.Email, user.Name, plain))
	w.WriteHeader(http.StatusNoContent)
}

// handleResetPassword completes a reset and signs every device out.
func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	var req resetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Token == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "This link is not valid"})
		return
	}
	if len(req.Password) < minPasswordLen {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Password must be at least 8 characters"})
		return
	}
	if len(req.Password) > maxPasswordLen {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Password is too long"})
		return
	}

	hash, err := hashPassword(req.Password)
	if err != nil {
		slog.Error("hash password", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Internal error"})
		return
	}

	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Internal error"})
		return
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)

	row, err := q.ConsumeEmailToken(ctx, db.ConsumeEmailTokenParams{
		TokenHash: hashToken(req.Token), Purpose: "reset",
	})
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			slog.Error("consume reset token", "error", err)
		}
		writeJSON(w, http.StatusBadRequest,
			ErrorResponse{Error: "This link is not valid, has already been used, or has expired"})
		return
	}
	if err := q.SetPassword(ctx, db.SetPasswordParams{ID: row.UserID, PasswordHash: hash}); err != nil {
		slog.Error("set password", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Internal error"})
		return
	}
	// Every session, including the one that may belong to whoever the account
	// is being taken back from. This is the point of server-side sessions.
	if _, err := q.DeleteSessionsForUser(ctx, row.UserID); err != nil {
		slog.Error("delete sessions after reset", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Internal error"})
		return
	}
	if err := tx.Commit(ctx); err != nil {
		slog.Error("commit reset", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Internal error"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleChangePassword changes it from inside the cabinet, keeping the caller
// signed in and signing every other device out.
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	t, ok := mustTenant(w, r)
	if !ok {
		return
	}
	var req changePasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Invalid JSON"})
		return
	}
	if len(req.New) < minPasswordLen {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Password must be at least 8 characters"})
		return
	}
	if len(req.New) > maxPasswordLen {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Password is too long"})
		return
	}

	ctx := r.Context()
	user, err := db.New(s.pool).GetUserByID(ctx, t.UserID)
	if err != nil {
		slog.Error("read user for password change", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Internal error"})
		return
	}
	if !verifyPassword(user.PasswordHash, req.Current) {
		// 403 rather than 401: the session is fine, the claim about the
		// current password is not, and a 401 would send the cabinet to the
		// sign-in screen for a mistyped field.
		writeJSON(w, http.StatusForbidden, ErrorResponse{Error: "That is not your current password"})
		return
	}

	hash, err := hashPassword(req.New)
	if err != nil {
		slog.Error("hash password", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Internal error"})
		return
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Internal error"})
		return
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)
	if err := q.SetPassword(ctx, db.SetPasswordParams{ID: user.ID, PasswordHash: hash}); err != nil {
		slog.Error("set password", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Internal error"})
		return
	}
	// Every device except this one. Signing the person out of the browser they
	// are standing in front of, as a consequence of their own deliberate act,
	// is a bug that reads as one.
	if _, err := q.DeleteOtherSessionsForUser(ctx, db.DeleteOtherSessionsForUserParams{
		UserID: user.ID, TokenHash: t.SessionTokenHash,
	}); err != nil {
		slog.Error("delete other sessions", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Internal error"})
		return
	}
	if err := tx.Commit(ctx); err != nil {
		slog.Error("commit password change", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Internal error"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
