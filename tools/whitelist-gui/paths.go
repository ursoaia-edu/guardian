package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const (
	serviceName       = "ProcSentinelAgent"
	defaultInstallDir = `C:\Windows\System32\ProcSentinel\agent`
	whitelistFileName = "whitelist.txt"
	syncFileName      = "sync.json"
	envFileName       = ".env"
)

// installDir resolves the agent installation directory. It prefers the
// ImagePath registered for the service, so a non-standard install is still
// found, and falls back to the path hardcoded in the installer scripts.
//
// The result is always the canonical path: System32, never the Sysnative
// alias. See ioPath for why that distinction matters.
func installDir() (dir string, fromRegistry bool) {
	if p, err := serviceImagePath(); err == nil && p != "" {
		return filepath.Dir(p), true
	}
	return defaultInstallDir, false
}

// ioPath converts a canonical path into one this process can actually open.
//
// Every path the program stores, compares, displays or hands to another process
// is canonical -- System32. Sysnative is a private alias that exists only for
// 32-bit processes: the 64-bit Explorer cannot resolve it, it is meaningless in
// a service registration, and it is not something a user can paste anywhere. So
// the alias is applied at the moment of a file operation and nowhere else.
func ioPath(path string) string {
	return fixRedirection(path)
}

func serviceImagePath() (string, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SYSTEM\CurrentControlSet\Services\`+serviceName, registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer k.Close()

	v, _, err := k.GetStringValue("ImagePath")
	if err != nil {
		return "", err
	}
	return unquoteImagePath(v), nil
}

// unquoteImagePath strips quotes and any trailing arguments from a service
// ImagePath, leaving just the executable path.
func unquoteImagePath(v string) string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, `"`) {
		if end := strings.Index(v[1:], `"`); end >= 0 {
			return v[1 : end+1]
		}
		return strings.Trim(v, `"`)
	}
	if i := strings.Index(strings.ToLower(v), ".exe"); i >= 0 {
		return v[:i+4]
	}
	return v
}

// fixRedirection rewrites System32 to Sysnative when this is a 32-bit process
// on 64-bit Windows. Without it the file system redirector would silently send
// us to SysWOW64 and we would edit the wrong (or a non-existent) whitelist.
func fixRedirection(path string) string {
	if runtime.GOARCH != "386" || os.Getenv("PROCESSOR_ARCHITEW6432") == "" {
		return path
	}

	winDir := os.Getenv("WINDIR")
	if winDir == "" {
		winDir = `C:\Windows`
	}

	sys32 := filepath.Join(winDir, "System32")
	if len(path) >= len(sys32) && strings.EqualFold(path[:len(sys32)], sys32) {
		return filepath.Join(winDir, "Sysnative") + path[len(sys32):]
	}
	return path
}
