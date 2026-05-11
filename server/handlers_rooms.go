package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"server/internal/db"
)

func (s *Server) handleListRooms(w http.ResponseWriter, r *http.Request) {
	t, ok := mustTenant(w, r)
	if !ok {
		return
	}
	var rooms []db.Room
	err := s.inAccount(r.Context(), t.AccountID, func(tx pgx.Tx) error {
		var err error
		rooms, err = db.New(tx).ListRooms(r.Context())
		return err
	})
	if err != nil {
		slog.Error("list rooms", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Could not list rooms"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rooms": rooms})
}

func (s *Server) handleCreateRoom(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Invalid JSON"})
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Room name cannot be empty"})
		return
	}

	t, ok := mustTenant(w, r)
	if !ok {
		return
	}
	var room db.Room
	err := s.inAccount(r.Context(), t.AccountID, func(tx pgx.Tx) error {
		var err error
		room, err = db.New(tx).CreateRoom(r.Context(), db.CreateRoomParams{
			AccountID: t.AccountID, Name: strings.TrimSpace(req.Name),
		})
		return err
	})
	if err != nil {
		slog.Error("create room", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Could not create the room"})
		return
	}
	writeJSON(w, http.StatusCreated, room)
}

// roomIDParam parses the URL parameter. A malformed id is reported as 404, not
// 400: whether the id is well-formed tells the caller nothing they may know.
func roomIDParam(r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "roomID"))
	return id, err == nil
}

func (s *Server) handleGetRoom(w http.ResponseWriter, r *http.Request) {
	id, ok := roomIDParam(r)
	if !ok {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "Room not found"})
		return
	}
	t, ok := mustTenant(w, r)
	if !ok {
		return
	}
	var room db.Room
	err := s.inAccount(r.Context(), t.AccountID, func(tx pgx.Tx) error {
		var err error
		room, err = db.New(tx).GetRoom(r.Context(), id)
		return err
	})
	if err != nil {
		// RLS turns "another account's room" into no rows, so the caller cannot
		// tell a foreign room from a nonexistent one.
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "Room not found"})
		return
	}
	writeJSON(w, http.StatusOK, room)
}

func (s *Server) handleDeleteRoom(w http.ResponseWriter, r *http.Request) {
	id, ok := roomIDParam(r)
	if !ok {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "Room not found"})
		return
	}
	t, ok := mustTenant(w, r)
	if !ok {
		return
	}
	var affected int64
	err := s.inAccount(r.Context(), t.AccountID, func(tx pgx.Tx) error {
		var err error
		affected, err = db.New(tx).DeleteRoom(r.Context(), id)
		return err
	})
	if err != nil {
		slog.Error("delete room", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Could not delete the room"})
		return
	}
	if affected == 0 {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "Room not found"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
