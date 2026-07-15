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
		if t.Role == roleMember {
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
	writeJSON(w, http.StatusOK, map[string]any{"rooms": roomResponses(rooms)})
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
		if err != nil {
			return err
		}
		return s.recordEvent(r.Context(), tx, eventInput{
			AccountID: t.AccountID, RoomID: &room.ID, Type: "room.created",
			Payload: map[string]any{"name": room.Name},
		})
	})
	if err != nil {
		slog.Error("create room", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Could not create the room"})
		return
	}
	writeJSON(w, http.StatusCreated, newRoomResponse(room))
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
	writeJSON(w, http.StatusOK, newRoomResponse(room))
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
		if err != nil {
			return err
		}
		// Only what the request actually set. A feed entry saying a room was
		// "updated" with no detail answers none of the questions the feed
		// exists for, and one listing every column answers them dishonestly.
		changed := map[string]any{"name": room.Name}
		if req.Mode != nil {
			changed["mode"] = *req.Mode
		}
		if req.ProtectionEnabled != nil {
			changed["protection_enabled"] = *req.ProtectionEnabled
		}
		if req.PowerAllowed != nil {
			changed["power_allowed"] = *req.PowerAllowed
		}
		return s.recordEvent(r.Context(), tx, eventInput{
			AccountID: t.AccountID, RoomID: &room.ID, Type: "room.updated", Payload: changed,
		})
	})
	if err != nil {
		writeLookupError(w, r, err, "Room")
		return
	}
	writeJSON(w, http.StatusOK, newRoomResponse(room))
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
	err := s.inAccount(r.Context(), t.AccountID, func(tx pgx.Tx) error {
		q := db.New(tx)
		// Read it first so the event can say WHICH room was deleted; the row is
		// gone by the time anyone reads the feed.
		room, err := q.GetRoom(r.Context(), id)
		if err != nil {
			return err
		}
		if err := s.recordEvent(r.Context(), tx, eventInput{
			AccountID: t.AccountID, Type: "room.deleted",
			Payload: map[string]any{"name": room.Name, "room_id": room.ID},
		}); err != nil {
			return err
		}
		affected, err := q.DeleteRoom(r.Context(), id)
		if err != nil {
			return err
		}
		if affected == 0 {
			return pgx.ErrNoRows
		}
		return nil
	})
	if err != nil {
		writeLookupError(w, r, err, "Room")
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
	writeJSON(w, http.StatusOK, map[string]any{"applications": applicationResponses(apps)})
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
		if err != nil {
			return err
		}
		return s.recordEvent(r.Context(), tx, eventInput{
			AccountID: t.AccountID, RoomID: &roomID, Type: "application.added",
			Payload: map[string]any{"name": app.Name, "list": app.List},
		})
	})
	if err != nil {
		writeLookupError(w, r, err, "Room")
		return
	}
	writeJSON(w, http.StatusCreated, newApplicationResponse(app))
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

	err := s.inAccount(r.Context(), t.AccountID, func(tx pgx.Tx) error {
		q := db.New(tx)
		// Same guard as the insert: prove the room belongs to this account AND
		// that the caller may touch it, so a foreign or ungranted room is an
		// honest 404 rather than a silent zero-row delete.
		if err := s.assertRoomVisible(r.Context(), q, t, roomID); err != nil {
			return err
		}
		affected, err := q.DeleteRoomApplication(r.Context(), db.DeleteRoomApplicationParams{
			ID: appID, RoomID: roomID,
		})
		if err != nil {
			return err
		}
		if affected == 0 {
			return errApplicationNotFound
		}
		return s.recordEvent(r.Context(), tx, eventInput{
			AccountID: t.AccountID, RoomID: &roomID, Type: "application.removed",
			Payload: map[string]any{"application_id": appID},
		})
	})
	if errors.Is(err, errApplicationNotFound) {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "Application not found"})
		return
	}
	if err != nil {
		writeLookupError(w, r, err, "Room")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// errApplicationNotFound distinguishes "the room is fine, that rule is not in
// it" from the room-level 404 writeLookupError produces, inside a transaction
// where the only way out is an error.
var errApplicationNotFound = errors.New("application not found")

// handlePatchRoomApplication switches a rule off without deleting it. The
// enabled column has been in the schema since the first migration with no way
// to clear it, so a rule could only be added or destroyed — and "let them play
// this weekend" meant retyping it on Monday.
func (s *Server) handlePatchRoomApplication(w http.ResponseWriter, r *http.Request) {
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
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Invalid JSON"})
		return
	}
	if req.Enabled == nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Nothing to update"})
		return
	}

	t, ok := mustTenant(w, r)
	if !ok {
		return
	}
	var app db.Application
	err := s.inAccount(r.Context(), t.AccountID, func(tx pgx.Tx) error {
		q := db.New(tx)
		if err := s.assertRoomVisible(r.Context(), q, t, roomID); err != nil {
			return err
		}
		var err error
		app, err = q.UpdateRoomApplication(r.Context(), db.UpdateRoomApplicationParams{
			ID: appID, RoomID: roomID, Enabled: req.Enabled,
		})
		if err != nil {
			return err
		}
		return s.recordEvent(r.Context(), tx, eventInput{
			AccountID: t.AccountID, RoomID: &roomID, Type: "application.updated",
			Payload: map[string]any{"name": app.Name, "enabled": app.Enabled},
		})
	})
	if err != nil {
		// UpdateRoomApplication returns no rows for an id that is not in this
		// room, which is the same answer as a room the caller cannot see.
		writeLookupError(w, r, err, "Application")
		return
	}
	writeJSON(w, http.StatusOK, newApplicationResponse(app))
}
