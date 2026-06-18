package main

// ClientApplication represents an application entry for clients
type ClientApplication struct {
	Name string `json:"name"`
	Mode string `json:"mode"`
}

// ClientSyncResponse represents the full client sync response
type ClientSyncResponse struct {
	Applications []ClientApplication `json:"applications"`
	Mode         string              `json:"mode"`
	Client       []ClientEntry       `json:"client"`
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
