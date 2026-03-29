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
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// ClientApplication represents an application from the server
type ClientApplication struct {
	Name string `json:"name"`
	Mode string `json:"mode"`
}

// ClientEntry represents a client entry from the server
type ClientEntry struct {
	Name   string `json:"name"`
	Status bool   `json:"status"`
}

// SyncResponse represents the server response from /client/sync
type SyncResponse struct {
	Applications []ClientApplication `json:"applications"`
	Mode         string              `json:"mode"`
	Client       []ClientEntry       `json:"client"`
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

// fetchSync fetches the full sync state from the server
func fetchSync(serverAddress string) (*SyncResponse, error) {
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	var token = os.Getenv("TOKEN")
	if token == "" {
		token = "mILp9n6shk3G9SGSaS2nmP6YlLHwsP1Z"
	}

	url := serverAddress + "/client/sync"
	if identity := os.Getenv("IDENTITY"); identity != "" {
		if _, err := strconv.Atoi(identity); err == nil {
			url += "?identity=" + identity
		}
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
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

	var response SyncResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to decode response: %v", err)
	}

	return &response, nil
}

// updateSync fetches updated sync state from server
func updateSync(serverAddress string, state **SyncResponse) {
	for {
		resp, err := fetchSync(serverAddress)
		if err != nil {
			log.Printf("Failed to sync: %v. Loading from sync.json.", err)
			if cached, loadErr := loadSyncFromFile(); loadErr == nil {
				*state = cached
				log.Printf("Loaded %d applications from sync.json", len(cached.Applications))
			} else {
				log.Printf("Could not load sync.json: %v", loadErr)
			}
		} else {
			*state = resp
			if err := saveSyncToFile(resp); err != nil {
				log.Printf("Failed to save sync.json: %v", err)
			}
			log.Printf("Synced: %d applications, mode=%s", len(resp.Applications), resp.Mode)
		}
		time.Sleep(10 * time.Second)
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

	initWhitelist()

	log.Printf("ProcSentinel Agent started on %s", runtime.GOOS)
	log.Printf("Server address: %s", serverAddress)
	log.Println("Press Ctrl+C to stop.")

	// Initialize sync state
	var state *SyncResponse

	// Start background goroutine to sync every 10 seconds
	go updateSync(serverAddress, &state)

	// Initial fetch
	resp, err := fetchSync(serverAddress)
	if err != nil {
		log.Printf("Initial sync failed: %v. Loading from sync.json.", err)
		if cached, loadErr := loadSyncFromFile(); loadErr == nil {
			state = cached
			log.Printf("Loaded %d applications from sync.json", len(cached.Applications))
		} else {
			log.Printf("Could not load sync.json: %v. Starting with empty state.", loadErr)
			state = &SyncResponse{}
		}
	} else {
		state = resp
		if err := saveSyncToFile(resp); err != nil {
			log.Printf("Failed to save sync.json: %v", err)
		}
		log.Printf("Initial sync: %d applications, mode=%s", len(resp.Applications), resp.Mode)
	}

	// Main process monitoring loop
	for {
		if state == nil {
			time.Sleep(1 * time.Second)
			continue
		}

		if len(state.Applications) == 0 && state.Mode != "whitelist" {
			time.Sleep(1 * time.Second)
			continue
		}

		// Check power status from client entries
		if powerStatus, found := getClientEntry(state, "power"); found && !powerStatus {
			log.Println("Shutdown PC triggered: power disabled")
			if err := shutdownPCService(); err != nil {
				log.Printf("Failed to shutdown PC: %v", err)
			}
			time.Sleep(60 * time.Second)
			continue
		}

		// Get list of running processes
		processes, err := getProcessList()
		if err != nil {
			log.Printf("Error getting process list: %v", err)
			time.Sleep(2 * time.Second)
			continue
		}

		processText := strings.ToLower(processes)

		// Copy current state to avoid race
		localApps := make([]ClientApplication, len(state.Applications))
		copy(localApps, state.Applications)
		localMode := state.Mode

		if localMode == "blacklist" {
			// Kill processes that match the list
			for _, app := range localApps {
				if app.Name != "" && strings.Contains(processText, strings.ToLower(app.Name)) {
					if err := killProcess(app.Name); err == nil {
						log.Printf("Killed process: %s", app.Name)
					} else {
						log.Printf("Failed to kill process %s: %v", app.Name, err)
					}
				}
			}
		} else if localMode == "whitelist" {
			// Kill processes NOT in the list
			allowedSet := make(map[string]bool)
			for _, app := range localApps {
				allowedSet[strings.ToLower(app.Name)] = true
			}
			for _, line := range strings.Split(processes, "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				procName := extractProcessName(line)
				if procName == "" {
					continue
				}
				if !allowedSet[strings.ToLower(procName)] && !isSystemProcess(procName) {
					if err := killProcess(procName); err == nil {
						log.Printf("Killed non-whitelisted process: %s", procName)
					}
				}
			}
		}

		time.Sleep(1 * time.Second)
	}
}

// extractProcessName extracts the process name from a process list line
func extractProcessName(line string) string {
	switch runtime.GOOS {
	case "windows":
		// tasklist /FO CSV /NH format: "chrome.exe","1234","Console","1","100,000 K"
		line = strings.TrimSpace(line)
		if len(line) == 0 || line[0] != '"' {
			return ""
		}
		end := strings.Index(line[1:], "\"")
		if end < 0 {
			return ""
		}
		return line[1 : end+1]
	case "linux", "darwin":
		// ps aux format: "user  PID %CPU %MEM VSZ RSS TTY STAT START TIME COMMAND"
		fields := strings.Fields(line)
		if len(fields) >= 11 {
			return filepath.Base(fields[10])
		}
	}
	return ""
}

// userWhitelist holds process names loaded from whitelist.txt
var userWhitelist map[string]bool

const whitelistFile = "whitelist.txt"

// generateWhitelist creates whitelist.txt from currently running processes
func generateWhitelist() error {
	processes, err := getProcessList()
	if err != nil {
		return fmt.Errorf("failed to get process list: %v", err)
	}

	seen := make(map[string]bool)
	var names []string
	for _, line := range strings.Split(processes, "\n") {
		name := extractProcessName(strings.TrimSpace(line))
		if name == "" {
			continue
		}
		lower := strings.ToLower(name)
		if !seen[lower] {
			seen[lower] = true
			names = append(names, lower)
		}
	}

	content := strings.Join(names, "\n") + "\n"
	return os.WriteFile(whitelistFile, []byte(content), 0644)
}

// loadWhitelist loads process names from whitelist.txt into userWhitelist
func loadWhitelist() {
	userWhitelist = make(map[string]bool)

	data, err := os.ReadFile(whitelistFile)
	if err != nil {
		return
	}

	for _, line := range strings.Split(string(data), "\n") {
		name := strings.TrimSpace(line)
		if name != "" && !strings.HasPrefix(name, "#") {
			userWhitelist[strings.ToLower(name)] = true
		}
	}
}

// initWhitelist generates whitelist.txt on first run, then loads it
func initWhitelist() {
	if _, err := os.Stat(whitelistFile); os.IsNotExist(err) {
		log.Println("First run: generating whitelist.txt from running processes...")
		if err := generateWhitelist(); err != nil {
			log.Printf("Failed to generate whitelist.txt: %v", err)
		} else {
			log.Println("whitelist.txt created. Review and edit it if needed.")
		}
	}
	loadWhitelist()
	log.Printf("Loaded %d processes from whitelist.txt", len(userWhitelist))
}

// isSystemProcess returns true for processes that should never be killed
func isSystemProcess(name string) bool {
	lower := strings.ToLower(name)

	// Check user whitelist first
	if userWhitelist[lower] {
		return true
	}

	systemProcs := []string{
		// Windows: kernel & session
		"system", "system idle process", "secure system", "registry",
		"smss.exe", "csrss.exe", "wininit.exe", "winlogon.exe",
		"services.exe", "lsass.exe", "lsaiso.exe",

		// Windows: core services
		"svchost.exe", "dwm.exe", "fontdrvhost.exe", "conhost.exe",
		"wudfhost.exe", "wmiprvse.exe", "dllhost.exe", "spoolsv.exe",
		"audiodg.exe", "dashost.exe", "searchindexer.exe",
		"searchprotocolhost.exe", "searchfilterhost.exe",

		// Windows: shell & UWP
		"explorer.exe", "sihost.exe", "taskhostw.exe",
		"runtimebroker.exe", "applicationframehost.exe",
		"shellexperiencehost.exe", "startmenuexperiencehost.exe",
		"searchhost.exe", "searchui.exe", "searchapp.exe",
		"textinputhost.exe", "lockapp.exe", "widgets.exe",
		"widgetservice.exe", "comppkgsrv.exe",

		// Windows: Settings & system apps
		// "systemsettings.exe", "systemsettingsbroker.exe",
		"userinit.exe", "logonui.exe",

		// Windows: security
		"securityhealthservice.exe", "securityhealthsystray.exe",
		"securityhealthhost.exe", "smartscreen.exe",
		"msmpeng.exe", "nissrv.exe", "mpcmdrun.exe",

		// Windows: input & accessibility
		"ctfmon.exe", "tabletinputservice.exe",

		// Windows: networking
		"lsm.exe", "networkservice.exe", "localservice.exe",

		// Windows: tools & shells
		"tasklist.exe", "taskmgr.exe", "cmd.exe", "powershell.exe", "pwsh.exe",
		"windowsterminal.exe", "wt.exe", "openssh.exe", "sshd.exe",

		// ProcSentinel
		"procsentinel-agent64.exe", "procsentinel-agent32.exe",
	}
	for _, sys := range systemProcs {
		if lower == sys {
			return true
		}
	}
	return false
}

// getProcessList returns a list of running processes based on the operating system
func getProcessList() (string, error) {
	switch runtime.GOOS {
	case "windows":
		out, err := exec.Command("tasklist", "/FO", "CSV", "/NH").Output()
		return string(out), err
	case "linux", "darwin":
		out, err := exec.Command("ps", "aux").Output()
		return string(out), err
	default:
		return "", fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}
}

// saveSyncToFile saves the sync response to sync.json (excludes client entries)
func saveSyncToFile(resp *SyncResponse) error {
	toSave := struct {
		Applications []ClientApplication `json:"applications"`
		Mode         string              `json:"mode"`
	}{
		Applications: resp.Applications,
		Mode:         resp.Mode,
	}
	data, err := json.Marshal(toSave)
	if err != nil {
		return err
	}
	return os.WriteFile("sync.json", data, 0644)
}

// loadSyncFromFile loads the sync response from sync.json
func loadSyncFromFile() (*SyncResponse, error) {
	data, err := os.ReadFile("sync.json")
	if err != nil {
		return nil, err
	}
	var resp SyncResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// getClientEntry returns the status of a client entry by name
func getClientEntry(state *SyncResponse, name string) (bool, bool) {
	if state == nil {
		return false, false
	}
	for _, entry := range state.Client {
		if entry.Name == name {
			return entry.Status, true
		}
	}
	return false, false
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
