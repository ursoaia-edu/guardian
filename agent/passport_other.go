//go:build !windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// machineGUID on Linux is the systemd/dbus machine id; on macOS the platform
// UUID. Anything else falls back to a generated identity for the process.
func machineGUID() (string, error) {
	switch runtime.GOOS {
	case "linux":
		for _, p := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
			if data, err := os.ReadFile(p); err == nil {
				if id := strings.TrimSpace(string(data)); id != "" {
					return id, nil
				}
			}
		}
		return "", errors.New("no machine-id file")
	case "darwin":
		out, err := exec.Command("ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
		if err != nil {
			return "", err
		}
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, "IOPlatformUUID") {
				if _, v, ok := strings.Cut(line, "= "); ok {
					return strings.Trim(strings.TrimSpace(v), `"`), nil
				}
			}
		}
		return "", errors.New("IOPlatformUUID not found")
	}
	return "", errors.New("unsupported platform")
}

func osInfo() (name, build string) {
	name = runtime.GOOS
	if runtime.GOOS == "linux" {
		if data, err := os.ReadFile("/etc/os-release"); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if v, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
					name = strings.Trim(v, `"`)
				}
			}
		}
		if data, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
			build = strings.TrimSpace(string(data))
		}
	}
	if out, err := exec.Command("uname", "-r").Output(); err == nil && build == "" {
		build = strings.TrimSpace(string(out))
	}
	return name, build
}
