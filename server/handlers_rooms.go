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
		q := db.New(tx)
		var err error
		if t.Role == "member" {
			rooms, err = q.ListRoomsForMember(r.Context(), t.UserID)
		} else {
			rooms, err = q.ListRooms(r.Context())
		}
		return err
	})
	if err != nil {
		slog.Error("list rooms", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Could not list rooms"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rooms": orEmpty(rooms)})
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
		q := db.New(tx)
		if err := s.assertRoomVisible(r.Context(), q, t, id); err != nil {
			return err
		}
		var err error
		room, err = q.GetRoom(r.Context(), id)
		return err
	})
	if err != nil {
		// RLS turns "another account's room" into no rows, so the caller cannot
		// tell a foreign room from a nonexistent one — but a dropped connection
		// is not a missing room, and writeLookupError keeps the two apart.
		writeLookupError(w, r, err, "Room")
		return
	}
	writeJSON(w, http.StatusOK, room)
}

func (s *Server) handlePatchRoom(w http.ResponseWriter, r *http.Request) {
	id, ok := roomIDParam(r)
	if !ok {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "Room not found"})
		return
	}
	var req struct {
		Name              *string `json:"name"`
		Mode              *string `json:"mode"`
		ProtectionEnabled *bool   `json:"protection_enabled"`
		PowerAllowed      *bool   `json:"power_allowed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Invalid JSON"})
		return
	}
	if req.Mode != nil && *req.Mode != "blacklist" && *req.Mode != "whitelist" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Mode must be 'blacklist' or 'whitelist'"})
		return
	}
	// The same name rule the create path applies. Without it a rename can empty
	// a room's name or set it to whitespace, and the cabinet then shows a room
	// with no label that a parent cannot tell apart from any other.
	if req.Name != nil {
		trimmed := strings.TrimSpace(*req.Name)
		if trimmed == "" {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Room name cannot be empty"})
			return
		}
		req.Name = &trimmed
	}
	t, ok := mustTenant(w, r)
	if !ok {
		return
	}
	var room db.Room
	err := s.inAccount(r.Context(), t.AccountID, func(tx pgx.Tx) error {
		q := db.New(tx)
		if err := s.assertRoomVisible(r.Context(), q, t, id); err != nil {
			return err
		}
		var err error
		room, err = q.UpdateRoom(r.Context(), db.UpdateRoomParams{
			ID: id, Name: req.Name, Mode: req.Mode,
			ProtectionEnabled: req.ProtectionEnabled, PowerAllowed: req.PowerAllowed,
		})
		return err
	})
	if err != nil {
		writeLookupError(w, r, err, "Room")
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
	if !requireManager(w, t) {
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

func (s *Server) handleListRoomApplications(w http.ResponseWriter, r *http.Request) {
	roomID, ok := roomIDParam(r)
	if !ok {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "Room not found"})
		return
	}
	t, ok := mustTenant(w, r)
	if !ok {
		return
	}
	var apps []db.Application
	err := s.inAccount(r.Context(), t.AccountID, func(tx pgx.Tx) error {
		q := db.New(tx)
		if err := s.assertRoomVisible(r.Context(), q, t, roomID); err != nil {
			return err
		}
		var err error
		apps, err = q.ListRoomApplications(r.Context(), roomID)
		return err
	})
	if err != nil {
		writeLookupError(w, r, err, "Room")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"applications": orEmpty(apps)})
}

func (s *Server) handleAddRoomApplication(w http.ResponseWriter, r *http.Request) {
	roomID, ok := roomIDParam(r)
	if !ok {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "Room not found"})
		return
	}
	var req struct {
		Name string `json:"name"`
		List string `json:"list"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Invalid JSON"})
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Application name cannot be empty"})
		return
	}
	if req.List == "" {
		req.List = "blacklist"
	}
	if req.List != "blacklist" && req.List != "whitelist" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "List must be 'blacklist' or 'whitelist'"})
		return
	}

	t, ok := mustTenant(w, r)
	if !ok {
		return
	}
	var app db.Application
	err := s.inAccount(r.Context(), t.AccountID, func(tx pgx.Tx) error {
		q := db.New(tx)
		// Proves the room belongs to this account before writing into it: RLS
		// would reject the insert anyway, but this gives an honest 404.
		if err := s.assertRoomVisible(r.Context(), q, t, roomID); err != nil {
			return err
		}
		var err error
		app, err = q.AddRoomApplication(r.Context(), db.AddRoomApplicationParams{
			AccountID: t.AccountID, RoomID: roomID, Name: req.Name, List: req.List,
		})
		return err
	})
	if err != nil {
		writeLookupError(w, r, err, "Room")
		return
	}
	writeJSON(w, http.StatusCreated, app)
}

func (s *Server) handleDeleteRoomApplication(w http.ResponseWriter, r *http.Request) {
	roomID, ok := roomIDParam(r)
	if !ok {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "Room not found"})
		return
	}
	appID, parseErr := uuid.Parse(chi.URLParam(r, "appID"))
	if parseErr != nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "Application not found"})
		return
	}
	t, ok := mustTenant(w, r)
	if !ok {
		return
	}

	var affected int64
	err := s.inAccount(r.Context(), t.AccountID, func(tx pgx.Tx) error {
		q := db.New(tx)
		// Same guard as the insert: prove the room belongs to this account so a
		// foreign room is an honest 404 rather than a silent zero-row delete.
		if _, err := q.GetRoom(r.Context(), roomID); err != nil {
			return err
		}
		var err error
		affected, err = q.DeleteRoomApplication(r.Context(), db.DeleteRoomApplicationParams{
			ID: appID, RoomID: roomID,
		})
		return err
	})
	if err != nil {
		writeLookupError(w, r, err, "Room")
		return
	}
	if affected == 0 {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "Application not found"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
