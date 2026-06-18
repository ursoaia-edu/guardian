package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"server/internal/db"
)

const bindingTokenTTL = 365 * 24 * time.Hour

func (s *Server) handleCreateBindingToken(w http.ResponseWriter, r *http.Request) {
	t, ok := mustTenant(w, r)
	if !ok {
		return
	}
	if !requireManager(w, t) {
		return
	}
	plain, hash := newToken()
	err := s.inAccount(r.Context(), t.AccountID, func(tx pgx.Tx) error {
		_, err := db.New(tx).CreateBindingToken(r.Context(), db.CreateBindingTokenParams{
			AccountID: t.AccountID, TokenHash: hash,
			ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(bindingTokenTTL), Valid: true},
		})
		return err
	})
	if err != nil {
		slog.Error("create binding token", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Could not create the token"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"token": plain})
}

// handleRevokeBindingTokens invalidates every binding token the account holds.
// A binding token lives for a year and one downloaded installer carries it to
// every machine, so without a kill switch a leaked installer is a year-long
// credential with no remedy. Machines already enrolled keep working: they hold
// their own per-machine tokens by now, and those are revoked one at a time by
// deleting the computer.
func (s *Server) handleRevokeBindingTokens(w http.ResponseWriter, r *http.Request) {
	t, ok := mustTenant(w, r)
	if !ok {
		return
	}
	if !requireManager(w, t) {
		return
	}
	var revoked int64
	err := s.inAccount(r.Context(), t.AccountID, func(tx pgx.Tx) error {
		var err error
		revoked, err = db.New(tx).RevokeAllBindingTokens(r.Context())
		return err
	})
	if err != nil {
		slog.Error("revoke binding tokens", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Could not revoke the tokens"})
		return
	}
	slog.Info("binding tokens revoked", "account_id", t.AccountID, "count", revoked)
	w.WriteHeader(http.StatusNoContent)
}

type enrollRequest struct {
	BindingToken string          `json:"binding_token"`
	MachineGUID  string          `json:"machine_guid"`
	Hostname     string          `json:"hostname"`
	OSName       string          `json:"os_name"`
	OSBuild      string          `json:"os_build"`
	Arch         string          `json:"arch"`
	AgentVersion string          `json:"agent_version"`
	Hardware     json.RawMessage `json:"hardware"`
}

func (s *Server) handleEnroll(w http.ResponseWriter, r *http.Request) {
	var req enrollRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Invalid JSON"})
		return
	}
	if req.MachineGUID == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "machine_guid is required"})
		return
	}

	ctx := r.Context()
	// The binding token names the account. account_id is never taken from the
	// request body, even though the agent could trivially send one.
	binding, err := db.New(s.pool).GetActiveBindingToken(ctx, hashToken(req.BindingToken))
	if err != nil {
		// An unreachable database is not a bad token. Collapsing the two would
		// tell every customer in the fleet to fetch a new installer during an
		// outage, and leave the operator nothing but a stream of Warns that look
		// like someone probing tokens. Same rule as writeLookupError, which this
		// handler cannot use because it has no account scope to speak of.
		if !errors.Is(err, pgx.ErrNoRows) {
			slog.Error("look up binding token", "error", err)
			writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Could not enroll this computer"})
			return
		}
		slog.Warn("enrollment with an invalid binding token", "remote", r.RemoteAddr)
		writeJSON(w, http.StatusUnauthorized, ErrorResponse{Error: "This installer's token is no longer valid"})
		return
	}

	agentPlain, agentHash := newToken()
	var overLimit bool
	err = s.inAccount(ctx, binding.AccountID, func(tx pgx.Tx) error {
		q := db.New(tx)

		// A known machine is being reinstalled: it occupies a seat already, so
		// the limit does not apply to it. pgx.ErrNoRows is the only error that
		// means "new machine"; anything else is a real failure.
		_, err := q.GetComputerByGUID(ctx, db.GetComputerByGUIDParams{
			AccountID: binding.AccountID, MachineGuid: req.MachineGUID,
		})
		switch {
		case err == nil:
			// existing machine, no limit check
		case errors.Is(err, pgx.ErrNoRows):
			count, err := q.CountComputers(ctx)
			if err != nil {
				return err
			}
			limit, err := q.GetAccountComputerLimit(ctx, binding.AccountID)
			if err != nil {
				return err
			}
			if count >= int64(limit) {
				overLimit = true
				return nil
			}
		default:
			return err
		}

		computer, err := q.UpsertComputerByGUID(ctx, db.UpsertComputerByGUIDParams{
			AccountID: binding.AccountID, MachineGuid: req.MachineGUID,
			Hostname: sanitizeText(req.Hostname), OsName: sanitizeText(req.OSName),
			OsBuild: sanitizeText(req.OSBuild), Arch: sanitizeText(req.Arch),
			AgentVersion: sanitizeText(req.AgentVersion),
			Hardware:     sanitizeJSONObject(req.Hardware), TokenHash: agentHash,
		})
		if err != nil {
			return err
		}
		return s.recordEvent(ctx, tx, eventInput{
			AccountID:  binding.AccountID,
			ComputerID: &computer.ID,
			Type:       "computer.enrolled",
			Payload: map[string]any{
				"hostname":      sanitizeText(req.Hostname),
				"agent_version": sanitizeText(req.AgentVersion),
			},
		})
	})
	if err != nil {
		slog.Error("enroll", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Could not enroll this computer"})
		return
	}
	if overLimit {
		writeJSON(w, http.StatusPaymentRequired, ErrorResponse{
			Error: "Your plan does not cover another computer. Upgrade in the cabinet and run the installer again.",
		})
		return
	}

	slog.Info("computer enrolled", "account_id", binding.AccountID, "hostname", req.Hostname)
	writeJSON(w, http.StatusCreated, map[string]string{"agent_token": agentPlain})
}

