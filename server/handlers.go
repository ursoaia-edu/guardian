package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
)

func (s *Server) handleClientSync(w http.ResponseWriter, r *http.Request) {
	identityStr := r.URL.Query().Get("identity")
	isComputerBlocked := true

	if identityStr != "" {
		if identity, err := parseIntParam(identityStr); err == nil {
			var blocked bool
			err := s.db.QueryRow("SELECT blocked FROM computers WHERE identity = ?", identity).Scan(&blocked)
			if err == nil || err == sql.ErrNoRows {
				isComputerBlocked = blocked
				if err := s.updateComputerDateTime(identity); err != nil {
					slog.Error("failed to update computer record", "identity", identity, "error", err)
				}
			}
		} else {
			slog.Warn("invalid identity parameter", "identity", identityStr)
		}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	apps := make([]ClientApplication, 0)
	entries := make([]ClientEntry, 0)

	if s.enabledCache && (identityStr == "" || isComputerBlocked) {
		for _, app := range s.appsCache {
			if app.Enabled && app.Mode == s.modeCache {
				apps = append(apps, ClientApplication{Name: app.Name, Mode: app.Mode})
			}
		}
		for name, status := range s.clientCache {
			entries = append(entries, ClientEntry{Name: name, Status: status})
		}
	}

	slog.Info("client sync request", "count", len(apps), "mode", s.modeCache)
	writeJSON(w, http.StatusOK, ClientSyncResponse{
		Applications: apps,
		Mode:         s.modeCache,
		Client:       entries,
	})
}

func (s *Server) handleGetAllApplications(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	apps := make([]Application, 0, len(s.appsCache))
	for _, app := range s.appsCache {
		apps = append(apps, app)
	}

	writeJSON(w, http.StatusOK, ApplicationsResponse{Applications: apps})
}

func (s *Server) handleAddApplication(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Invalid JSON"})
		return
	}

	if strings.TrimSpace(req.Name) == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Application name cannot be empty"})
		return
	}

	if req.Mode == "" {
		req.Mode = "blacklist"
	}
	if req.Mode != "blacklist" && req.Mode != "whitelist" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Mode must be 'blacklist' or 'whitelist'"})
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.saveApplicationToDatabase(req.Name, req.Mode); err != nil {
		slog.Error("failed to save application", "name", req.Name, "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Failed to save to database"})
		return
	}

	// Reload from DB to get the ID
	var app Application
	err := s.db.QueryRow("SELECT id, name, enabled, mode FROM applications WHERE name = ?", req.Name).
		Scan(&app.ID, &app.Name, &app.Enabled, &app.Mode)
	if err != nil {
		slog.Error("failed to read back application", "name", req.Name, "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Failed to read back application"})
		return
	}
	s.appsCache[app.Name] = app

	slog.Info("application added", "name", req.Name, "mode", req.Mode)
	writeJSON(w, http.StatusCreated, map[string]string{"message": fmt.Sprintf("Application '%s' added", req.Name)})
}

