package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"server/internal/db"
)

const (
	defaultEventLimit = 50
	maxEventLimit     = 200
)

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

// encodeCursor names a position in the feed by the two columns it is ordered
// on. Opaque to the client on purpose: it is the server's index, not an offset
// the client should do arithmetic with.
func encodeCursor(e db.Event) string {
	return strconv.FormatInt(e.CreatedAt.Time.UnixNano(), 10) + "_" + e.ID.String()
}

func decodeCursor(s string) (pgtype.Timestamptz, uuid.UUID, error) {
	nanos, idPart, ok := strings.Cut(s, "_")
	if !ok {
		return pgtype.Timestamptz{}, uuid.Nil, errors.New("malformed cursor")
	}
	n, err := strconv.ParseInt(nanos, 10, 64)
	if err != nil {
		return pgtype.Timestamptz{}, uuid.Nil, err
	}
	id, err := uuid.Parse(idPart)
	if err != nil {
		return pgtype.Timestamptz{}, uuid.Nil, err
	}
	return pgtype.Timestamptz{Time: time.Unix(0, n), Valid: true}, id, nil
}

func (s *Server) handleListEvents(w http.ResponseWriter, r *http.Request) {
	t, ok := mustTenant(w, r)
	if !ok {
		return
	}

	limit := defaultEventLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "limit must be a positive integer"})
			return
		}
		limit = min(n, maxEventLimit)
	}

	var (
		beforeTime pgtype.Timestamptz
		beforeID   uuid.UUID
		paging     bool
	)
	if v := r.URL.Query().Get("cursor"); v != "" {
		var err error
		beforeTime, beforeID, err = decodeCursor(v)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Invalid cursor"})
			return
		}
		paging = true
	}

	var events []db.Event
	err := s.inAccount(r.Context(), t.AccountID, func(tx pgx.Tx) error {
		q := db.New(tx)
		var err error
		if paging {
			events, err = q.ListEventsBefore(r.Context(), db.ListEventsBeforeParams{
				BeforeTime: beforeTime, BeforeID: beforeID, RowLimit: int32(limit),
			})
		} else {
			events, err = q.ListRecentEvents(r.Context(), int32(limit))
		}
		return err
	})
	if err != nil {
		slog.Error("list events", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Could not list events"})
		return
	}

	out := map[string]any{"events": eventResponses(events)}
	// A cursor only when the page was full. Offering one on a short page would
	// send every client one request further to learn what this one already
	// said, forever.
	if len(events) == limit {
		out["next_cursor"] = encodeCursor(events[len(events)-1])
	}
	writeJSON(w, http.StatusOK, out)
}
