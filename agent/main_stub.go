//go:build !windows

package main

// Non-Windows stub for service detection
func isWindowsService() (bool, error) {
	return false, nil
}