package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

func withService(fn func(*mgr.Service) error) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("cannot access the service control manager: %v", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("service %s is not registered: %v", serviceName, err)
	}
	defer s.Close()

	return fn(s)
}

// serviceState returns a human readable state for the agent service.
func serviceState() string {
	var state string
	err := withService(func(s *mgr.Service) error {
		st, err := s.Query()
		if err != nil {
			return err
		}
		state = stateName(st.State)
		return nil
	})
	if err != nil {
		return "not installed"
	}
	return state
}

func stateName(s svc.State) string {
	switch s {
	case svc.Stopped:
		return "stopped"
	case svc.StartPending:
		return "starting"
	case svc.StopPending:
		return "stopping"
	case svc.Running:
		return "running"
	case svc.ContinuePending:
		return "resuming"
	case svc.PausePending:
		return "pausing"
	case svc.Paused:
		return "paused"
	}
	return "unknown"
}

// restartService stops and starts the agent. The agent calls initWhitelist()
// once at startup and never re-reads the file, so edits to whitelist.txt do not
// take effect until the service is restarted.
func restartService() error {
	if err := stopService(); err != nil {
		return err
	}
	return startService()
}

func stopService() error {
	return withService(func(s *mgr.Service) error {
		st, err := s.Query()
		if err != nil {
			return err
		}
		if st.State == svc.Stopped {
			return nil
		}
		if st.State != svc.StopPending {
			if _, err := s.Control(svc.Stop); err != nil {
				return fmt.Errorf("could not stop the service: %v", err)
			}
		}
		return waitFor(s, svc.Stopped, 30*time.Second)
	})
}

func startService() error {
	return withService(func(s *mgr.Service) error {
		st, err := s.Query()
		if err != nil {
			return err
		}
		if st.State == svc.Running {
			return nil
		}
		if err := s.Start(); err != nil {
			return fmt.Errorf("could not start the service: %v", err)
		}
		return waitFor(s, svc.Running, 30*time.Second)
	})
}

func waitFor(s *mgr.Service, want svc.State, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		st, err := s.Query()
		if err != nil {
			return err
		}
		if st.State == want {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for the service to become %s", stateName(want))
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// serviceIsRegistered reports whether the SCM knows about the agent at all.
func serviceIsRegistered() bool {
	return withService(func(s *mgr.Service) error { return nil }) == nil
}

// deleteServiceViaSCM is the fallback for a broken install: the registration
// exists but the executable that would normally run -remove is gone.
func deleteServiceViaSCM() error {
	return withService(func(s *mgr.Service) error {
		if err := s.Delete(); err != nil {
			return fmt.Errorf("could not deregister the service: %v", err)
		}
		return nil
	})
}

// sysnativeToSystem32 rewrites the WOW64 alias back to the real directory.
func sysnativeToSystem32(path string) string {
	lower := strings.ToLower(path)
	idx := strings.Index(lower, `\sysnative\`)
	if idx < 0 {
		return path
	}
	return path[:idx] + `\System32\` + path[idx+len(`\sysnative\`):]
}

// normalizeServiceImagePath repairs the registration written when a 32-bit
// installer launches the agent through the Sysnative alias.
//
// A 32-bit process cannot execute anything under System32 directly -- the file
// system redirector sends it to SysWOW64 -- so the installer has to invoke the
// agent through Sysnative. The agent then registers itself using
// os.Executable(), which may hand back that alias. Sysnative does not exist for
// the 64-bit service host, so such a registration would never start.
//
// Rewriting it is cheap and harmless when the path was already correct.
func normalizeServiceImagePath() error {
	return withService(func(s *mgr.Service) error {
		cfg, err := s.Config()
		if err != nil {
			return fmt.Errorf("could not read the service configuration: %v", err)
		}

		fixed := sysnativeToSystem32(cfg.BinaryPathName)
		if fixed == cfg.BinaryPathName {
			return nil
		}

		cfg.BinaryPathName = fixed
		if err := s.UpdateConfig(cfg); err != nil {
			return fmt.Errorf("could not correct the service image path: %v", err)
		}
		return nil
	})
}

// registeredImagePath returns the executable path the SCM has on record.
func registeredImagePath() string {
	var path string
	_ = withService(func(s *mgr.Service) error {
		if cfg, err := s.Config(); err == nil {
			path = filepath.Clean(unquoteImagePath(cfg.BinaryPathName))
		}
		return nil
	})
	return path
}

// isElevated reports whether we can actually manage the service. Opening the
// service control manager for write access requires elevation, which makes this
// a reliable proxy for "running as administrator".
func isElevated() bool {
	m, err := mgr.Connect()
	if err != nil {
		return false
	}
	m.Disconnect()
	return true
}
