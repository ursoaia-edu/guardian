package main

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"server/internal/db"
)

const (
	defaultProcessEventLimit = 50
	maxProcessEventLimit     = 200
)

// processEventFilters is what the query string may narrow the log by. Every
// field is optional and they compose; an absent one is inert in SQL.
type processEventFilters struct {
	Process  *string
	Reason   *string
	Computer *uuid.UUID
	Since    pgtype.Timestamptz
	Until    pgtype.Timestamptz
	Limit    int32
	Before   pgtype.Timestamptz
	BeforeID *uuid.UUID
}

// readProcessEventFilters parses the query string. A malformed value is
// ignored rather than rejected: this is a log viewer, and a mistyped filter
// that returns the unfiltered feed is friendlier than a 400 with no rows.
// The one exception is the cursor, which is the server's own opaque token —
// a broken one means the client is out of step and should be told so.
func readProcessEventFilters(r *http.Request) (processEventFilters, error) {
	f := processEventFilters{Limit: defaultProcessEventLimit}
	q := r.URL.Query()

	if v := q.Get("process"); v != "" {
		f.Process = &v
	}
	if v := q.Get("reason"); v == "blacklist" || v == "whitelist" || v == "locked" || v == "overflow" {
		f.Reason = &v
	}
	if v := q.Get("computer_id"); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			f.Computer = &id
		}
	}
	if v := q.Get("since"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			f.Since = pgtype.Timestamptz{Time: t, Valid: true}
		}
	}
	if v := q.Get("until"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			f.Until = pgtype.Timestamptz{Time: t, Valid: true}
		}
	}
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			if n > maxProcessEventLimit {
				n = maxProcessEventLimit
			}
			f.Limit = int32(n)
		}
	}
	if v := q.Get("cursor"); v != "" {
		at, id, err := decodeCursor(v)
		if err != nil {
			return f, err
		}
		f.Before = at
		f.BeforeID = &id
	}
	return f, nil
}

func (s *Server) handleListRoomProcessEvents(w http.ResponseWriter, r *http.Request) {
	t, _ := tenantFrom(r.Context())
	roomID, err := uuid.Parse(chi.URLParam(r, "roomID"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "Not found"})
		return
	}
	filters, err := readProcessEventFilters(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Invalid cursor"})
		return
	}

	var rows []db.ListRoomProcessEventsRow
	err = s.inAccount(r.Context(), t.AccountID, func(tx pgx.Tx) error {
		q := db.New(tx)
		// The room must be visible to this caller, not merely in the account:
		// a guest sees only the rooms granted to them.
		if err := s.assertRoomVisible(r.Context(), q, t, roomID); err != nil {
			return err
		}
		var err error
		rows, err = q.ListRoomProcessEvents(r.Context(), db.ListRoomProcessEventsParams{
			RoomID: roomID, Process: filters.Process, Reason: filters.Reason,
			Computer: filters.Computer, Since: filters.Since, Until: filters.Until,
			BeforeTime: filters.Before, BeforeID: filters.BeforeID,
			RowLimit: filters.Limit,
		})
		return err
	})
	if err != nil {
		s.writeProcessEventError(w, err, "list room process events")
		return
	}
	writeProcessEventPage(w, roomRowsToResponses(rows), int(filters.Limit))
}

func (s *Server) handleListComputerProcessEvents(w http.ResponseWriter, r *http.Request) {
	t, _ := tenantFrom(r.Context())
	computerID, err := uuid.Parse(chi.URLParam(r, "computerID"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "Not found"})
		return
	}
	filters, err := readProcessEventFilters(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Invalid cursor"})
		return
	}

	var rows []db.ListComputerProcessEventsRow
	err = s.inAccount(r.Context(), t.AccountID, func(tx pgx.Tx) error {
		q := db.New(tx)
		// Reading the computer first is what makes a guest's reach the same
		// here as everywhere else: an unassigned machine, or one in a room
		// they were not granted, is not theirs to read the history of.
		computer, err := q.GetComputer(r.Context(), computerID)
		if err != nil {
			return err
		}
		if t.Role == roleMember {
			if computer.RoomID == nil {
				return pgx.ErrNoRows
			}
			if err := s.assertRoomVisible(r.Context(), q, t, *computer.RoomID); err != nil {
				return err
			}
		}
		rows, err = q.ListComputerProcessEvents(r.Context(), db.ListComputerProcessEventsParams{
			ComputerID: computerID, Process: filters.Process, Reason: filters.Reason,
			Since: filters.Since, Until: filters.Until,
			BeforeTime: filters.Before, BeforeID: filters.BeforeID,
			RowLimit: filters.Limit,
		})
		return err
	})
	if err != nil {
		s.writeProcessEventError(w, err, "list computer process events")
		return
	}
	writeProcessEventPage(w, computerRowsToResponses(rows), int(filters.Limit))
}

// writeProcessEventError keeps "not yours" and "not there" indistinguishable,
// the way every other lookup in this API does, and keeps a database failure a
// 500 rather than dressing it up as a 404.
func (s *Server) writeProcessEventError(w http.ResponseWriter, err error, what string) {
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "Not found"})
		return
	}
	slog.Error(what, "error", err)
	writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Internal error"})
}

// writeProcessEventPage emits the page and a cursor, present only when the
// page was full — its absence means the end of the feed.
func writeProcessEventPage(w http.ResponseWriter, out []ProcessEventResponse, limit int) {
	body := map[string]any{"process_events": out}
	if len(out) == limit && len(out) > 0 {
		last := out[len(out)-1]
		body["next_cursor"] = strconv.FormatInt(last.CreatedAt.UnixNano(), 10) + "_" + last.ID
	}
	writeJSON(w, http.StatusOK, body)
}

func roomRowsToResponses(rows []db.ListRoomProcessEventsRow) []ProcessEventResponse {
	out := make([]ProcessEventResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, ProcessEventResponse{
			ID: r.ID.String(), ComputerID: r.ComputerID.String(),
			ComputerName: r.ComputerName, RoomID: uuidPtrString(r.RoomID),
			Process: r.Process, Reason: r.Reason, Count: int(r.Count),
			FirstAt: r.FirstAt.Time, LastAt: r.LastAt.Time, CreatedAt: r.CreatedAt.Time,
		})
	}
	return out
}

func computerRowsToResponses(rows []db.ListComputerProcessEventsRow) []ProcessEventResponse {
	out := make([]ProcessEventResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, ProcessEventResponse{
			ID: r.ID.String(), ComputerID: r.ComputerID.String(),
			ComputerName: r.ComputerName, RoomID: uuidPtrString(r.RoomID),
			Process: r.Process, Reason: r.Reason, Count: int(r.Count),
			FirstAt: r.FirstAt.Time, LastAt: r.LastAt.Time, CreatedAt: r.CreatedAt.Time,
		})
	}
	return out
}

func uuidPtrString(id *uuid.UUID) *string {
	if id == nil {
		return nil
	}
	s := id.String()
	return &s
}
