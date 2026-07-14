package main

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"server/internal/db"

	"github.com/jackc/pgx/v5/pgconn"
)

// The account's own members — the owner and any admins. Distinct from
// /rooms/{roomID}/members, which grants one room to a guest and nothing else.
//
// The admin role has been in the schema since 00002 and in requireManager's
// allow-list since it existed, with no way to give it to anybody: an account
// had exactly one manager, forever, and "my partner should be able to do this
// too" had no answer. These three handlers are that answer.

func (s *Server) handleListAccountMembers(w http.ResponseWriter, r *http.Request) {
	t, ok := mustTenant(w, r)
	if !ok {
		return
	}
	var members []db.ListAccountMembersRow
	err := s.inAccount(r.Context(), t.AccountID, func(tx pgx.Tx) error {
		var err error
		members, err = db.New(tx).ListAccountMembers(r.Context())
		return err
	})
	if err != nil {
		slog.Error("list account members", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Could not list members"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": accountMemberResponses(members)})
}

func (s *Server) handleAddAccountMember(w http.ResponseWriter, r *http.Request) {
	t, ok := mustTenant(w, r)
	if !ok {
		return
	}
	var req struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Invalid JSON"})
		return
	}

	ctx := r.Context()
	user, err := db.New(s.pool).GetUserByEmail(ctx, strings.ToLower(strings.TrimSpace(req.Email)))
	if err != nil {
		// Same answer, and the same reasoning, as inviting a room guest:
		// invitations by email arrive with the cabinet, and until then a
		// truthful 404 is what lets the caller tell the person to register.
		writeJSON(w, http.StatusNotFound, ErrorResponse{
			Error: "That person has no Guardian account yet",
		})
		return
	}

	// Only ever 'admin'. Ownership is set at registration and is not
	// transferable in v1 (specs/2026-09-05-saas-design.md), so there is no
	// role parameter to get wrong — and no way to mint a second owner.
	err = s.inAccount(ctx, t.AccountID, func(tx pgx.Tx) error {
		if err := db.New(tx).AddAccountMember(ctx, db.AddAccountMemberParams{
			AccountID: t.AccountID, UserID: user.ID, Role: roleAdmin,
		}); err != nil {
			return err
		}
		return s.recordEvent(ctx, tx, eventInput{
			AccountID: t.AccountID, Type: "account_member.added",
			Payload: map[string]any{"email": user.Email, "role": roleAdmin},
		})
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			writeJSON(w, http.StatusConflict, ErrorResponse{Error: "That person is already a member of this account"})
			return
		}
		slog.Error("add account member", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Could not add the member"})
		return
	}
	slog.Info("account member added", "account_id", t.AccountID, "user_id", user.ID)
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) handleDeleteAccountMember(w http.ResponseWriter, r *http.Request) {
	t, ok := mustTenant(w, r)
	if !ok {
		return
	}
	userID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "Member not found"})
		return
	}

	ctx := r.Context()
	err = s.inAccount(ctx, t.AccountID, func(tx pgx.Tx) error {
		q := db.New(tx)
		// DeleteAccountMember refuses the owner row in SQL, so this cannot
		// leave an account with nobody who can be billed or delete it.
		affected, err := q.DeleteAccountMember(ctx, userID)
		if err != nil {
			return err
		}
		if affected == 0 {
			return pgx.ErrNoRows
		}
		return s.recordEvent(ctx, tx, eventInput{
			AccountID: t.AccountID, Type: "account_member.removed",
			Payload: map[string]any{"user_id": userID},
		})
	})
	if err != nil {
		// An owner id lands here too: refusing to say "that one is the owner"
		// costs nothing, since the caller can see the roles in the list.
		writeLookupError(w, r, err, "Member")
		return
	}
	slog.Info("account member removed", "account_id", t.AccountID, "user_id", userID)
	w.WriteHeader(http.StatusNoContent)
}
