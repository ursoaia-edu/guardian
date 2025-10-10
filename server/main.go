package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"

	_ "github.com/mattn/go-sqlite3"
)

// Server holds the state of the procsentinel server
type Server struct {
	mu sync.RWMutex
	db *sql.DB
	// In-memory cache for fast access
	blockedAppsCache map[string]bool
	enabledCache     bool
}

// Application represents a blocked application
type Application struct {
	Name string `json:"name"`
}

// StatusResponse represents the server status
type StatusResponse struct {
	Enabled bool `json:"enabled"`
}

// ApplicationsResponse represents the list of blocked applications
type ApplicationsResponse struct {
	Applications []string `json:"applications"`
}

// ErrorResponse represents an error response
type ErrorResponse struct {
	Error string `json:"error"`
}

// ServerInfoResponse represents server information
type ServerInfoResponse struct {
	ServerIP    string `json:"server_ip"`
	ServerPort  string `json:"server_port"`
	Version     string `json:"version"`
	Status      string `json:"status"`
}

// NewServer creates a new server instance
func NewServer() (*Server, error) {
	db, err := sql.Open("sqlite3", "./procsentinel.db")
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %v", err)
	}

	server := &Server{
		db:               db,
		blockedAppsCache: make(map[string]bool),
		enabledCache:     true,
	}

	if err := server.initDatabase(); err != nil {
		return nil, fmt.Errorf("failed to initialize database: %v", err)
	}

	if err := server.loadFromDatabase(); err != nil {
		return nil, fmt.Errorf("failed to load data from database: %v", err)
	}

	return server, nil
}

// initDatabase creates the necessary tables
func (s *Server) initDatabase() error {
	// Create blocked_applications table
	createBAppsTable := `
	CREATE TABLE IF NOT EXISTS blocked_applications (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT UNIQUE NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_blocked_apps_name ON blocked_applications(name);
	`

	// Create server_status table
	createStatusTable := `
	CREATE TABLE IF NOT EXISTS server_status (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		enabled BOOLEAN NOT NULL DEFAULT 1,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	INSERT OR IGNORE INTO server_status (id, enabled) VALUES (1, 1);
	`

	if _, err := s.db.Exec(createBAppsTable); err != nil {
		return fmt.Errorf("failed to create blocked_applications table: %v", err)
	}

	if _, err := s.db.Exec(createStatusTable); err != nil {
		return fmt.Errorf("failed to create server_status table: %v", err)
	}

	return nil
}

// loadFromDatabase loads data from SQLite into cache
func (s *Server) loadFromDatabase() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Load blocked applications
	rows, err := s.db.Query("SELECT name FROM blocked_applications")
	if err != nil {
		return fmt.Errorf("failed to query blocked applications: %v", err)
	}
	defer rows.Close()

	s.blockedAppsCache = make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return fmt.Errorf("failed to scan application name: %v", err)
		}
		s.blockedAppsCache[name] = true
	}

	// Load server status
	var enabled bool
	err = s.db.QueryRow("SELECT enabled FROM server_status WHERE id = 1").Scan(&enabled)
	if err != nil {
		return fmt.Errorf("failed to query server status: %v", err)
	}
	s.enabledCache = enabled

	log.Printf("Loaded %d blocked applications and status (enabled: %v) from database", len(s.blockedAppsCache), s.enabledCache)
	return nil
}

// saveApplicationToDatabase saves a blocked application to SQLite
func (s *Server) saveApplicationToDatabase(name string) error {
	_, err := s.db.Exec("INSERT OR IGNORE INTO blocked_applications (name) VALUES (?)", name)
	if err != nil {
		return fmt.Errorf("failed to save application to database: %v", err)
	}
	return nil
}

// removeApplicationFromDatabase removes a blocked application from SQLite
func (s *Server) removeApplicationFromDatabase(name string) error {
	_, err := s.db.Exec("DELETE FROM blocked_applications WHERE name = ?", name)
	if err != nil {
		return fmt.Errorf("failed to remove application from database: %v", err)
	}
	return nil
}

// saveStatusToDatabase saves server status to SQLite
func (s *Server) saveStatusToDatabase(enabled bool) error {
	_, err := s.db.Exec("UPDATE server_status SET enabled = ?, updated_at = CURRENT_TIMESTAMP WHERE id = 1", enabled)
	if err != nil {
		return fmt.Errorf("failed to save status to database: %v", err)
	}
	return nil
}

// resetApplicationsDatabase removes all blocked applications from SQLite
func (s *Server) resetApplicationsDatabase() error {
	_, err := s.db.Exec("DELETE FROM blocked_applications")
	if err != nil {
		return fmt.Errorf("failed to reset applications in database: %v", err)
	}
	return nil
}

// Close closes the database connection
func (s *Server) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// getApplications returns the list of blocked applications
func (s *Server) getApplications(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")

	// If server is disabled, return empty list
	if !s.enabledCache {
		response := ApplicationsResponse{
			Applications: []string{},
		}
		json.NewEncoder(w).Encode(response)
		return
	}

	// Convert cache map keys to slice
	apps := make([]string, 0, len(s.blockedAppsCache))
	for app := range s.blockedAppsCache {
		apps = append(apps, app)
	}

	response := ApplicationsResponse{
		Applications: apps,
	}
	json.NewEncoder(w).Encode(response)
}

