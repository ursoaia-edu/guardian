package main

import (
	"context"
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
		if t.Role == roleMember {
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
	writeJSON(w, http.StatusOK, map[string]any{"computers": computerResponses(computers)})
}

// computerIDParam parses the URL parameter. A malformed id is a 404, not a
// 500, mirroring roomIDParam: whether the id is well-formed tells the caller
// nothing they are entitled to know, and "undefined" is exactly the id a JS
// cabinet sends the moment a state variable is unset.
func computerIDParam(r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "computerID"))
	return id, err == nil
}

// visibleComputer reads one machine subject to the caller's role: a guest sees
// only machines sitting in a room they were granted, and an invisible machine
// is a 404 like a foreign one.
func visibleComputer(ctx context.Context, q *db.Queries, t Tenant, id uuid.UUID) (db.Computer, error) {
	if t.Role == roleMember {
		return q.GetComputerForMember(ctx, db.GetComputerForMemberParams{ID: id, UserID: t.UserID})
	}
	return q.GetComputer(ctx, id)
}

func (s *Server) handleGetComputer(w http.ResponseWriter, r *http.Request) {
	id, ok := computerIDParam(r)
	if !ok {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "Computer not found"})
		return
	}
	t, ok := mustTenant(w, r)
	if !ok {
		return
	}
	var computer db.Computer
	err := s.inAccount(r.Context(), t.AccountID, func(tx pgx.Tx) error {
		var err error
		computer, err = visibleComputer(r.Context(), db.New(tx), t, id)
		return err
	})
	if err != nil {
		writeLookupError(w, r, err, "Computer")
		return
	}
	writeJSON(w, http.StatusOK, newComputerResponse(computer))
}

// handleDeleteComputer unenrolls a machine. The row IS the credential — the
// agent authenticates by a digest stored on it — so deleting it revokes that
// one machine and nothing else, which is the per-machine revocation the
// account-wide binding-token kill switch could not give.
//
// The machine does not go free: its next sync is a 401, and the agent treats a
// 401 as "keep enforcing the last known policy". Somebody has to reinstall it
// to bring it back, which is the point.
func (s *Server) handleDeleteComputer(w http.ResponseWriter, r *http.Request) {
	id, ok := computerIDParam(r)
	if !ok {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "Computer not found"})
		return
	}
	t, ok := mustTenant(w, r)
	if !ok {
		return
	}
	err := s.inAccount(r.Context(), t.AccountID, func(tx pgx.Tx) error {
		q := db.New(tx)
		computer, err := q.GetComputer(r.Context(), id)
		if err != nil {
			return err
		}
		// Recorded before the delete, and carrying the machine's names,
		// because events.computer_id is ON DELETE SET NULL: a moment later
		// there is nothing left to point at.
		if err := s.recordEvent(r.Context(), tx, eventInput{
			AccountID: t.AccountID, Type: "computer.removed",
			Payload: map[string]any{
				"hostname": computer.Hostname, "display_name": computer.DisplayName,
				"machine_guid": computer.MachineGuid,
			},
		}); err != nil {
			return err
		}
		affected, err := q.DeleteComputer(r.Context(), id)
		if err != nil {
			return err
		}
		if affected == 0 {
			return pgx.ErrNoRows
		}
		return nil
	})
	if err != nil {
		writeLookupError(w, r, err, "Computer")
		return
	}
	slog.Info("computer unenrolled", "account_id", t.AccountID, "computer_id", id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePatchComputer(w http.ResponseWriter, r *http.Request) {
	id, ok := computerIDParam(r)
	if !ok {
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
	err := s.inAccount(r.Context(), t.AccountID, func(tx pgx.Tx) error {
		q := db.New(tx)
		// Read before write: it is the caller's 404 (a machine a guest may not
		// see), and it is what the events below compare against to say what
		// actually changed rather than what was merely sent.
		before, err := visibleComputer(r.Context(), q, t, id)
		if err != nil {
			return err
		}
		if t.Role == roleMember {
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
		computer, err = q.UpdateComputer(r.Context(), db.UpdateComputerParams{
			ID: id, DisplayName: req.DisplayName, RoomID: req.RoomID,
			SetRoom: req.SetRoom, Blocked: req.Blocked,
		})
		if err != nil {
			return err
		}
		return s.recordComputerChanges(r.Context(), tx, t, before, computer)
	})
	if err != nil {
		writeLookupError(w, r, err, "Computer")
		return
	}
	writeJSON(w, http.StatusOK, newComputerResponse(computer))
}

// recordComputerChanges writes one event per thing that actually changed.
// Comparing before and after rather than trusting the request body matters:
// a PATCH that sets blocked to the value it already had is not a blocking,
// and a feed that says otherwise teaches people to ignore it.
func (s *Server) recordComputerChanges(ctx context.Context, tx pgx.Tx, t Tenant, before, after db.Computer) error {
	name := after.DisplayName
	if name == "" {
		name = after.Hostname
	}

	if before.Blocked != after.Blocked {
		kind := "computer.unblocked"
		if after.Blocked {
			kind = "computer.blocked"
		}
		if err := s.recordEvent(ctx, tx, eventInput{
			AccountID: t.AccountID, RoomID: after.RoomID, ComputerID: &after.ID,
			Type: kind, Payload: map[string]any{"name": name},
		}); err != nil {
			return err
		}
	}

	if !sameRoom(before.RoomID, after.RoomID) {
		payload := map[string]any{"name": name}
		if before.RoomID != nil {
			payload["from_room_id"] = *before.RoomID
		}
		if err := s.recordEvent(ctx, tx, eventInput{
			AccountID: t.AccountID, RoomID: after.RoomID, ComputerID: &after.ID,
			Type: "computer.assigned", Payload: payload,
		}); err != nil {
			return err
		}
	}

	if before.DisplayName != after.DisplayName {
		if err := s.recordEvent(ctx, tx, eventInput{
			AccountID: t.AccountID, RoomID: after.RoomID, ComputerID: &after.ID,
			Type: "computer.renamed", Payload: map[string]any{"name": name},
		}); err != nil {
			return err
		}
	}
	return nil
}

func sameRoom(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
