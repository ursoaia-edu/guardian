//go:build !windows

package main

import (
	"fmt"
)

// Stub functions for non-Windows systems
func installService() error {
	return fmt.Errorf("service installation not supported on this platform")
}

func removeService() error {
	return fmt.Errorf("service removal not supported on this platform")
}

func startService() error {
	return fmt.Errorf("service start not supported on this platform")
}

func stopService() error {
	return fmt.Errorf("service stop not supported on this platform")
}

func runService(name string, isDebug bool) {
	// Not supported on non-Windows
}

const svcName = "ProcSentinelAgent"