// addApplication adds a new application to the blocked list
func (s *Server) addApplication(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	var app Application
	if err := json.NewDecoder(r.Body).Decode(&app); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Invalid JSON"})
		return
	}

	if strings.TrimSpace(app.Name) == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Application name cannot be empty"})
		return
	}

	s.mu.Lock()
	// Update cache
	s.blockedAppsCache[app.Name] = true
	// Save to database
	if err := s.saveApplicationToDatabase(app.Name); err != nil {
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: fmt.Sprintf("Failed to save to database: %v", err)})
		return
	}
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"message": fmt.Sprintf("Application '%s' added to blocked list", app.Name)})
}

// removeApplication removes an application from the blocked list
func (s *Server) removeApplication(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	// Extract application name from URL path
	path := strings.TrimPrefix(r.URL.Path, "/applications/")
	if path == "" || path == r.URL.Path {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Application name is required in URL path"})
		return
	}

	s.mu.Lock()
	if _, exists := s.blockedAppsCache[path]; !exists {
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(ErrorResponse{Error: fmt.Sprintf("Application '%s' not found in blocked list", path)})
		return
	}
	// Remove from cache
	delete(s.blockedAppsCache, path)
	// Remove from database
	if err := s.removeApplicationFromDatabase(path); err != nil {
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: fmt.Sprintf("Failed to remove from database: %v", err)})
		return
	}
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": fmt.Sprintf("Application '%s' removed from blocked list", path)})
}

// getStatus returns the current server status
func (s *Server) getStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	s.mu.RLock()
	enabled := s.enabledCache
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(StatusResponse{Enabled: enabled})
}

// updateStatus updates the server status (enable/disable)
func (s *Server) updateStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	var status StatusResponse
	if err := json.NewDecoder(r.Body).Decode(&status); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Invalid JSON"})
		return
	}

	s.mu.Lock()
	// Update cache
	s.enabledCache = status.Enabled
	// Save to database
	if err := s.saveStatusToDatabase(status.Enabled); err != nil {
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: fmt.Sprintf("Failed to save status to database: %v", err)})
		return
	}
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	statusText := "disabled"
	if status.Enabled {
		statusText = "enabled"
	}
	json.NewEncoder(w).Encode(map[string]string{"message": fmt.Sprintf("Server %s", statusText)})
}

// getServerInfo returns server information including local IP
func (s *Server) getServerInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	// Get local IP address
	localIP := getLocalIP()
	
	// Get server status
	s.mu.RLock()
	serverStatus := "enabled"
	if !s.enabledCache {
		serverStatus = "disabled"
	}
	s.mu.RUnlock()

	response := ServerInfoResponse{
		ServerIP:   localIP,
		ServerPort: "8080",
		Version:    "1.0.0",
		Status:     serverStatus,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// getLocalIP returns the local IP address of the server
func getLocalIP() string {
	// Try to get the local IP by connecting to a remote address
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		// Fallback: get all interfaces and find a non-loopback IP
		addrs, err := net.InterfaceAddrs()
		if err != nil {
			return "127.0.0.1"
		}

		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
				if ipNet.IP.To4() != nil {
					return ipNet.IP.String()
				}
			}
		}
		return "127.0.0.1"
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String()
}

// resetApplications removes all applications from the blocked list
func (s *Server) resetApplications(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	s.mu.Lock()
	// Get count before clearing
	count := len(s.blockedAppsCache)
	// Clear cache
	s.blockedAppsCache = make(map[string]bool)
	// Clear database
	if err := s.resetApplicationsDatabase(); err != nil {
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: fmt.Sprintf("Failed to reset applications in database: %v", err)})
		return
	}
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": fmt.Sprintf("Reset complete: removed %d applications from blocked list", count)})
}

// setupRoutes configures the HTTP routes
func (s *Server) setupRoutes() {
	http.HandleFunc("/applications", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.getApplications(w, r)
		case http.MethodPost:
			s.addApplication(w, r)
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		}
	})

	http.HandleFunc("/applications/", s.removeApplication)

	http.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.getStatus(w, r)
		case http.MethodPut:
			s.updateStatus(w, r)
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		}
	})

	// Reset endpoint - remove all applications
	http.HandleFunc("/reset", s.resetApplications)

	// Server info endpoint
	http.HandleFunc("/info", s.getServerInfo)

	// Health check endpoint
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
}

func main() {
	server, err := NewServer()
	if err != nil {
		log.Fatalf("Failed to create server: %v", err)
	}
	defer server.Close()

	server.setupRoutes()

	port := "0.0.0.0:8080"
	fmt.Printf("ProcSentinel Server starting on port %s\n", port)
	fmt.Println("Available endpoints:")
	fmt.Println("  GET    /applications     - Get list of blocked applications")
	fmt.Println("  POST   /applications     - Add new blocked application")
	fmt.Println("  DELETE /applications/{name} - Remove blocked application")
	fmt.Println("  DELETE /reset            - Remove all blocked applications")
	fmt.Println("  GET    /status          - Get server status")
	fmt.Println("  PUT    /status          - Update server status (enable/disable)")
	fmt.Println("  GET    /info            - Get server information (IP, version, status)")
	fmt.Println("  GET    /health          - Health check")

	log.Fatal(http.ListenAndServe(port, nil))
}
