package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// loadWhitelist reads whitelist.txt using the same rules as the agent: one
// process name per line, blank lines and # comments ignored, everything
// lowercased. A missing file is not an error -- the agent only creates it on
// its first run, so before that there is simply nothing to show.
func loadWhitelist(path string) (names []string, exists bool, err error) {
	data, err := os.ReadFile(ioPath(path))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}

	seen := make(map[string]bool)
	for _, line := range strings.Split(string(data), "\n") {
		name := strings.TrimSpace(line)
		if name == "" || strings.HasPrefix(name, "#") {
			continue
		}
		lower := strings.ToLower(name)
		if !seen[lower] {
			seen[lower] = true
			names = append(names, lower)
		}
	}

	sort.Strings(names)
	return names, true, nil
}

// saveWhitelist writes the list back in the format the agent expects. The
// previous file is kept as whitelist.txt.bak so a bad edit can be undone.
func saveWhitelist(path string, names []string) error {
	path = ioPath(path)

	if data, err := os.ReadFile(path); err == nil {
		if err := os.WriteFile(path+".bak", data, 0644); err != nil {
			return fmt.Errorf("could not write the backup: %v", err)
		}
	}

	sorted := append([]string(nil), names...)
	sort.Strings(sorted)

	var b strings.Builder
	for _, n := range sorted {
		b.WriteString(strings.ToLower(n))
		b.WriteString("\r\n")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(b.String()), 0644)
}

// normalizeName cleans up a manually typed process name.
func normalizeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.Trim(s, `"'`)
	// A pasted full path is accepted -- keep only the executable name, which is
	// what tasklist reports and what the agent compares against.
	if i := strings.LastIndexAny(s, `\/`); i >= 0 {
		s = s[i+1:]
	}
	return s
}
