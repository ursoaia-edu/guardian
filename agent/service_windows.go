//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
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
	stopCh := make(chan struct{})
	go runAgentService(stopCh)

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

// eventLogger routes the shared run loop's output to the Windows event log.
type eventLogger struct{}

func (eventLogger) Infof(format string, args ...any)  { elog.Info(1, fmt.Sprintf(format, args...)) }
func (eventLogger) Warnf(format string, args ...any)  { elog.Warning(1, fmt.Sprintf(format, args...)) }
func (eventLogger) Errorf(format string, args ...any) { elog.Error(1, fmt.Sprintf(format, args...)) }

// runAgentService runs the shared agent loop from the service's own
// directory, which is where .env, sync.json, whitelist.txt and the
// credentials file live. Services start with System32 as their working
// directory, so without the chdir none of those would be found.
func runAgentService(stopCh <-chan struct{}) {
	exePath, err := os.Executable()
	if err != nil {
		elog.Error(1, fmt.Sprintf("Failed to get executable path: %v", err))
		return
	}
	if err := os.Chdir(filepath.Dir(exePath)); err != nil {
		elog.Error(1, fmt.Sprintf("Failed to change directory: %v", err))
		return
	}
	elog.Info(1, fmt.Sprintf("ProcSentinel Agent %s service started", agentVersion))
	runAgent(stopCh, eventLogger{}, runModeService)
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
	if elog != nil {
		elog.Info(1, "start: attempting privilege enable + shutdown")
	}

	// Call the main shutdownPC function which does the actual work
	if err := shutdownPC(); err != nil {
		if elog != nil {
			elog.Error(1, fmt.Sprintf("Shutdown failed: %v", err))
		}
		return err
	}

	if elog != nil {
		elog.Info(1, "Shutdown initiated successfully")
	}
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
