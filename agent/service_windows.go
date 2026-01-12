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

	elog.Info(1, fmt.Sprintf("ProcSentinel Agent service started"))
	elog.Info(1, fmt.Sprintf("Server address: %s", serverAddress))

	// Initialize blocked applications list
	var blocked []string

	// Start background goroutine to update blocked applications every 10 seconds
	go updateBlockedApplicationsService(serverAddress, &blocked, stopCh)

	// Initial fetch (don't wait 10 seconds for first fetch)
	apps, err := fetchBlockedApplications(serverAddress)
	if err != nil {
		elog.Warning(1, fmt.Sprintf("Initial fetch failed: %v. Loading from apps.txt.", err))
		if cached, loadErr := loadAppsFromFile(); loadErr == nil {
			blocked = cached
			elog.Info(1, fmt.Sprintf("Loaded %d applications from apps.txt", len(cached)))
		} else {
			elog.Warning(1, fmt.Sprintf("Could not load apps.txt: %v. Starting with empty list.", loadErr))
			blocked = []string{}
		}
	} else {
		blocked = apps
		if err := saveAppsToFile(apps); err != nil {
			elog.Warning(1, fmt.Sprintf("Failed to save apps.txt: %v", err))
		}
		if len(apps) > 0 {
			elog.Info(1, fmt.Sprintf("Initial blocked applications: %v", apps))
		} else {
			elog.Info(1, "No applications currently blocked")
		}
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
			// Get list of running processes
			processes, err := getProcessList()
			if err != nil {
				elog.Error(1, fmt.Sprintf("Error getting process list: %v", err))
				continue
			}

			processText := strings.ToLower(processes)
			// Create a local copy to avoid race conditions with the update goroutine
			localBlocked := make([]string, len(blocked))
			copy(localBlocked, blocked)

			for _, name := range localBlocked {
				if name == "force_poweroff" || name == "force_shutdown" {
					elog.Info(1, fmt.Sprintf("Shutdown PC triggered: %s", name))

					// Perform shutdown
					if err := shutdownPCService(); err != nil {
						elog.Error(1, fmt.Sprintf("Failed to shutdown PC: %v", err))
					}

				} else if name != "" && strings.Contains(processText, strings.ToLower(name)) {
					// Kill the process
					if err := killProcess(name); err == nil {
						elog.Info(1, fmt.Sprintf("Killed process: %s", name))
					} else {
						elog.Error(1, fmt.Sprintf("Failed to kill process %s: %v", name, err))
					}
				}
			}
		}
	}
}

// updateBlockedApplicationsService fetches updated list from server for service
func updateBlockedApplicationsService(serverAddress string, blocked *[]string, stopCh <-chan bool) {
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
			apps, err := fetchBlockedApplications(serverAddress)
			if err != nil {
				elog.Warning(1, fmt.Sprintf("Failed to fetch blocked applications: %v. Loading from apps.txt.", err))
				if cached, loadErr := loadAppsFromFile(); loadErr == nil {
					*blocked = cached
					elog.Info(1, fmt.Sprintf("Loaded %d applications from apps.txt", len(cached)))
				} else {
					elog.Warning(1, fmt.Sprintf("Could not load apps.txt: %v", loadErr))
				}
			} else {
				*blocked = apps
				if err := saveAppsToFile(apps); err != nil {
					elog.Warning(1, fmt.Sprintf("Failed to save apps.txt: %v", err))
				}
				if len(apps) > 0 {
					elog.Info(1, fmt.Sprintf("Updated blocked applications: %v", apps))
				} else {
					elog.Info(1, "No applications currently blocked")
				}
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
