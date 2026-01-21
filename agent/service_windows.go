//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/debug"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"
)

var elog debug.Log

const svcName = "ProcSentinelAgent"
const svcDisplayName = "ProcSentinel Agent"
const svcDescription = "ProcSentinel process monitoring and control agent"

type myservice struct{}

func (m *myservice) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (ssec bool, errno uint32) {
	const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown | svc.AcceptPauseAndContinue
	changes <- svc.Status{State: svc.StartPending}

	// Start the main agent logic in a goroutine
	stopCh := make(chan bool)
	go runAgent(stopCh)

	changes <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}

	elog.Info(1, fmt.Sprintf("%s service started", svcName))

loop:
	for {
		select {
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				changes <- c.CurrentStatus
				// Testing deadlock from https://code.google.com/p/winsvc/issues/detail?id=4
				time.Sleep(100 * time.Millisecond)
			case svc.Stop, svc.Shutdown:
				elog.Info(1, fmt.Sprintf("%s service stopping", svcName))
				// Signal the agent to stop
				close(stopCh)
				break loop
			case svc.Pause:
				changes <- svc.Status{State: svc.Paused, Accepts: cmdsAccepted}
				elog.Info(1, fmt.Sprintf("%s service paused", svcName))
			case svc.Continue:
				changes <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}
				elog.Info(1, fmt.Sprintf("%s service continued", svcName))
			default:
				elog.Error(1, fmt.Sprintf("unexpected control request #%d", c))
			}
		}
	}
	changes <- svc.Status{State: svc.StopPending}
	return
}

func runService(name string, isDebug bool) {
	var err error
	if isDebug {
		elog = debug.New(name)
	} else {
		elog, err = eventlog.Open(name)
		if err != nil {
			return
		}
	}
	defer elog.Close()

	elog.Info(1, fmt.Sprintf("starting %s service", name))
	run := svc.Run
	if isDebug {
		run = debug.Run
	}
	err = run(name, &myservice{})
	if err != nil {
		elog.Error(1, fmt.Sprintf("%s service failed: %v", name, err))
		return
	}
	elog.Info(1, fmt.Sprintf("%s service stopped", name))
}

// runAgent runs the main agent logic
func runAgent(stopCh <-chan bool) {
	// Change to service directory to find .env file
	exePath, err := os.Executable()
	if err != nil {
		elog.Error(1, fmt.Sprintf("Failed to get executable path: %v", err))
		return
	}
	serviceDir := filepath.Dir(exePath)
	if err := os.Chdir(serviceDir); err != nil {
		elog.Error(1, fmt.Sprintf("Failed to change directory: %v", err))
		return
	}

	// Load environment variables from .env file
	if err := loadEnvFile(); err != nil {
		elog.Warning(1, fmt.Sprintf("Could not load .env file: %v. Using defaults.", err))
	}

	// Get server address from environment variable
	serverAddress := os.Getenv("SERVER_ADDRESS")
	if serverAddress == "" {
		serverAddress = "http://localhost:8080" // Default fallback
	}

	initWhitelist()

	elog.Info(1, fmt.Sprintf("ProcSentinel Agent service started"))
	elog.Info(1, fmt.Sprintf("Server address: %s", serverAddress))

	// Initialize sync state
	var state *SyncResponse

	// Start background goroutine to sync
	go updateSyncService(serverAddress, &state, stopCh)

	// Initial fetch
	resp, err := fetchSync(serverAddress)
	if err != nil {
		elog.Warning(1, fmt.Sprintf("Initial sync failed: %v. Loading from sync.json.", err))
		if cached, loadErr := loadSyncFromFile(); loadErr == nil {
			state = cached
			elog.Info(1, fmt.Sprintf("Loaded %d applications from sync.json", len(cached.Applications)))
		} else {
			elog.Warning(1, fmt.Sprintf("Could not load sync.json: %v. Starting with empty state.", loadErr))
			state = &SyncResponse{}
		}
	} else {
		state = resp
		if err := saveSyncToFile(resp); err != nil {
			elog.Warning(1, fmt.Sprintf("Failed to save sync.json: %v", err))
		}
		elog.Info(1, fmt.Sprintf("Initial sync: %d applications, mode=%s", len(resp.Applications), resp.Mode))
	}

	// Main process monitoring loop
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-stopCh:
			elog.Info(1, "Agent stopping")
			return
		case <-ticker.C:
			if state == nil {
				continue
			}

			if len(state.Applications) == 0 && state.Mode != "whitelist" {
				continue
			}

			// Check power status from client entries
			if powerStatus, found := getClientEntry(state, "power"); found && !powerStatus {
				elog.Info(1, "Shutdown PC triggered: power disabled")
				if err := shutdownPCService(); err != nil {
					elog.Error(1, fmt.Sprintf("Failed to shutdown PC: %v", err))
				}
				continue
			}

			// Get list of running processes
			processes, err := getProcessList()
			if err != nil {
				elog.Error(1, fmt.Sprintf("Error getting process list: %v", err))
				continue
			}

			processText := strings.ToLower(processes)

			// Copy current state to avoid race
			localApps := make([]ClientApplication, len(state.Applications))
			copy(localApps, state.Applications)
			localMode := state.Mode

			if localMode == "blacklist" {
				for _, app := range localApps {
					if app.Name != "" && strings.Contains(processText, strings.ToLower(app.Name)) {
						if err := killProcess(app.Name); err == nil {
							elog.Info(1, fmt.Sprintf("Killed process: %s", app.Name))
						} else {
							elog.Error(1, fmt.Sprintf("Failed to kill process %s: %v", app.Name, err))
						}
					}
				}
			} else if localMode == "whitelist" {
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
							elog.Info(1, fmt.Sprintf("Killed non-whitelisted process: %s", procName))
						}
					}
				}
			}
		}
	}
}