func (s *Server) handleUpdateApplication(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    string      `json:"name"`
		Enabled interface{} `json:"enabled"`
		Mode    string      `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Invalid JSON"})
		return
	}

	var enabledInt int
	switch v := req.Enabled.(type) {
	case bool:
		if v {
			enabledInt = 1
		}
	case float64:
		enabledInt = int(v)
	default:
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Invalid enabled field type"})
		return
	}

	if strings.TrimSpace(req.Name) == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Application name cannot be empty"})
		return
	}

	if req.Mode != "" && req.Mode != "blacklist" && req.Mode != "whitelist" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Mode must be 'blacklist' or 'whitelist'"})
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.appsCache[req.Name]; !exists {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: fmt.Sprintf("Application '%s' not found", req.Name)})
		return
	}

	var err error
	if req.Mode != "" {
		_, err = s.db.Exec("UPDATE applications SET enabled = ?, mode = ? WHERE name = ?", enabledInt, req.Mode, req.Name)
	} else {
		_, err = s.db.Exec("UPDATE applications SET enabled = ? WHERE name = ?", enabledInt, req.Name)
	}
	if err != nil {
		slog.Error("failed to update application", "name", req.Name, "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Failed to update database"})
		return
	}

	// Update cache
	app := s.appsCache[req.Name]
	app.Enabled = enabledInt == 1
	if req.Mode != "" {
		app.Mode = req.Mode
	}
	s.appsCache[req.Name] = app

	slog.Info("application updated", "name", req.Name, "enabled", enabledInt, "mode", req.Mode)
	writeJSON(w, http.StatusOK, map[string]string{"message": fmt.Sprintf("Application '%s' updated", req.Name)})
}

func (s *Server) handleRemoveApplication(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Invalid JSON"})
		return
	}

	if strings.TrimSpace(req.Name) == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Application name is required"})
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.appsCache[req.Name]; !exists {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: fmt.Sprintf("Application '%s' not found", req.Name)})
		return
	}

	if err := s.removeApplicationFromDatabase(req.Name); err != nil {
		slog.Error("failed to remove application", "name", req.Name, "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Failed to remove from database"})
		return
	}
	delete(s.appsCache, req.Name)

	slog.Info("application removed", "name", req.Name)
	writeJSON(w, http.StatusOK, map[string]string{"message": fmt.Sprintf("Application '%s' removed", req.Name)})
}

func (s *Server) handleResetApplications(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	count := len(s.appsCache)
	if err := s.resetApplicationsDatabase(); err != nil {
		slog.Error("failed to reset applications", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Failed to reset applications"})
		return
	}
	s.appsCache = make(map[string]Application)

	slog.Info("applications reset", "removed", count)
	writeJSON(w, http.StatusOK, map[string]string{"message": fmt.Sprintf("Reset complete: removed %d applications", count)})
}

func (s *Server) handleGetStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	enabled := s.enabledCache
	mode := s.modeCache
	s.mu.RUnlock()

	writeJSON(w, http.StatusOK, StatusResponse{Enabled: enabled, Mode: mode})
}

func (s *Server) handleUpdateStatus(w http.ResponseWriter, r *http.Request) {
	var status StatusResponse
	if err := json.NewDecoder(r.Body).Decode(&status); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Invalid JSON"})
		return
	}

	if status.Mode != "" && status.Mode != "blacklist" && status.Mode != "whitelist" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Mode must be 'blacklist' or 'whitelist'"})
		return
	}

	s.mu.Lock()
	s.enabledCache = status.Enabled
	if status.Mode != "" {
		s.modeCache = status.Mode
	}
	if err := s.saveStatusToDatabase(s.enabledCache, s.modeCache); err != nil {
		s.mu.Unlock()
		slog.Error("failed to save status", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Failed to save status"})
		return
	}
	s.mu.Unlock()

	statusText := "disabled"
	if status.Enabled {
		statusText = "enabled"
	}
	slog.Info("server status updated", "status", statusText, "mode", s.modeCache)
	writeJSON(w, http.StatusOK, map[string]string{"message": fmt.Sprintf("Server %s (mode: %s)", statusText, s.modeCache)})
}

func (s *Server) handleGetServerInfo(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	serverStatus := "enabled"
	if !s.enabledCache {
		serverStatus = "disabled"
	}
	mode := s.modeCache
	s.mu.RUnlock()

	writeJSON(w, http.StatusOK, ServerInfoResponse{
		ServerIP:   getLocalIP(),
		ServerPort: "8080",
		Version:    "1.0.0",
		Status:     serverStatus,
		Mode:       mode,
	})
}

func (s *Server) handleGetClient(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries := make([]ClientEntry, 0, len(s.clientCache))
	for name, status := range s.clientCache {
		entries = append(entries, ClientEntry{Name: name, Status: status})
	}
	writeJSON(w, http.StatusOK, ClientEntryResponse{Entries: entries})
}

func (s *Server) handleUpdateClient(w http.ResponseWriter, r *http.Request) {
	var entry ClientEntry
	if err := json.NewDecoder(r.Body).Decode(&entry); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Invalid JSON"})
		return
	}

	if strings.TrimSpace(entry.Name) == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Client entry name cannot be empty"})
		return
	}

	s.mu.Lock()
	s.clientCache[entry.Name] = entry.Status
	if err := s.saveClientToDatabase(entry.Name, entry.Status); err != nil {
		s.mu.Unlock()
		slog.Error("failed to save client entry", "name", entry.Name, "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Failed to save to database"})
		return
	}
	s.mu.Unlock()

	statusText := "disabled"
	if entry.Status {
		statusText = "enabled"
	}
	slog.Info("client entry updated", "name", entry.Name, "status", statusText)
	writeJSON(w, http.StatusOK, map[string]string{"message": fmt.Sprintf("Client '%s' %s", entry.Name, statusText)})
}

func (s *Server) handleGetComputers(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query("SELECT identity, blocked, datetime FROM computers ORDER BY identity ASC")
	if err != nil {
		slog.Error("failed to query computers", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Failed to fetch computers"})
		return
	}
	defer rows.Close()

	computers := make([]Computer, 0)
	for rows.Next() {
		var c Computer
		if err := rows.Scan(&c.Identity, &c.Blocked, &c.DateTime); err != nil {
			slog.Error("failed to scan computer", "error", err)
			continue
		}
		computers = append(computers, c)
	}

	writeJSON(w, http.StatusOK, ComputersResponse{Computers: computers, CurrentTime: getCurrentTime()})
}

func (s *Server) handleUpdateComputer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Identity int  `json:"identity"`
		Blocked  bool `json:"blocked"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Invalid JSON"})
		return
	}

	_, err := s.db.Exec("INSERT OR REPLACE INTO computers (identity, blocked) VALUES (?, ?)", req.Identity, req.Blocked)
	if err != nil {
		slog.Error("failed to update computer", "identity", req.Identity, "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Failed to update computer"})
		return
	}

	statusText := "unblocked"
	if req.Blocked {
		statusText = "blocked"
	}
	slog.Info("computer updated", "identity", req.Identity, "status", statusText)
	writeJSON(w, http.StatusOK, map[string]string{"message": fmt.Sprintf("Computer %d %s", req.Identity, statusText)})
}

func (s *Server) handleResetComputers(w http.ResponseWriter, r *http.Request) {
	if _, err := s.db.Exec("UPDATE computers SET blocked = 0"); err != nil {
		slog.Error("failed to reset computers", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Failed to reset computers"})
		return
	}

	slog.Info("all computers unblocked")
	writeJSON(w, http.StatusOK, map[string]string{"message": "All computers unblocked"})
}

func (s *Server) handleBlockAllComputers(w http.ResponseWriter, r *http.Request) {
	if _, err := s.db.Exec("UPDATE computers SET blocked = 1"); err != nil {
		slog.Error("failed to block all computers", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Failed to block computers"})
		return
	}

	slog.Info("all computers blocked")
	writeJSON(w, http.StatusOK, map[string]string{"message": "All computers blocked"})
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