func (s *Server) handleAgentSync(w http.ResponseWriter, r *http.Request) {
	computer, ok := computerFrom(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, ErrorResponse{Error: "Unauthorized"})
		return
	}

	resp := ClientSyncResponse{
		Applications: []ClientApplication{},
		Mode:         "free",
		Client:       []ClientEntry{},
	}

	// Telemetry is recorded in its own transaction, deliberately outside the one
	// that computes the policy. "Telemetry must never stop enforcement" is only
	// true if a failed telemetry write cannot fail the request — and there are
	// more ways for a jsonb write to fail than any input filter will enumerate.
	// A lost last_seen_at is a degraded fleet view; a failed sync is a machine
	// running with no policy at all.
	if err := s.inAccount(r.Context(), computer.AccountID, func(tx pgx.Tx) error {
		return db.New(tx).TouchComputer(r.Context(), db.TouchComputerParams{
			ID: computer.ID, Runtime: readRuntime(r),
		})
	}); err != nil {
		slog.Error("record agent telemetry", "computer_id", computer.ID, "error", err)
	}

	// A blocked machine is locked, not unmanaged. Expressed in the wire format
	// the agent already speaks: whitelist mode with an empty list means "allow
	// nothing but the system processes the agent protects unconditionally".
	// Sending "free" here would mean pressing "block this computer" in the
	// cabinet switched protection OFF — the exact opposite of the button.
	if computer.Blocked {
		resp.Mode = "whitelist"
		writeJSON(w, http.StatusOK, resp)
		return
	}

	// No room at all: nothing is enforced. A freshly enrolled machine must not
	// start killing processes before someone deliberately placed it somewhere.
	if computer.RoomID == nil {
		writeJSON(w, http.StatusOK, resp)
		return
	}

	err := s.inAccount(r.Context(), computer.AccountID, func(tx pgx.Tx) error {
		q := db.New(tx)
		room, err := q.GetRoom(r.Context(), *computer.RoomID)
		if err != nil {
			// A room that has been deleted out from under the machine is a
			// legitimate "nothing to enforce"; a database failure is not, and
			// collapsing the two would hide an outage behind a quiet free mode.
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		if !room.ProtectionEnabled {
			return nil
		}
		apps, err := q.ListRoomApplications(r.Context(), room.ID)
		if err != nil {
			return err
		}
		for _, a := range apps {
			if a.Enabled && a.List == room.Mode {
				resp.Applications = append(resp.Applications,
					ClientApplication{Name: a.Name, Mode: a.List})
			}
		}
		resp.Mode = room.Mode
		resp.Client = []ClientEntry{{Name: "power", Status: room.PowerAllowed}}
		return nil
	})
	if err != nil {
		slog.Error("agent sync", "computer_id", computer.ID, "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Sync failed"})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// readRuntime collects the volatile half of the passport the agent reports on
// every sync. Anything it cannot vouch for becomes an empty object.
func readRuntime(r *http.Request) []byte {
	return sanitizeJSONObject([]byte(r.URL.Query().Get("runtime")))
}

// sanitizeJSONObject decodes raw as a JSON object and re-encodes it, so the
// bytes handed to a jsonb column are ones Postgres will actually accept.
// Anything it cannot vouch for becomes an empty object.
//
// json.Valid is NOT a sufficient gate: it accepts things jsonb rejects. Both of
// these are valid JSON and both make Postgres error —
//
//	a JSON string holding a NUL escape    ERROR: unsupported Unicode escape sequence
//	a JSON string holding invalid UTF-8   ERROR: invalid byte sequence for encoding "UTF8"
//
// — and a Windows machine on a non-UTF-8 codepage is exactly how the second one
// reaches us, in agent telemetry (readRuntime) as much as in the hardware
// inventory sent at enrollment. So the value is decoded and re-encoded:
// decoding replaces invalid UTF-8 with U+FFFD, requiring an object rejects the
// scalars the column is not meant to hold, and the NUL escape is checked for
// explicitly because Go emits it again on the way out.
func sanitizeJSONObject(raw []byte) []byte {
	const empty = `{}`
	if len(raw) == 0 {
		return []byte(empty)
	}
	var probe map[string]any
	if err := json.Unmarshal(raw, &probe); err != nil {
		return []byte(empty)
	}
	encoded, err := json.Marshal(probe)
	if err != nil {
		return []byte(empty)
	}
	// Go re-emits a NUL as a six-character escape, so it is those six
	// bytes that have to be looked for, spelled out here rather than
	// written as a literal that an editor can silently interpret.
	if bytes.Contains(encoded, []byte{'\\', 'u', '0', '0', '0', '0'}) {
		return []byte(empty)
	}
	return encoded
}

// sanitizeText makes a plain string safe for a Postgres text column, the same
// destination sanitizeJSONObject protects for jsonb. A Postgres text column
// rejects an embedded NUL outright, and rejects bytes that are not valid
// UTF-8 — exactly the two shapes a machine on a non-UTF-8 codepage can put in
// a hostname, OS name, build, or architecture string. Unlike jsonb there is no
// decode/re-encode round trip available for a bare string, so the two
// failures are handled directly: NUL bytes are stripped, and anything left
// that is not valid UTF-8 is replaced rather than rejected.
func sanitizeText(s string) string {
	return strings.ToValidUTF8(strings.ReplaceAll(s, "\x00", ""), "�")
}
