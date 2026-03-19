package main

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
)

// readAgentEventLog returns the most recent Application-log entries written by
// the agent. This is the only place the agent records sync failures and kill
// decisions, so it is where an operator has to look when something is wrong.
//
// wevtutil is used rather than the Win32 event-log API because the agent writes
// plain event-source strings and a text dump is exactly what the pane shows.
func readAgentEventLog(count int) (string, error) {
	query := fmt.Sprintf(
		"*[System[Provider[@Name='%s']]]", serviceName)

	cmd := exec.Command("wevtutil", "qe", "Application",
		"/q:"+query,
		fmt.Sprintf("/c:%d", count),
		"/rd:true",
		"/f:text")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("could not read the event log: %v", err)
	}

	text := strings.TrimSpace(string(out))
	if text == "" {
		return "", nil
	}
	return text, nil
}

// summarizeEventLog condenses the verbose wevtutil text dump into one line per
// event: the timestamp and the message.
func summarizeEventLog(raw string) []string {
	var lines []string
	var date, message string

	flush := func() {
		if message == "" {
			return
		}
		if date != "" {
			lines = append(lines, date+"  "+message)
		} else {
			lines = append(lines, message)
		}
		date, message = "", ""
	}

	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "Event["):
			flush()
		case strings.HasPrefix(trimmed, "Date:"):
			date = strings.TrimSpace(strings.TrimPrefix(trimmed, "Date:"))
		case strings.HasPrefix(trimmed, "Description:"):
			message = ""
		case message == "" && trimmed != "" && !strings.Contains(trimmed, ":"):
			message = trimmed
		}
	}
	flush()

	return lines
}