// updateSyncService fetches updated sync state from server for service mode
func updateSyncService(serverAddress string, state **SyncResponse, stopCh <-chan bool) {
	sleepSeconds := 20
	if envSleep := os.Getenv("CHECK_INTERVAL"); envSleep != "" {
		if val, err := strconv.Atoi(envSleep); err == nil {
			sleepSeconds = val
		}
	}
	ticker := time.NewTicker(time.Duration(sleepSeconds) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			resp, err := fetchSync(serverAddress)
			if err != nil {
				elog.Warning(1, fmt.Sprintf("Failed to sync: %v. Loading from sync.json.", err))
				if cached, loadErr := loadSyncFromFile(); loadErr == nil {
					*state = cached
					elog.Info(1, fmt.Sprintf("Loaded %d applications from sync.json", len(cached.Applications)))
				} else {
					elog.Warning(1, fmt.Sprintf("Could not load sync.json: %v", loadErr))
				}
			} else {
				*state = resp
				if err := saveSyncToFile(resp); err != nil {
					elog.Warning(1, fmt.Sprintf("Failed to save sync.json: %v", err))
				}
				elog.Info(1, fmt.Sprintf("Synced: %d applications, mode=%s", len(resp.Applications), resp.Mode))
			}
		}
	}
}

// Service management functions

func installService() error {
	exepath, err := os.Executable()
	if err != nil {
		return err
	}

	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	s, err := m.OpenService(svcName)
	if err == nil {
		s.Close()
		return fmt.Errorf("service %s already exists", svcName)
	}

	s, err = m.CreateService(svcName, exepath, mgr.Config{
		DisplayName:      svcDisplayName,
		Description:      svcDescription,
		StartType:        mgr.StartAutomatic,
		ServiceStartName: "",
	})
	if err != nil {
		return err
	}
	defer s.Close()

	err = eventlog.InstallAsEventCreate(svcName, eventlog.Error|eventlog.Warning|eventlog.Info)
	if err != nil {
		s.Delete()
		return fmt.Errorf("SetupEventLogSource() failed: %s", err)
	}
	return nil
}

// shutdownPCService initiates shutdown with service event logging
func shutdownPCService() error {
	elog.Info(1, "start: attempting privilege enable + shutdown")

	// Call the main shutdownPC function which does the actual work
	if err := shutdownPC(); err != nil {
		elog.Error(1, fmt.Sprintf("Shutdown failed: %v", err))
		return err
	}

	elog.Info(1, "Shutdown initiated successfully")
	return nil
}

func removeService() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	s, err := m.OpenService(svcName)
	if err != nil {
		return fmt.Errorf("service %s is not installed", svcName)
	}
	defer s.Close()

	err = s.Delete()
	if err != nil {
		return err
	}

	err = eventlog.Remove(svcName)
	if err != nil {
		return fmt.Errorf("RemoveEventLogSource() failed: %s", err)
	}
	return nil
}

func startService() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	s, err := m.OpenService(svcName)
	if err != nil {
		return fmt.Errorf("could not access service: %v", err)
	}
	defer s.Close()

	err = s.Start("is", "manual-started")
	if err != nil {
		return fmt.Errorf("could not start service: %v", err)
	}
	return nil
}

func stopService() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	s, err := m.OpenService(svcName)
	if err != nil {
		return fmt.Errorf("could not access service: %v", err)
	}
	defer s.Close()

	status, err := s.Control(svc.Stop)
	if err != nil {
		return fmt.Errorf("could not send control=%d: %v", svc.Stop, err)
	}

	timeout := time.Now().Add(10 * time.Second)
	for status.State != svc.Stopped {
		if timeout.Before(time.Now()) {
			return fmt.Errorf("timeout waiting for service to go to state=%d", svc.Stopped)
		}
		time.Sleep(300 * time.Millisecond)
		status, err = s.Query()
		if err != nil {
			return fmt.Errorf("could not retrieve service status: %v", err)
		}
	}
	return nil
}
