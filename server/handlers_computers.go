package main

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"server/internal/db"
)

func (s *Server) handleListComputers(w http.ResponseWriter, r *http.Request) {
	t, ok := mustTenant(w, r)
	if !ok {
		return
	}
	var computers []db.Computer
	err := s.inAccount(r.Context(), t.AccountID, func(tx pgx.Tx) error {
		q := db.New(tx)
		var err error
		if t.Role == "member" {
			computers, err = q.ListComputersForMember(r.Context(), t.UserID)
		} else {
			computers, err = q.ListComputers(r.Context())
		}
		return err
	})
	if err != nil {
		slog.Error("list computers", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Could not list computers"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"computers": orEmpty(computers)})
}

func (s *Server) handlePatchComputer(w http.ResponseWriter, r *http.Request) {
	// A malformed id is a 404, not a 500, mirroring roomIDParam: whether the id
	// is well-formed tells the caller nothing they are entitled to know, and
	// "undefined" is exactly the id a JS cabinet sends the moment a state
	// variable is unset — a real client bug, not a contrived one.
	id, err := uuid.Parse(chi.URLParam(r, "computerID"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "Computer not found"})
		return
	}
	// No json tags: this struct is never unmarshalled into. The body is decoded
	// field by field out of raw below, because only that distinguishes an absent
	// room_id from an explicitly null one.
	var req struct {
		DisplayName *string
		RoomID      *uuid.UUID
		SetRoom     bool
		Blocked     *bool
	}
	raw := map[string]json.RawMessage{}
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Invalid JSON"})
		return
	}
	// room_id present and null means "take it out of every room", which is a
	// different instruction from "leave the room alone".
	if v, ok := raw["room_id"]; ok {
		req.SetRoom = true
		if string(v) != "null" {
			var roomID uuid.UUID
			if err := json.Unmarshal(v, &roomID); err != nil {
				writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Invalid room_id"})
				return
			}
			req.RoomID = &roomID
		}
	}
	if v, ok := raw["display_name"]; ok {
		var name string
		if err := json.Unmarshal(v, &name); err != nil {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Invalid display_name"})
			return
		}
		req.DisplayName = &name
	}
	if v, ok := raw["blocked"]; ok {
		var blocked bool
		if err := json.Unmarshal(v, &blocked); err != nil {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Invalid blocked"})
			return
		}
		req.Blocked = &blocked
	}

	t, ok := mustTenant(w, r)
	if !ok {
		return
	}
	var computer db.Computer
	err = s.inAccount(r.Context(), t.AccountID, func(tx pgx.Tx) error {
		q := db.New(tx)
		if t.Role == "member" {
			if _, err := q.GetComputerForMember(r.Context(), db.GetComputerForMemberParams{
				ID: id, UserID: t.UserID,
			}); err != nil {
				return err
			}
			// Taking a machine out of every room removes it from management
			// altogether: it stops enforcing anything and nobody is told. That
			// is an account-wide act, not something a guest does to a machine
			// that happens to sit in their room.
			if req.SetRoom && req.RoomID == nil {
				return pgx.ErrNoRows
			}
		}
		if req.RoomID != nil {
			// assertRoomVisible, not GetRoom. GetRoom only proves the room is in
			// the account, so a guest could move a machine into a room they were
			// never granted — a write beyond their rooms, and the one place
			// where a foreign room (404) and an ungranted one (200) would
			// otherwise be distinguishable.
			if err := s.assertRoomVisible(r.Context(), q, t, *req.RoomID); err != nil {
				return err
			}
		}
		var err error
		computer, err = q.UpdateComputer(r.Context(), db.UpdateComputerParams{
			ID: id, DisplayName: req.DisplayName, RoomID: req.RoomID,
			SetRoom: req.SetRoom, Blocked: req.Blocked,
		})
		return err
	})
	if err != nil {
		writeLookupError(w, r, err, "Computer")
		return
	}
	writeJSON(w, http.StatusOK, computer)
}
