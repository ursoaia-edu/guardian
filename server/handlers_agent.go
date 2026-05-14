package main

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
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

		hardware := req.Hardware
		if len(hardware) == 0 {
			hardware = json.RawMessage("{}")
		}
		_, err = q.UpsertComputerByGUID(ctx, db.UpsertComputerByGUIDParams{
			AccountID: binding.AccountID, MachineGuid: req.MachineGUID,
			Hostname: req.Hostname, OsName: req.OSName, OsBuild: req.OSBuild,
			Arch: req.Arch, AgentVersion: req.AgentVersion,
			Hardware: hardware, TokenHash: agentHash,
		})
		return err
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
