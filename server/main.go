package main

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
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
	systemCache      map[string]bool
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

// System represents a system entry
type System struct {
	Name   string `json:"name"`
	Status bool   `json:"status"`
}

// SystemResponse represents a system response
type SystemResponse struct {
	Systems []System `json:"systems"`
}

// ServerInfoResponse represents server information
type ServerInfoResponse struct {
	ServerIP   string `json:"server_ip"`
	ServerPort string `json:"server_port"`
	Version    string `json:"version"`
	Status     string `json:"status"`
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
		enabledCache:     false, // Start with server disabled by default
		systemCache:      make(map[string]bool),
	}

	if err := server.initDatabase(); err != nil {
		return nil, fmt.Errorf("failed to initialize database: %v", err)
	}

	if err := server.loadFromDatabase(); err != nil {
		return nil, fmt.Errorf("failed to load data from database: %v", err)
	}

	// Always start with server disabled on restart
	if err := server.forceDisableOnStartup(); err != nil {
		return nil, fmt.Errorf("failed to disable server on startup: %v", err)
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
		enabled BOOLEAN NOT NULL DEFAULT 0,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	INSERT OR IGNORE INTO server_status (id, enabled) VALUES (1, 0);
	`

	// Create system table
	createSystemTable := `
	CREATE TABLE IF NOT EXISTS system (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT UNIQUE NOT NULL,
		status BOOLEAN NOT NULL DEFAULT 1,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_system_name ON system(name);
	INSERT OR IGNORE INTO system (name, status) VALUES ('power', 1);
	`

	if _, err := s.db.Exec(createBAppsTable); err != nil {
		return fmt.Errorf("failed to create blocked_applications table: %v", err)
	}

	if _, err := s.db.Exec(createStatusTable); err != nil {
		return fmt.Errorf("failed to create server_status table: %v", err)
	}

	if _, err := s.db.Exec(createSystemTable); err != nil {
		return fmt.Errorf("failed to create system table: %v", err)
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

	// Load system data
	systemRows, err := s.db.Query("SELECT name, status FROM system")
	if err != nil {
		return fmt.Errorf("failed to query system data: %v", err)
	}
	defer systemRows.Close()

	s.systemCache = make(map[string]bool)
	for systemRows.Next() {
		var name string
		var status bool
		if err := systemRows.Scan(&name, &status); err != nil {
			return fmt.Errorf("failed to scan system data: %v", err)
		}
		s.systemCache[name] = status
	}

	log.Printf("Loaded %d blocked applications, %d system entries, and status (enabled: %v) from database", len(s.blockedAppsCache), len(s.systemCache), s.enabledCache)
	return nil
}

// forceDisableOnStartup ensures server starts disabled on every restart
func (s *Server) forceDisableOnStartup() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Set cache to disabled
	s.enabledCache = false

	// Update database to disabled
	if err := s.saveStatusToDatabase(false); err != nil {
		return fmt.Errorf("failed to set startup status to disabled: %v", err)
	}

	log.Printf("Server status forced to disabled on startup")
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

// saveSystemToDatabase saves or updates system data in SQLite
func (s *Server) saveSystemToDatabase(name string, status bool) error {
	_, err := s.db.Exec("INSERT OR REPLACE INTO system (name, status, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)", name, status)
	if err != nil {
		return fmt.Errorf("failed to save system to database: %v", err)
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

	// Initialize apps list
	apps := make([]string, 0)

	// If server is enabled, add blocked applications
	if s.enabledCache {
		// Convert cache map keys to slice
		for app := range s.blockedAppsCache {
			apps = append(apps, app)
		}
	}

	// Always check if power system is disabled, add force_poweroff if so
	if powerStatus, exists := s.systemCache["power"]; exists && !powerStatus {
		apps = append(apps, "force_poweroff")
	}

	response := ApplicationsResponse{
		Applications: apps,
	}
	json.NewEncoder(w).Encode(response)
}

// Middleware for token authentication
func withAuth(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var token = os.Getenv("TOKEN")
		if token == "" {
			token = "mILp9n6shk3G9SGSaS2nmP6YlLHwsP1Z"
		}
		authHeader := r.Header.Get("Authorization")
		expected := "Bearer " + token

		if authHeader != expected {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// If authorized, call the next handler
		handler(w, r)
	}
}

// getAllApplications returns the complete list of blocked applications regardless of server status
func (s *Server) getAllApplications(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")

	// Always return the full list regardless of server status
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

// getSystem returns the list of system entries
func (s *Server) getSystem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")

	// Convert cache map to slice of System structs
	systems := make([]System, 0, len(s.systemCache))
	for name, status := range s.systemCache {
		systems = append(systems, System{Name: name, Status: status})
	}

	response := SystemResponse{
		Systems: systems,
	}
	json.NewEncoder(w).Encode(response)
}

// updateSystem updates system status
func (s *Server) updateSystem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	var system System
	if err := json.NewDecoder(r.Body).Decode(&system); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Invalid JSON"})
		return
	}

	if strings.TrimSpace(system.Name) == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "System name cannot be empty"})
		return
	}

	s.mu.Lock()
	// Update cache
	s.systemCache[system.Name] = system.Status
	// Save to database
	if err := s.saveSystemToDatabase(system.Name, system.Status); err != nil {
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: fmt.Sprintf("Failed to save to database: %v", err)})
		return
	}
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	statusText := "disabled"
	if system.Status {
		statusText = "enabled"
	}
	json.NewEncoder(w).Encode(map[string]string{"message": fmt.Sprintf("System '%s' %s", system.Name, statusText)})
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

// loadEnvFile loads environment variables from .env file
func loadEnvFile(filename string) error {
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Parse KEY=VALUE format
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			// Remove quotes if present
			if len(value) >= 2 {
				if (strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"")) ||
					(strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'")) {
					value = value[1 : len(value)-1]
				}
			}
			// Set environment variable if not already set
			if os.Getenv(key) == "" {
				os.Setenv(key, value)
			}
		}
	}
	return scanner.Err()
}

// getWebDir returns the web directory path from environment or default
func getWebDir() string {
	// Try to get from environment variable
	webDir := os.Getenv("WEB_DIR")
	if webDir == "" {
		// Default fallback
		webDir = "../bin/web"
	}
	return webDir
}

// serveStaticFiles serves static files from the web directory
func serveStaticFiles() http.Handler {
	// Get the web directory from environment or default
	webDir := getWebDir()
	absWebDir, err := filepath.Abs(webDir)
	if err != nil {
		log.Printf("Warning: Could not resolve absolute path for web directory: %v", err)
		absWebDir = webDir
	}

	// Check if web directory exists
	if _, err := os.Stat(absWebDir); os.IsNotExist(err) {
		log.Printf("Warning: Web directory does not exist at %s", absWebDir)
		// Return a handler that serves a simple message
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`
			<!DOCTYPE html>
			<html>
			<head><title>ProcSentinel Web Interface</title></head>
			<body>
				<h1>ProcSentinel Server</h1>
				<p>Web interface not available. Build the Flutter web app first:</p>
				<pre>cd mobile && flutter build web --web-renderer html --base-href / --output ../bin/web</pre>
				<h2>API Endpoints:</h2>
				<ul>
					<li><a href="/applications">/applications</a> - Get blocked applications</li>
					<li><a href="/status">/status</a> - Server status</li>
					<li><a href="/info">/info</a> - Server information</li>
					<li><a href="/health">/health</a> - Health check</li>
				</ul>
			</body>
			</html>
			`))
		})
	}

	log.Printf("Serving static files from: %s", absWebDir)
	return http.FileServer(http.Dir(absWebDir))
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
	http.HandleFunc("/applications", withAuth(func(w http.ResponseWriter, r *http.Request) {
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
	}))

	http.HandleFunc("/applications/", withAuth(s.removeApplication))

	// Get all applications endpoint - always returns full list regardless of server status
	http.HandleFunc("/applications/all", withAuth(s.getAllApplications))

	http.HandleFunc("/status", withAuth(func(w http.ResponseWriter, r *http.Request) {
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
	}))

	// Reset endpoint - remove all applications
	http.HandleFunc("/reset", withAuth(s.resetApplications))

	// Server info endpoint
	http.HandleFunc("/info", withAuth(s.getServerInfo))

	// System endpoint
	http.HandleFunc("/system", withAuth(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.getSystem(w, r)
		case http.MethodPut:
			s.updateSystem(w, r)
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		}
	}))

	// Health check endpoint
	http.HandleFunc("/health", withAuth(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))

	// Serve static files from /bin/web at root path
	// This should be the last route to catch all remaining requests
	http.Handle("/", serveStaticFiles())
}

func main() {
	// Load environment variables from .env file
	if err := loadEnvFile("../.env"); err != nil {
		log.Printf("Warning: Could not load .env file: %v", err)
	}

	server, err := NewServer()
	if err != nil {
		log.Fatalf("Failed to create server: %v", err)
	}
	defer server.Close()

	server.setupRoutes()

	port := "0.0.0.0:8080"
	fmt.Printf("ProcSentinel Server starting on port %s\n", port)
	fmt.Println("\n🌐 Web Interface: http://localhost:8080/")
	fmt.Println("\nAPI Endpoints:")
	fmt.Println("  GET    /applications     - Get list of blocked applications (empty when disabled)")
	fmt.Println("  GET    /applications/all - Get ALL blocked applications (always returns full list)")
	fmt.Println("  POST   /applications     - Add new blocked application")
	fmt.Println("  DELETE /applications/{name} - Remove blocked application")
	fmt.Println("  DELETE /reset            - Remove all blocked applications")
	fmt.Println("  GET    /status          - Get server status")
	fmt.Println("  PUT    /status          - Update server status (enable/disable)")
	fmt.Println("  GET    /info            - Get server information (IP, version, status)")
	fmt.Println("  GET    /system          - Get system entries")
	fmt.Println("  PUT    /system          - Update system status")
	fmt.Println("  GET    /health          - Health check")
	fmt.Printf("\n📁 Static Files: Serving from %s at root path (/)\n", getWebDir())

	log.Fatal(http.ListenAndServe(port, nil))
}
