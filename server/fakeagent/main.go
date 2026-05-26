// Command fakeagent enrolls and syncs like the Windows agent, so the API can be
// exercised without a Windows machine.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"
)

// One client for both calls, with a timeout: a diagnostic utility that
// hangs against a wedged server is worse than one that fails.
var client = &http.Client{Timeout: 15 * time.Second}

func main() {
	server := flag.String("server", "http://localhost:8080", "server base URL")
	binding := flag.String("binding-token", "", "binding token from the cabinet")
	guid := flag.String("guid", "fake-agent-0001", "machine GUID to report")
	hostname := flag.String("hostname", "FAKE-PC", "hostname to report")
	interval := flag.Duration("interval", 20*time.Second, "sync interval")
	flag.Parse()

	if *binding == "" {
		fmt.Fprintln(os.Stderr, "-binding-token is required")
		os.Exit(1)
	}

	token, err := enroll(*server, *binding, *guid, *hostname)
	if err != nil {
		fmt.Fprintf(os.Stderr, "enroll: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("enrolled as %s\n", *hostname)

	consecutiveFailures := 0
	for {
		body, err := sync(*server, token)
		if err != nil {
			consecutiveFailures++
			fmt.Fprintf(os.Stderr, "sync: %v\n", err)
			// Five in a row is not a blip: the computer has been deleted, or
			// its token rotated by a reinstall elsewhere. Looping silently
			// forever would tell whoever is watching nothing at all.
			if consecutiveFailures >= 5 {
				fmt.Fprintln(os.Stderr, "giving up after 5 consecutive sync failures")
				os.Exit(1)
			}
		} else {
			consecutiveFailures = 0
			fmt.Printf("%s %s\n", time.Now().Format(time.TimeOnly), body)
		}
		time.Sleep(*interval)
	}
}

func enroll(server, binding, guid, hostname string) (string, error) {
	payload, _ := json.Marshal(map[string]any{
		"binding_token": binding,
		"machine_guid":  guid,
		"hostname":      hostname,
		"os_name":       "Windows 11",
		"os_build":      "22631",
		"arch":          "amd64",
		"agent_version": "fake",
		"hardware":      map[string]any{"cpu": "fake", "ram_gb": 16},
	})
	resp, err := client.Post(server+"/agent/enroll", "application/json", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, raw)
	}
	var out struct {
		AgentToken string `json:"agent_token"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	return out.AgentToken, nil
}

func sync(server, token string) (string, error) {
	runtime := url.QueryEscape(`{"uptime_s":1234,"user":"fake"}`)
	req, _ := http.NewRequest("GET", server+"/agent/sync?runtime="+runtime, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, raw)
	}
	return string(raw), nil
}
