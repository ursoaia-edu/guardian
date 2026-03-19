package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// osArch reports the architecture of the operating system, not of this binary.
// The console ships as a 32-bit build so it runs everywhere, but it must still
// install the 64-bit agent on 64-bit Windows -- the same rule
// Install-Guardian.bat applies.
func osArch() string {
	if os.Getenv("PROCESSOR_ARCHITEW6432") != "" {
		return "64"
	}
	switch strings.ToLower(os.Getenv("PROCESSOR_ARCHITECTURE")) {
	case "x86":
		return "32"
	case "":
		// Fall back to our own architecture if the variable is missing.
		if runtime.GOARCH == "386" {
			return "32"
		}
		return "64"
	}
	return "64"
}

func agentExeName(arch string) string {
	return "procsentinel-agent" + arch + ".exe"
}

// agentExeNames lists both architectures, for cases where either may be found.
func agentExeNames() []string {
	return []string{agentExeName("64"), agentExeName("32")}
}

// agentSearchPaths lists where the agent executable is looked for, relative to
// the console's own directory, matching the shipped dist/agent/ layout.
func agentSearchPaths(guiDir, arch string) []string {
	name := agentExeName(arch)
	return []string{
		filepath.Join(guiDir, "bin", "agent", name),
		filepath.Join(guiDir, "agent", name),
		filepath.Join(guiDir, name),
	}
}

// findAgentExe returns the first existing candidate, or "" if none matched.
func findAgentExe(guiDir, arch string) string {
	for _, p := range agentSearchPaths(guiDir, arch) {
		if info, err := os.Stat(ioPath(p)); err == nil && !info.IsDir() {
			return p
		}
	}
	return ""
}

// guiDir is the directory holding this executable.
func guiDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

// templateEnvPath is the configuration template shipped next to the console,
// the same file the PowerShell installer reads.
func templateEnvPath() string {
	return filepath.Join(guiDir(), "agent.env")
}

type installOptions struct {
	SourceExe string
	TargetDir string
	Env       *envConfig
}

// validateInstallOptions checks the configuration before anything is written.
func validateInstallOptions(opts installOptions) error {
	if opts.SourceExe == "" {
		return fmt.Errorf("no agent executable selected")
	}
	if _, err := os.Stat(ioPath(opts.SourceExe)); err != nil {
		return fmt.Errorf("agent executable is not readable: %v", err)
	}
	if opts.TargetDir == "" {
		return fmt.Errorf("no installation folder given")
	}
	if opts.Env == nil {
		return fmt.Errorf("no configuration given")
	}

	addr := opts.Env.Get("SERVER_ADDRESS")
	if addr == "" {
		return fmt.Errorf("SERVER_ADDRESS cannot be empty")
	}
	if !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
		return fmt.Errorf("SERVER_ADDRESS must start with http:// or https://")
	}

	if id := opts.Env.Get("IDENTITY"); id != "" {
		if _, err := strconv.Atoi(id); err != nil {
			return fmt.Errorf("IDENTITY must be an integer")
		}
	}
	if ci := opts.Env.Get("CHECK_INTERVAL"); ci != "" {
		v, err := strconv.Atoi(ci)
		if err != nil || v <= 0 {
			return fmt.Errorf("CHECK_INTERVAL must be a positive integer")
		}
	}
	return nil
}

// performInstall runs the full installation. Service registration is delegated
// to the agent binary itself: it owns its display name, description, start type
// and event-log source, and duplicating that here would create a second source
// of truth for the agent's own identity.
func performInstall(opts installOptions, progress func(string)) error {
	if err := validateInstallOptions(opts); err != nil {
		return err
	}

	targetExe := filepath.Join(opts.TargetDir, filepath.Base(opts.SourceExe))

	progress("stopping and deregistering the previous version")
	if err := removeExistingService(opts.TargetDir); err != nil {
		return err
	}

	progress("creating the installation folder")
	if err := os.MkdirAll(ioPath(opts.TargetDir), 0755); err != nil {
		return fmt.Errorf("could not create the folder: %v", err)
	}

	progress("copying the agent")
	if err := copyFile(opts.SourceExe, targetExe); err != nil {
		return fmt.Errorf("could not copy the agent: %v", err)
	}

	progress("writing the configuration")
	if err := opts.Env.Save(filepath.Join(opts.TargetDir, envFileName)); err != nil {
		return fmt.Errorf("could not write .env: %v", err)
	}

	progress("registering the service")
	if out, err := runAgentCommand(targetExe, "-install"); err != nil {
		return fmt.Errorf("service registration failed: %v%s", err, formatOutput(out))
	}

	// A 32-bit installer has to launch the agent through the Sysnative alias,
	// which the agent may then record as its own image path.
	progress("verifying the service registration")
	if err := normalizeServiceImagePath(); err != nil {
		return err
	}

	progress("starting the service")
	if err := startService(); err != nil {
		return fmt.Errorf("the service was registered but did not start: %v", err)
	}

	progress("waiting for whitelist.txt to be generated")
	waitForWhitelist(opts.TargetDir, 15*time.Second)

	return nil
}

// removeExistingService clears any prior registration. It prefers the agent's
// own -remove, and falls back to the SCM when the executable is gone, which is
// exactly the broken-install case.
func removeExistingService(dir string) error {
	if !serviceIsRegistered() {
		return nil
	}

	if err := stopService(); err != nil {
		return fmt.Errorf("could not stop the previous service: %v", err)
	}

	for _, name := range agentExeNames() {
		exe := filepath.Join(dir, name)
		if _, err := os.Stat(ioPath(exe)); err == nil {
			if _, err := runAgentCommand(exe, "-remove"); err == nil {
				return nil
			}
		}
	}

	return deleteServiceViaSCM()
}

// waitForWhitelist gives the freshly started agent a chance to generate the
// file. Without this the editor would show an empty list right after a
// successful install, which reads as a failure.
func waitForWhitelist(dir string, timeout time.Duration) bool {
	path := filepath.Join(dir, whitelistFileName)
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		if _, err := os.Stat(ioPath(path)); err == nil {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

// runAgentCommand invokes the agent binary without flashing a console window.
// The executable is reached through ioPath: a 32-bit process cannot launch
// anything under the real System32 without the Sysnative alias.
func runAgentCommand(exe string, args ...string) (string, error) {
	exe = ioPath(exe)

	cmd := exec.Command(exe, args...)
	cmd.Dir = filepath.Dir(exe)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	out, err := cmd.CombinedOutput()
	return string(out), err
}

func formatOutput(out string) string {
	out = strings.TrimSpace(out)
	if out == "" {
		return ""
	}
	return "\n\n" + out
}

func copyFile(src, dst string) error {
	in, err := os.Open(ioPath(src))
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(ioPath(dst), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}

	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
