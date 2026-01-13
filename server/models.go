package main

// Application represents an application entry
type Application struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Mode    string `json:"mode"`
}

// ClientApplication represents an application entry for clients
type ClientApplication struct {
	Name string `json:"name"`
	Mode string `json:"mode"`
}

// StatusResponse represents the server status
type StatusResponse struct {
	Enabled bool   `json:"enabled"`
	Mode    string `json:"mode"`
}

// ApplicationsResponse represents the list of applications
type ApplicationsResponse struct {
	Applications []Application `json:"applications"`
}

// ClientSyncResponse represents the full client sync response
type ClientSyncResponse struct {
	Applications []ClientApplication `json:"applications"`
	Mode         string              `json:"mode"`
	Client       []ClientEntry        `json:"client"`
}

// ErrorResponse represents an error response
type ErrorResponse struct {
	Error string `json:"error"`
}

// ClientEntry represents a client entry
type ClientEntry struct {
	Name   string `json:"name"`
	Status bool   `json:"status"`
}

// ClientEntryResponse represents a client entry response
type ClientEntryResponse struct {
	Entries []ClientEntry `json:"entries"`
}

// Computer represents a computer record
type Computer struct {
	Identity int    `json:"identity"`
	Blocked  bool   `json:"blocked"`
	DateTime string `json:"datetime"`
}

// ComputersResponse represents the list of computers
type ComputersResponse struct {
	Computers   []Computer `json:"computers"`
	CurrentTime string     `json:"current_time"`
}

// ServerInfoResponse represents server information
type ServerInfoResponse struct {
	ServerIP   string `json:"server_ip"`
	ServerPort string `json:"server_port"`
	Version    string `json:"version"`
	Status     string `json:"status"`
	Mode       string `json:"mode"`
}
