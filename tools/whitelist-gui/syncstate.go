package main

import (
	"encoding/json"
	"os"
)

type clientApplication struct {
	Name string `json:"name"`
	Mode string `json:"mode"`
}

// syncState mirrors the subset of sync.json the agent writes to disk.
type syncState struct {
	Applications []clientApplication `json:"applications"`
	Mode         string              `json:"mode"`
}

// loadSyncState reads the agent's cached server state. It is informational
// only, but it is what tells the user whether the whitelist is enforced at all:
// whitelist.txt is consulted only when the server mode is "whitelist".
func loadSyncState(path string) (*syncState, error) {
	data, err := os.ReadFile(ioPath(path))
	if err != nil {
		return nil, err
	}

	var s syncState
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}
