package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

type installState int

const (
	stateNotInstalled installState = iota
	stateBroken
	stateStopped
	stateRunningNoSync
	stateRunning
)

func (s installState) String() string {
	switch s {
	case stateNotInstalled:
		return "Not installed"
	case stateBroken:
		return "Installation is broken"
	case stateStopped:
		return "Installed, stopped"
	case stateRunningNoSync:
		return "Running, no contact with the server"
	case stateRunning:
		return "Running"
	}
	return "Unknown"
}

// health is the combined view of everything that determines whether the agent
// is actually doing its job. "Installed" is deliberately not a boolean: a
// registered service whose directory holds nothing but the executable is
// registered and non-functional at the same time.
type health struct {
	State installState

	Registered   bool
	ServiceState string
	Running      bool
	StartAuto    bool

	ExePath   string
	ExeExists bool

	EnvExists     bool
	HasServerAddr bool
	HasToken      bool
	CheckInterval int

	SyncExists bool
	SyncAge    time.Duration
	SyncFresh  bool
	Mode       string
	AppCount   int

	WhitelistExists bool
	WhitelistCount  int

	Warnings []string
}

// probeHealth inspects the service and the given installation directory. It
// never fails: every signal it cannot read is reported as missing, which is
// itself diagnostic information.
func probeHealth(dir string) health {
	h := health{ServiceState: "not installed", CheckInterval: 20}

	h.readService()
	h.readExecutable(dir)
	h.readEnv(dir)
	h.readSync(dir)
	h.readWhitelist(dir)
	h.classify()
	h.collectWarnings()

	return h
}

func (h *health) readService() {
	m, err := mgr.Connect()
	if err != nil {
		return
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		return
	}
	defer s.Close()

	h.Registered = true

	if st, err := s.Query(); err == nil {
		h.ServiceState = stateName(st.State)
		h.Running = st.State == svc.Running
	} else {
		h.ServiceState = "unknown"
	}

	if cfg, err := s.Config(); err == nil {
		h.StartAuto = cfg.StartType == mgr.StartAutomatic
		if h.ExePath == "" {
			h.ExePath = unquoteImagePath(cfg.BinaryPathName)
		}
	}
}

func (h *health) readExecutable(dir string) {
	if h.ExePath == "" {
		// No registration to learn the name from: look for either architecture
		// in the directory we were pointed at.
		for _, name := range agentExeNames() {
			candidate := filepath.Join(dir, name)
			if _, err := os.Stat(ioPath(candidate)); err == nil {
				h.ExePath = candidate
				break
			}
		}
	}
	if h.ExePath == "" {
		return
	}
	_, err := os.Stat(ioPath(h.ExePath))
	h.ExeExists = err == nil
}

func (h *health) readEnv(dir string) {
	cfg, err := loadEnvConfig(filepath.Join(dir, envFileName))
	if err != nil {
		return
	}
	h.EnvExists = true
	h.HasServerAddr = cfg.Get("SERVER_ADDRESS") != ""
	h.HasToken = cfg.Get("TOKEN") != ""
	h.CheckInterval = cfg.CheckInterval()
}

func (h *health) readSync(dir string) {
	path := filepath.Join(dir, syncFileName)

	info, err := os.Stat(ioPath(path))
	if err != nil {
		return
	}
	h.SyncExists = true
	h.SyncAge = time.Since(info.ModTime())

	// The agent rewrites sync.json on every successful poll. Three intervals of
	// silence is well past coincidence and means it is not reaching the server.
	h.SyncFresh = h.SyncAge <= time.Duration(3*h.CheckInterval)*time.Second

	if st, err := loadSyncState(path); err == nil {
		h.Mode = st.Mode
		h.AppCount = len(st.Applications)
	}
}

func (h *health) readWhitelist(dir string) {
	names, exists, err := loadWhitelist(filepath.Join(dir, whitelistFileName))
	if err != nil {
		return
	}
	h.WhitelistExists = exists
	h.WhitelistCount = len(names)
}

func (h *health) classify() {
	switch {
	case !h.Registered:
		h.State = stateNotInstalled
	case !h.ExeExists:
		h.State = stateBroken
	case !h.Running:
		h.State = stateStopped
	case !h.SyncFresh:
		h.State = stateRunningNoSync
	default:
		h.State = stateRunning
	}
}

// collectWarnings gathers problems that can accompany any state and therefore
// must not be folded into the state itself.
func (h *health) collectWarnings() {
	if !h.Registered {
		return
	}
	if !h.EnvExists {
		h.Warnings = append(h.Warnings,
			"no .env - the agent will fall back to http://localhost:8080")
	} else {
		if !h.HasServerAddr {
			h.Warnings = append(h.Warnings, "SERVER_ADDRESS is not set in .env")
		}
		if !h.HasToken {
			h.Warnings = append(h.Warnings, "TOKEN is not set in .env")
		}
	}
	if !h.StartAuto {
		h.Warnings = append(h.Warnings, "service start type is not Automatic")
	}
	if !h.WhitelistExists {
		h.Warnings = append(h.Warnings, "no whitelist.txt - the agent has not completed a first run")
	}
}

// SyncSummary describes the last successful server sync in words.
func (h *health) SyncSummary() string {
	if !h.SyncExists {
		return "never"
	}
	age := humanDuration(h.SyncAge.Round(time.Second))
	if h.SyncFresh {
		return age + " ago"
	}
	return age + " ago - stale"
}

func humanDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
