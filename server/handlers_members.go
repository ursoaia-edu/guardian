package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"server/internal/db"
)

func (s *Server) handleAddRoomMember(w http.ResponseWriter, r *http.Request) {
	t, ok := mustTenant(w, r)
	if !ok {
		return
	}
	roomID, ok := roomIDParam(r)
	if !ok {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "Room not found"})
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
		// Inviting somebody who has no account yet needs an email, which
		// arrives with the cabinet. Until then this is honestly a 404.
		writeJSON(w, http.StatusNotFound, ErrorResponse{
			Error: "That person has no Guardian account yet",
		})
		return
	}

	err = s.inAccount(ctx, t.AccountID, func(tx pgx.Tx) error {
		q := db.New(tx)
		if _, err := q.GetRoom(ctx, roomID); err != nil {
			return err
		}
		if err := q.AddRoomMember(ctx, db.AddRoomMemberParams{
			RoomID: roomID, AccountID: t.AccountID, UserID: user.ID,
		}); err != nil {
			return err
		}
		return s.recordEvent(ctx, tx, eventInput{
			AccountID: t.AccountID, RoomID: &roomID, Type: "room_member.granted",
			Payload: map[string]any{"email": user.Email},
		})
	})
	if err != nil {
		writeLookupError(w, r, err, "Room")
		return
	}
	slog.Info("room member added", "room_id", roomID, "user_id", user.ID)
	w.WriteHeader(http.StatusCreated)
}

// handleDeleteRoomMember takes a guest's access back. Sharing a room with
// somebody and then having no way to un-share it is the same defect as a
// credential with no kill switch: the grant outlives the reason for it, and the
// only remedy would be deleting the room the family actually uses.
func (s *Server) handleDeleteRoomMember(w http.ResponseWriter, r *http.Request) {
	t, ok := mustTenant(w, r)
	if !ok {
		return
	}
	roomID, ok := roomIDParam(r)
	if !ok {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "Room not found"})
		return
	}
	userID, parseErr := uuid.Parse(chi.URLParam(r, "userID"))
	if parseErr != nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "Member not found"})
		return
	}

	var affected int64
	err := s.inAccount(r.Context(), t.AccountID, func(tx pgx.Tx) error {
		q := db.New(tx)
		// Prove the room belongs to this account first, so a foreign room is a
		// 404 rather than a silent zero-row delete.
		if _, err := q.GetRoom(r.Context(), roomID); err != nil {
			return err
		}
		var err error
		affected, err = q.DeleteRoomMember(r.Context(), db.DeleteRoomMemberParams{
			RoomID: roomID, UserID: userID,
		})
		if err != nil || affected == 0 {
			return err
		}
		return s.recordEvent(r.Context(), tx, eventInput{
			AccountID: t.AccountID, RoomID: &roomID, Type: "room_member.revoked",
			Payload: map[string]any{"user_id": userID},
		})
	})
	if err != nil {
		writeLookupError(w, r, err, "Room")
		return
	}
	if affected == 0 {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "Member not found"})
		return
	}
	slog.Info("room member removed", "room_id", roomID, "user_id", userID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListRoomMembers(w http.ResponseWriter, r *http.Request) {
	roomID, ok := roomIDParam(r)
	if !ok {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "Room not found"})
		return
	}
	t, ok := mustTenant(w, r)
	if !ok {
		return
	}
	var members []db.ListRoomMembersRow
	err := s.inAccount(r.Context(), t.AccountID, func(tx pgx.Tx) error {
		q := db.New(tx)
		if err := s.assertRoomVisible(r.Context(), q, t, roomID); err != nil {
			return err
		}
		var err error
		members, err = q.ListRoomMembers(r.Context(), roomID)
		return err
	})
	if err != nil {
		writeLookupError(w, r, err, "Room")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": memberResponses(members)})
}

// assertRoomVisible is the single place that answers "may this caller touch
// this room?". Admins may touch any room of their account; a guest only the
// rooms granted to them. Both failures look identical from outside.
func (s *Server) assertRoomVisible(ctx context.Context, q *db.Queries, t Tenant, roomID uuid.UUID) error {
	if _, err := q.GetRoom(ctx, roomID); err != nil {
		return err
	}
	if t.Role != roleMember {
		return nil
	}
	ok, err := q.IsRoomMember(ctx, db.IsRoomMemberParams{RoomID: roomID, UserID: t.UserID})
	if err != nil {
		return err
	}
	if !ok {
		return pgx.ErrNoRows
	}
	return nil
}
