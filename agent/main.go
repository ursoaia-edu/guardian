package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// ApplicationsResponse represents the server response for blocked applications
type ApplicationsResponse struct {
	Applications []string `json:"applications"`
}

// loadEnvFile loads environment variables from .env file
func loadEnvFile() error {
	file, err := os.Open(".env")
	if err != nil {
		return fmt.Errorf("failed to open .env file: %v", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			os.Setenv(key, value)
		}
	}

	return scanner.Err()
}

// fetchBlockedApplications fetches the list of blocked applications from the server
func fetchBlockedApplications(serverAddress string) ([]string, error) {
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	var token = os.Getenv("TOKEN")
	if token == "" {
		token = "mILp9n6shk3G9SGSaS2nmP6YlLHwsP1Z"
	}

	req, err := http.NewRequest("GET", serverAddress+"/applications", nil)
	if err != nil {
		panic(err)
	}

	req.Header.Add("Authorization", "Bearer "+token)

	req.Header.Add("Accept", "application/json")

	resp, err := client.Do(req)

	if err != nil {
		return nil, fmt.Errorf("failed to connect to server: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	var response ApplicationsResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to decode response: %v", err)
	}

	return response.Applications, nil
}

// updateBlockedApplications fetches updated list from server
func updateBlockedApplications(serverAddress string, blocked *[]string) {
	for {
		apps, err := fetchBlockedApplications(serverAddress)
		if err != nil {
			log.Printf("Failed to fetch blocked applications: %v. Using empty list.", err)
			*blocked = []string{} // Use empty list when server is unavailable
		} else {
			*blocked = apps
			if len(apps) > 0 {
				log.Printf("Updated blocked applications: %v", apps)
			} else {
				log.Println("No applications currently blocked")
			}
		}
		time.Sleep(30 * time.Second) // Update every 30 seconds
	}
}

func main() {
	// Load environment variables from .env file
	if err := loadEnvFile(); err != nil {
		log.Printf("Warning: Could not load .env file: %v. Using defaults.", err)
	}

	// Get server address from environment variable
	serverAddress := os.Getenv("SERVER_ADDRESS")
	if serverAddress == "" {
		serverAddress = "http://localhost:8080" // Default fallback
	}

	log.Printf("ProcSentinel Agent started on %s", runtime.GOOS)
	log.Printf("Server address: %s", serverAddress)
	log.Println("Press Ctrl+C to stop.")

	// Initialize blocked applications list
	var blocked []string

	// Start background goroutine to update blocked applications every 60 seconds
	go updateBlockedApplications(serverAddress, &blocked)

	// Initial fetch (don't wait 60 seconds for first fetch)
	apps, err := fetchBlockedApplications(serverAddress)
	if err != nil {
		log.Printf("Initial fetch failed: %v. Starting with empty list.", err)
		blocked = []string{}
	} else {
		blocked = apps
		if len(apps) > 0 {
			log.Printf("Initial blocked applications: %v", apps)
		} else {
			log.Println("No applications currently blocked")
		}
	}

	// Main process monitoring loop
	for {
		// Get list of running processes based on OS
		processes, err := getProcessList()
		if err != nil {
			log.Printf("Error getting process list: %v", err)
			time.Sleep(2 * time.Second)
			continue
		}

		processText := strings.ToLower(processes)
		// Create a local copy to avoid race conditions with the update goroutine
		localBlocked := make([]string, len(blocked))
		copy(localBlocked, blocked)

		for _, name := range localBlocked {
			if name != "" && strings.Contains(processText, strings.ToLower(name)) {
				// Kill the process
				if err := killProcess(name); err == nil {
					log.Printf("Killed process: %s", name)
				} else {
					log.Printf("Failed to kill process %s: %v", name, err)
				}
			}
		}

		time.Sleep(10 * time.Second) // check every second
	}
}

// getProcessList returns a list of running processes based on the operating system
func getProcessList() (string, error) {
	switch runtime.GOOS {
	case "windows":
		out, err := exec.Command("tasklist").Output()
		return string(out), err
	case "linux", "darwin":
		out, err := exec.Command("ps", "aux").Output()
		return string(out), err
	default:
		return "", fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}
}

// killProcess terminates a process by name based on the operating system
func killProcess(name string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("taskkill", "/F", "/IM", name).Run()
	case "linux", "darwin":
		return exec.Command("pkill", "-f", name).Run()
	default:
		return fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}
}
