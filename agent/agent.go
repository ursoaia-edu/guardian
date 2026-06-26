package main

import (
	"errors"
	"log"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// agentVersion is reported to the server at enrollment and on every sync.
// A var so a release build can stamp it: -ldflags "-X main.agentVersion=3.1.0".
var agentVersion = "3.0.0"

// logger is the one seam between console mode (stdout) and service mode (the
// Windows event log). Everything below logs through it, which is what lets
// both modes share a single run loop instead of the two copies that used to
// drift apart.
type logger interface {
	Infof(format string, args ...any)
	Warnf(format string, args ...any)
	Errorf(format string, args ...any)
}

type consoleLogger struct{}

func (consoleLogger) Infof(format string, args ...any)  { log.Printf(format, args...) }
func (consoleLogger) Warnf(format string, args ...any)  { log.Printf("WARNING: "+format, args...) }
func (consoleLogger) Errorf(format string, args ...any) { log.Printf("ERROR: "+format, args...) }

type runMode string

const (
	runModeConsole runMode = "console"
	runModeService runMode = "service"
)

const (
	envFileName = ".env"

	defaultConsoleInterval = 10 * time.Second
	defaultServiceInterval = 20 * time.Second

	// enrollRetryInterval paces every "not enrolled yet" outcome: no binding
	// token, a rejected one, a plan at its limit, or no connection. A minute is
	// slow enough not to hammer anything and fast enough that fixing the cause
	// in the cabinet takes effect without touching the machine.
	enrollRetryInterval = time.Minute

	enforceInterval = time.Second

	// shutdownBackoff is how long enforcement pauses after it asks the OS to
	// power off, so a shutdown that takes a few seconds to land is not
	// requested again every second in the meantime.
	shutdownBackoff = time.Minute
)

// config is what .env (and, above it, the real environment) provides.
type config struct {
	ServerAddress string
	BindingToken  string
	CheckInterval time.Duration
	Mode          runMode
}

// loadConfig reads .env from the working directory. Real environment
// variables take precedence over the file, so a value can be overridden for
// one run without editing anything.
func loadConfig(mode runMode) (config, error) {
	cfg := config{Mode: mode, CheckInterval: defaultConsoleInterval}
	if mode == runModeService {
		cfg.CheckInterval = defaultServiceInterval
	}

	vals, err := readEnvFile(envFileName)
	get := func(key string) string {
		if v := os.Getenv(key); v != "" {
			return v
		}
		return vals[key]
	}

	cfg.ServerAddress = strings.TrimRight(strings.TrimSpace(get("SERVER_ADDRESS")), "/")
	cfg.BindingToken = strings.TrimSpace(get("BINDING_TOKEN"))
	if v, convErr := strconv.Atoi(get("CHECK_INTERVAL")); convErr == nil && v > 0 {
		cfg.CheckInterval = time.Duration(v) * time.Second
	}
	return cfg, err
}

// policyState is the last policy the agent received, shared between the sync
// goroutine and the enforcement loop. The old code shared a bare pointer and
// documented the race; a mutex costs nothing here.
type policyState struct {
	mu  sync.RWMutex
	cur *SyncResponse
}

func (p *policyState) set(s *SyncResponse) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cur = s
}

func (p *policyState) get() *SyncResponse {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.cur
}

type agent struct {
	cfg    config
	lg     logger
	client *apiClient
	state  policyState

	// creds is nil until this machine has enrolled. It is only ever touched
	// from the sync goroutine.
	creds *credentials

	// generatedGUID stands in for the machine's own identifier when the OS
	// cannot supply one. Fixed for the life of the process so retries enroll
	// as the same machine.
	generatedGUID string

	started time.Time

	// Intervals live on the struct so tests can shorten them.
	checkInterval time.Duration
	enrollRetry   time.Duration

	lastSummary string
}

func newAgent(cfg config, lg logger) *agent {
	return &agent{
		cfg:           cfg,
		lg:            lg,
		client:        newAPIClient(cfg.ServerAddress),
		started:       time.Now(),
		checkInterval: cfg.CheckInterval,
		enrollRetry:   enrollRetryInterval,
	}
}

// runAgent is the whole agent: load configuration and the cached policy,
// enroll if needed, then sync and enforce until stopCh closes. Console mode
// and service mode both end up here.
func runAgent(stopCh <-chan struct{}, lg logger, mode runMode) {
	cfg, err := loadConfig(mode)
	if err != nil {
		lg.Warnf("Could not load %s: %v", envFileName, err)
	}
	if cfg.ServerAddress == "" {
		lg.Errorf("SERVER_ADDRESS is not set in %s; this agent has no server to talk to", envFileName)
	}

	initWhitelist(lg)

	a := newAgent(cfg, lg)
	a.lg.Infof("Server address: %s", cfg.ServerAddress)
	a.loadCachedPolicy()
	a.loadCredentials()

	go a.syncLoop(stopCh)
	a.enforceLoop(stopCh)
}

