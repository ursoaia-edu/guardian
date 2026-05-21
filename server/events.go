package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"server/internal/db"
)

const defaultEventLimit = 200

type eventInput struct {
	AccountID  uuid.UUID
	RoomID     *uuid.UUID
	ComputerID *uuid.UUID
	Type       string
	Payload    map[string]any
}

// recordEvent writes inside the caller's transaction so the event and the change
// it describes commit or roll back together.
func (s *Server) recordEvent(ctx context.Context, tx pgx.Tx, e eventInput) error {
	payload := []byte("{}")
	if e.Payload != nil {
		encoded, err := json.Marshal(e.Payload)
		if err != nil {
			return err
		}
		payload = encoded
	}
	return db.New(tx).RecordEvent(ctx, db.RecordEventParams{
		AccountID: e.AccountID, RoomID: e.RoomID, ComputerID: e.ComputerID,
		Type: e.Type, Payload: payload,
	})
}

func (s *Server) handleListEvents(w http.ResponseWriter, r *http.Request) {
	t, ok := mustTenant(w, r)
	if !ok {
		return
	}
	var events []db.Event
	err := s.inAccount(r.Context(), t.AccountID, func(tx pgx.Tx) error {
		var err error
		events, err = db.New(tx).ListRecentEvents(r.Context(), defaultEventLimit)
		return err
	})
	if err != nil {
		slog.Error("list events", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Could not list events"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": orEmpty(events)})
}
