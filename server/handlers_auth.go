package main

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"server/internal/db"
)

const minPasswordLen = 8

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Invalid JSON"})
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if req.Email == "" || !strings.Contains(req.Email, "@") {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "A valid email is required"})
		return
	}
	if len(req.Password) < minPasswordLen {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Password must be at least 8 characters"})
		return
	}

	hash, err := hashPassword(req.Password)
	if err != nil {
		slog.Error("hash password", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Could not create the account"})
		return
	}

	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Could not create the account"})
		return
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)

	user, err := q.CreateUser(ctx, db.CreateUserParams{
		Email: req.Email, PasswordHash: hash, Name: req.Name,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			writeJSON(w, http.StatusConflict, ErrorResponse{Error: "That email is already registered"})
			return
		}
		slog.Error("create user", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Could not create the account"})
		return
	}

	accountName := req.Name
	if accountName == "" {
		accountName = req.Email
	}
	account, err := q.CreateAccount(ctx, db.CreateAccountParams{Name: accountName, OwnerUserID: user.ID})
	if err != nil {
		slog.Error("create account", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Could not create the account"})
		return
	}
	// The account now exists, so scope the rest of this transaction to it. There
	// is no unscoped INSERT policy on account_members precisely so that no code
	// path can make an arbitrary user the owner of an arbitrary account; this
	// insert has to earn its scope like every other write.
	if _, err := tx.Exec(ctx,
		`SELECT set_config('app.account_id', $1, true)`, account.ID.String()); err != nil {
		slog.Error("scope registration transaction", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Could not create the account"})
		return
	}
	if err := q.AddAccountMember(ctx, db.AddAccountMemberParams{
		AccountID: account.ID, UserID: user.ID, Role: "owner",
	}); err != nil {
		slog.Error("add owner membership", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Could not create the account"})
		return
	}
	if err := tx.Commit(ctx); err != nil {
		slog.Error("commit registration", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Could not create the account"})
		return
	}

	slog.Info("account registered", "account_id", account.ID)
	writeJSON(w, http.StatusCreated, map[string]string{
		"account_id": account.ID.String(),
		"user_id":    user.ID.String(),
	})
}