// loadCachedPolicy restores the last policy saved to sync.json, so the machine
// is enforcing something from the first second even if the server is
// unreachable — or this machine's token has since been revoked.
func (a *agent) loadCachedPolicy() {
	cached, err := loadSyncFromFile()
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			a.lg.Warnf("Could not read sync.json: %v", err)
		}
		return
	}
	a.state.set(cached)
	a.lg.Infof("Loaded the last known policy from sync.json: %d applications, mode=%s",
		len(cached.Applications), cached.Mode)
}

func (a *agent) loadCredentials() {
	creds, err := loadCredentialsFile()
	if err != nil {
		a.lg.Warnf("Could not read %s: %v", credentialsFileName, err)
		return
	}
	if creds == nil {
		return
	}
	// A token is only good for the server that minted it. If SERVER_ADDRESS
	// changed underneath a saved credential, using it would produce an
	// endless stream of 401s that look like a revocation.
	if creds.ServerAddress != "" && creds.ServerAddress != a.cfg.ServerAddress {
		a.lg.Warnf("%s was issued by %s but SERVER_ADDRESS is now %s; treating this machine as not enrolled",
			credentialsFileName, creds.ServerAddress, a.cfg.ServerAddress)
		return
	}
	a.creds = creds
	a.lg.Infof("Enrolled (since %s)", creds.EnrolledAt.Format(time.RFC3339))
}

func (a *agent) syncLoop(stopCh <-chan struct{}) {
	for {
		wait := a.step()
		select {
		case <-stopCh:
			return
		case <-time.After(wait):
		}
	}
}

// step does one unit of sync-side work and reports how long to wait before
// the next. Kept separate from the loop so a test can drive it directly.
func (a *agent) step() time.Duration {
	if a.creds == nil {
		return a.tryEnroll()
	}
	a.syncOnce()
	return a.checkInterval
}

// tryEnroll exchanges the installer's binding token for this machine's own
// token. Every failure is a distinct message, because the customer fixes a
// revoked token and a full plan in the cabinet and a dead network at home,
// and one generic "enrollment failed" would tell them nothing.
func (a *agent) tryEnroll() time.Duration {
	if a.cfg.BindingToken == "" {
		a.lg.Warnf("Not enrolled and no BINDING_TOKEN in %s: this computer cannot join the fleet. "+
			"Reinstall with the installer from the cabinet. The last known policy stays in force meanwhile.", envFileName)
		return a.enrollRetry
	}

	p := a.passport()
	token, err := a.client.enroll(a.cfg.BindingToken, p)
	switch {
	case err == nil:
	case errors.Is(err, errBindingTokenRejected):
		a.lg.Errorf("Enrollment refused: the installer's token is no longer valid (%v). "+
			"Download a fresh installer from the cabinet; retrying in %s in case it was re-enabled.", err, a.enrollRetry)
		return a.enrollRetry
	case errors.Is(err, errPlanLimit):
		a.lg.Errorf("Enrollment refused: %v. Upgrade the plan in the cabinet; the agent retries on its own every %s.", err, a.enrollRetry)
		return a.enrollRetry
	default:
		a.lg.Warnf("Enrollment failed: %v. Retrying in %s.", err, a.enrollRetry)
		return a.enrollRetry
	}

	creds := &credentials{
		AgentToken:    token,
		MachineGUID:   p.MachineGUID,
		ServerAddress: a.cfg.ServerAddress,
		EnrolledAt:    time.Now(),
	}
	if err := saveCredentialsFile(creds); err != nil {
		// Enrolled on the server but nothing on disk: the next attempt
		// re-enrolls the same machine_guid, which rotates the token rather
		// than adding a duplicate, so retrying is safe.
		a.lg.Errorf("Enrolled, but could not save %s: %v. Retrying enrollment in %s.", credentialsFileName, err, a.enrollRetry)
		return a.enrollRetry
	}
	a.creds = creds

	// The binding token has done its job. It is account-wide and reusable, so
	// it has no business staying on this disk: from here the machine lives on
	// its own token.
	if err := removeEnvKey(envFileName, "BINDING_TOKEN"); err != nil {
		a.lg.Warnf("Enrolled, but could not remove BINDING_TOKEN from %s: %v", envFileName, err)
	}
	a.cfg.BindingToken = ""

	a.lg.Infof("Enrolled with %s as %s (machine %s)", a.cfg.ServerAddress, p.Hostname, p.MachineGUID)
	return 0 // sync straight away
}

