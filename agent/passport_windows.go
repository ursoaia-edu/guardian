//go:build windows

package main

import (
	"golang.org/x/sys/windows/registry"
)

// machineGUID is the identifier Windows assigns at installation and keeps
// across renames, reboots and agent reinstalls — the server keys "same
// machine, reinstalled" on it. WOW64_64KEY matters: a 32-bit agent on 64-bit
// Windows otherwise reads the WOW64 view of the registry, where this value is
// absent, and every 32-bit install would fall back to a generated identity.
func machineGUID() (string, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Cryptography`, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return "", err
	}
	defer k.Close()
	v, _, err := k.GetStringValue("MachineGuid")
	return v, err
}

// osInfo reports the Windows edition and build the way the cabinet shows
// them. Every value is optional; the enrollment does not depend on any of it.
func osInfo() (name, build string) {
	name = "Windows"
	k, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return name, ""
	}
	defer k.Close()
	if v, _, err := k.GetStringValue("ProductName"); err == nil && v != "" {
		name = v
	}
	if v, _, err := k.GetStringValue("DisplayVersion"); err == nil && v != "" {
		name += " " + v
	}
	build, _, _ = k.GetStringValue("CurrentBuild")
	return name, build
}
