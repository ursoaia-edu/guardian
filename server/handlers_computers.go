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
		var err error
		computers, err = db.New(tx).ListComputers(r.Context())
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
	id, err := uuid.Parse(chi.URLParam(r, "computerID"))
	if err != nil {
		writeLookupError(w, r, err, "Computer")
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
		if req.RoomID != nil {
			// Assigning to a room of another account must not be possible; RLS
			// makes the lookup return no rows, which becomes a 404 below.
			if _, err := q.GetRoom(r.Context(), *req.RoomID); err != nil {
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