func (a *agent) syncOnce() {
	resp, err := a.client.sync(a.creds.AgentToken, a.runtimeTelemetry())
	if err != nil {
		if errors.Is(err, errUnauthorized) {
			// Fail secure: a machine removed from the account, or re-enrolled
			// elsewhere with this GUID, keeps enforcing what it last knew.
			// Deleting a computer in the cabinet must not free it.
			a.lg.Errorf("The server no longer accepts this computer's token (%v). "+
				"Keeping the last known policy in force. Reinstall from the cabinet to re-enroll.", err)
		} else {
			a.lg.Warnf("Sync failed: %v. Keeping the last known policy.", err)
		}
		return
	}

	a.state.set(resp)
	if err := saveSyncToFile(resp); err != nil {
		a.lg.Warnf("Failed to save sync.json: %v", err)
	}
	// One line per change, not one per poll: three lines a minute of
	// "nothing new" would bury everything else in the event log.
	summary := policySummary(resp)
	if summary != a.lastSummary {
		a.lg.Infof("Synced: %s", summary)
		a.lastSummary = summary
	}
}

func policySummary(resp *SyncResponse) string {
	power := "n/a"
	if status, found := getClientEntry(resp, "power"); found {
		power = strconv.FormatBool(status)
	}
	return "mode=" + resp.Mode + ", " + strconv.Itoa(len(resp.Applications)) + " applications, power=" + power
}

// passport is what the server learns about this machine once, at enrollment.
func (a *agent) passport() passport {
	guid, err := machineGUID()
	if err != nil || guid == "" {
		if a.generatedGUID == "" {
			a.generatedGUID = randomGUID()
			a.lg.Warnf("Could not read a machine identifier (%v); using a generated one for this enrollment", err)
		}
		guid = a.generatedGUID
	}
	hostname, _ := os.Hostname()
	osName, osBuild := osInfo()
	return passport{
		MachineGUID:  guid,
		Hostname:     hostname,
		OSName:       osName,
		OSBuild:      osBuild,
		Arch:         runtime.GOARCH,
		AgentVersion: agentVersion,
		Hardware: map[string]any{
			"cpu_count": runtime.NumCPU(),
			"go_os":     runtime.GOOS,
		},
	}
}

// runtimeTelemetry is the cheap, volatile half of the passport, sent on every
// sync and overwritten server-side each time.
func (a *agent) runtimeTelemetry() map[string]any {
	return map[string]any{
		"uptime_s":      int64(time.Since(a.started).Seconds()),
		"mode":          string(a.cfg.Mode),
		"agent_version": agentVersion,
	}
}

// enforceLoop applies the current policy once a second until stopped. The
// rules are exactly the ones the previous console and service loops applied;
// they are just no longer written twice.
func (a *agent) enforceLoop(stopCh <-chan struct{}) {
	ticker := time.NewTicker(enforceInterval)
	defer ticker.Stop()

	for {
		select {
		case <-stopCh:
			a.lg.Infof("Agent stopping")
			return
		case <-ticker.C:
		}

		state := a.state.get()
		if state == nil || state.Mode == "free" {
			continue
		}
		// Whitelist mode with an empty list means "allow nothing but the
		// protected system processes" — that is how the server expresses a
		// locked machine, so it must enforce. An empty blacklist enforces nothing.
		if len(state.Applications) == 0 && state.Mode != "whitelist" {
			continue
		}

		if powerStatus, found := getClientEntry(state, "power"); found && !powerStatus {
			a.lg.Infof("Shutdown PC triggered: power disabled")
			if err := shutdownPCService(); err != nil {
				a.lg.Errorf("Failed to shutdown PC: %v", err)
			}
			select {
			case <-stopCh:
				return
			case <-time.After(shutdownBackoff):
			}
			continue
		}

		a.enforce(state)
	}
}

// enforce kills what the policy says to kill, once, against the current
// process list.
func (a *agent) enforce(state *SyncResponse) {
	processes, err := getProcessList()
	if err != nil {
		a.lg.Errorf("Error getting process list: %v", err)
		return
	}
	processText := strings.ToLower(processes)

	switch state.Mode {
	case "blacklist":
		for _, app := range state.Applications {
			if app.Name != "" && strings.Contains(processText, strings.ToLower(app.Name)) {
				if err := killProcess(app.Name); err == nil {
					a.lg.Infof("Killed process: %s", app.Name)
				} else {
					a.lg.Errorf("Failed to kill process %s: %v", app.Name, err)
				}
			}
		}
	case "whitelist":
		allowedSet := make(map[string]bool, len(state.Applications))
		for _, app := range state.Applications {
			allowedSet[strings.ToLower(app.Name)] = true
		}
		for _, line := range strings.Split(processes, "\n") {
			procName := extractProcessName(strings.TrimSpace(line))
			if procName == "" {
				continue
			}
			if !allowedSet[strings.ToLower(procName)] && !isSystemProcess(procName) {
				if err := killProcess(procName); err == nil {
					a.lg.Infof("Killed non-whitelisted process: %s", procName)
				}
			}
		}
	}
}
