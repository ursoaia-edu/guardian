package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
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

	url := serverAddress + "/client/applications"
	if identity := os.Getenv("IDENTITY"); identity != "" {
		if _, err := strconv.Atoi(identity); err == nil {
			url += "?identity=" + identity
		}
	}

	req, err := http.NewRequest("GET", url, nil)
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
			log.Printf("Failed to fetch blocked applications: %v. Loading from apps.txt.", err)
			if cached, loadErr := loadAppsFromFile(); loadErr == nil {
				*blocked = cached
				log.Printf("Loaded %d applications from apps.txt", len(cached))
			} else {
				log.Printf("Could not load apps.txt: %v", loadErr)
			}
		} else {
			*blocked = apps
			if err := saveAppsToFile(apps); err != nil {
				log.Printf("Failed to save apps.txt: %v", err)
			}
			if len(apps) > 0 {
				log.Printf("Updated blocked applications: %v", apps)
			} else {
				log.Println("No applications currently blocked")
			}
		}
		time.Sleep(10 * time.Second) // Update every 10 seconds
	}
}

func main() {
	var (
		install   = flag.Bool("install", false, "Install Windows service")
		remove    = flag.Bool("remove", false, "Remove Windows service")
		start     = flag.Bool("start", false, "Start Windows service")
		stop      = flag.Bool("stop", false, "Stop Windows service")
		debugMode = flag.Bool("debug", false, "Run service in debug mode")
	)
	flag.Parse()

	if runtime.GOOS == "windows" {
		// Handle Windows service installation/management
		if *install {
			err := installService()
			if err != nil {
				log.Fatalf("Failed to install service: %v", err)
			}
			fmt.Println("Service installed successfully")
			return
		}

		if *remove {
			err := removeService()
			if err != nil {
				log.Fatalf("Failed to remove service: %v", err)
			}
			fmt.Println("Service removed successfully")
			return
		}

		if *start {
			err := startService()
			if err != nil {
				log.Fatalf("Failed to start service: %v", err)
			}
			fmt.Println("Service started successfully")
			return
		}

		if *stop {
			err := stopService()
			if err != nil {
				log.Fatalf("Failed to stop service: %v", err)
			}
			fmt.Println("Service stopped successfully")
			return
		}

		// Check if running as a Windows service
		isService, err := isWindowsService()
		if err != nil {
			log.Fatalf("Failed to determine if running as service: %v", err)
		}

		if isService {
			runService(svcName, false)
			return
		}

		if *debugMode {
			runService(svcName, true)
			return
		}
	}

	// Run in console mode (default for non-Windows or when not running as service)
	runConsole()
}

func runConsole() {
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
		log.Printf("Initial fetch failed: %v. Loading from apps.txt.", err)
		if cached, loadErr := loadAppsFromFile(); loadErr == nil {
			blocked = cached
			log.Printf("Loaded %d applications from apps.txt", len(cached))
		} else {
			log.Printf("Could not load apps.txt: %v. Starting with empty list.", loadErr)
			blocked = []string{}
		}
	} else {
		blocked = apps
		if err := saveAppsToFile(apps); err != nil {
			log.Printf("Failed to save apps.txt: %v", err)
		}
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
		time_to_sleep := 1
		for _, name := range localBlocked {
			if name == "force_poweroff" || name == "force_shutdown" {
				time_to_sleep = 60
				log.Printf("Shutdown PC triggered: %s", name)
				// Perform shutdown
				if err := shutdownPCService(); err != nil {
					log.Printf("Failed to shutdown PC: %v", err)
				} else {
					log.Println("Shutdown initiated successfully")
				}

			} else if name != "" && strings.Contains(processText, strings.ToLower(name)) {
				if err := killProcess(name); err == nil {
					log.Printf("Killed process: %s", name)
				} else {
					log.Printf("Failed to kill process %s: %v", name, err)
				}
			}
		}

		time.Sleep(time.Duration(time_to_sleep) * time.Second) // check every second
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

// saveAppsToFile saves the blocked applications list to apps.txt
func saveAppsToFile(apps []string) error {
	return os.WriteFile("apps.txt", []byte(strings.Join(apps, "\n")), 0644)
}

// loadAppsFromFile loads the blocked applications list from apps.txt
func loadAppsFromFile() ([]string, error) {
	data, err := os.ReadFile("apps.txt")
	if err != nil {
		return nil, err
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return []string{}, nil
	}
	return strings.Split(content, "\n"), nil
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
