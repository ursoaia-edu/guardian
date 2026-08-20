package main

import (
	"os"
	"strconv"
	"strings"
)

// knownEnvOrder is the order the installer writes keys in. Unknown keys found
// in an existing file are appended after these, so a hand-added setting is
// never silently dropped.
var knownEnvOrder = []string{"SERVER_ADDRESS", "TOKEN", "CHECK_INTERVAL", "IDENTITY"}

// envConfig holds the agent's .env contents. The agent parses it with a plain
// SplitN on "=", so anything more elaborate than KEY=VALUE would not be
// understood downstream and is not supported here either.
type envConfig struct {
	vals  map[string]string
	extra []string // keys outside knownEnvOrder, in the order first seen
}

func newEnvConfig() *envConfig {
	return &envConfig{vals: map[string]string{}}
}

func loadEnvConfig(path string) (*envConfig, error) {
	data, err := os.ReadFile(ioPath(path))
	if err != nil {
		return nil, err
	}

	cfg := newEnvConfig()
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		cfg.Set(strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]))
	}
	return cfg, nil
}

func (e *envConfig) Get(key string) string { return e.vals[key] }

func (e *envConfig) Set(key, value string) {
	if key == "" {
		return
	}
	if _, seen := e.vals[key]; !seen && !isKnownEnvKey(key) {
		e.extra = append(e.extra, key)
	}
	e.vals[key] = value
}

// CheckInterval returns the agent's poll interval, falling back to the same
// default the agent itself uses in service mode.
func (e *envConfig) CheckInterval() int {
	if v, err := strconv.Atoi(e.Get("CHECK_INTERVAL")); err == nil && v > 0 {
		return v
	}
	return 20
}

// Render produces the file contents. CRLF and a trailing newline match what
// the PowerShell installer writes.
func (e *envConfig) Render() string {
	var b strings.Builder
	write := func(k string) {
		v, ok := e.vals[k]
		if !ok || v == "" {
			return
		}
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(v)
		b.WriteString("\r\n")
	}
	for _, k := range knownEnvOrder {
		write(k)
	}
	for _, k := range e.extra {
		write(k)
	}
	return b.String()
}

func (e *envConfig) Save(path string) error {
	return os.WriteFile(ioPath(path), []byte(e.Render()), 0644)
}

func isKnownEnvKey(key string) bool {
	for _, k := range knownEnvOrder {
		if k == key {
			return true
		}
	}
	return false
}
