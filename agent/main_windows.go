//go:build windows

package main

import (
	"golang.org/x/sys/windows/svc"
)

// Windows-specific service detection
func isWindowsService() (bool, error) {
	return svc.IsWindowsService()
}