package main

import (
	"net/http"
)

// handleGetServerInfo reports the server's own version and liveness. It does
// not report a mode: mode is a property of a room now, not of the server,
// and this handler has no tenant in scope to report one for.
func (s *Server) handleGetServerInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, ServerInfoResponse{
		Version: "1.0.0",
		Status:  "ok",
	})
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
