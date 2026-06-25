package main

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// healthTimeout bounds the database ping. A probe that hangs for the full
// request timeout is as useless to an orchestrator as one that lies.
const healthTimeout = 2 * time.Second

// handleHealth answers 200 only when Postgres answers too. Without the ping
// the endpoint reports "ok" straight through a database outage, so the
// compose healthcheck, a load balancer, or an uptime monitor would keep
// routing traffic to a server that can serve nothing but this one route.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), healthTimeout)
	defer cancel()
	if err := s.pool.Ping(ctx); err != nil {
		slog.Error("health check: database unreachable", "error", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status": "degraded", "database": "unreachable",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
