package main

import (
	"os/exec"
	"sort"
	"strings"
	"syscall"
)

// runningProcesses returns the unique lowercase names of running processes,
// collected exactly the way the agent collects them.
func runningProcesses() ([]string, error) {
	cmd := exec.Command("tasklist", "/FO", "CSV", "/NH")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		name := extractProcessName(line)
		if name == "" {
			continue
		}
		lower := strings.ToLower(name)
		if !seen[lower] {
			seen[lower] = true
			names = append(names, lower)
		}
	}

	sort.Strings(names)
	return names, nil
}

// extractProcessName mirrors the agent's parser for tasklist CSV output:
// "chrome.exe","1234","Console","1","100,000 K"
func extractProcessName(line string) string {
	line = strings.TrimSpace(line)
	if len(line) == 0 || line[0] != '"' {
		return ""
	}
	end := strings.Index(line[1:], `"`)
	if end < 0 {
		return ""
	}
	return line[1 : end+1]
}
