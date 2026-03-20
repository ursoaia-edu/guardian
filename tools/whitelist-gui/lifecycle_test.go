package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateDeletionTargetRejectsSystemDirs(t *testing.T) {
	winDir := os.Getenv("WINDIR")
	if winDir == "" {
		winDir = `C:\Windows`
	}

	cases := []string{
		"",
		`C:\`,
		winDir,
		filepath.Join(winDir, "System32"),
		filepath.Join(winDir, "SysWOW64"),
		os.Getenv("ProgramData"),
		os.Getenv("USERPROFILE"),
	}

	// An unset environment variable collapses to "", which the empty-path guard
	// must reject just as firmly as a real system directory.
	for _, dir := range cases {
		if err := validateDeletionTarget(dir); err == nil {
			t.Errorf("validateDeletionTarget(%q) = nil, want refusal", dir)
		}
	}
}

func TestValidateDeletionTargetRequiresAgentExe(t *testing.T) {
	// Deep enough to pass the depth check, but with no agent binary in it.
	dir := filepath.Join(t.TempDir(), "a", "b", "c")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}

	err := validateDeletionTarget(dir)
	if err == nil {
		t.Fatal("expected refusal for a directory without the agent executable")
	}
	if !strings.Contains(err.Error(), "procsentinel-agent") {
		t.Errorf("error = %q, want it to mention the missing agent binary", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "procsentinel-agent64.exe"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := validateDeletionTarget(dir); err != nil {
		t.Errorf("validateDeletionTarget with agent present = %v, want nil", err)
	}
}

func TestPathDepthAndDriveRoot(t *testing.T) {
	depths := map[string]int{
		`C:\`:        0,
		`C:\a`:       1,
		`C:\a\b`:     2,
		`C:\a\b\c`:   3,
		`C:\a\b\c\d`: 4,
	}
	for in, want := range depths {
		if got := pathDepth(filepath.Clean(in)); got != want {
			t.Errorf("pathDepth(%q) = %d, want %d", in, got, want)
		}
	}

	if !isDriveRoot(`C:\`) {
		t.Error(`isDriveRoot("C:\") = false, want true`)
	}
	if isDriveRoot(`C:\Windows`) {
		t.Error(`isDriveRoot("C:\Windows") = true, want false`)
	}
}

func TestEnvConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	content := "SERVER_ADDRESS=http://host:8080\r\n# comment\n\nTOKEN=abc\nCUSTOM=keep\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadEnvConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Get("SERVER_ADDRESS") != "http://host:8080" {
		t.Errorf("SERVER_ADDRESS = %q", cfg.Get("SERVER_ADDRESS"))
	}
	if cfg.Get("TOKEN") != "abc" {
		t.Errorf("TOKEN = %q", cfg.Get("TOKEN"))
	}

	// An unrecognised key must survive a rewrite rather than being dropped.
	if cfg.Get("CUSTOM") != "keep" {
		t.Errorf("CUSTOM = %q, want keep", cfg.Get("CUSTOM"))
	}

	rendered := cfg.Render()
	if !strings.Contains(rendered, "CUSTOM=keep") {
		t.Errorf("rendered output lost the custom key:\n%s", rendered)
	}
	if !strings.HasPrefix(rendered, "SERVER_ADDRESS=") {
		t.Errorf("rendered output should start with SERVER_ADDRESS:\n%s", rendered)
	}
}

func TestEnvConfigCheckIntervalFallback(t *testing.T) {
	cfg := newEnvConfig()
	if got := cfg.CheckInterval(); got != 20 {
		t.Errorf("default CheckInterval = %d, want 20 (the agent's service-mode default)", got)
	}

	cfg.Set("CHECK_INTERVAL", "5")
	if got := cfg.CheckInterval(); got != 5 {
		t.Errorf("CheckInterval = %d, want 5", got)
	}

	cfg.Set("CHECK_INTERVAL", "nonsense")
	if got := cfg.CheckInterval(); got != 20 {
		t.Errorf("CheckInterval with bad value = %d, want the 20 fallback", got)
	}
}

func TestValidateInstallOptions(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "procsentinel-agent64.exe")
	if err := os.WriteFile(exe, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	good := func() installOptions {
		cfg := newEnvConfig()
		cfg.Set("SERVER_ADDRESS", "http://host:8080")
		cfg.Set("TOKEN", "t")
		return installOptions{SourceExe: exe, TargetDir: dir, Env: cfg}
	}

	if err := validateInstallOptions(good()); err != nil {
		t.Fatalf("valid options rejected: %v", err)
	}

	bad := good()
	bad.Env.Set("SERVER_ADDRESS", "host:8080")
	if err := validateInstallOptions(bad); err == nil {
		t.Error("SERVER_ADDRESS without a scheme was accepted")
	}

	bad = good()
	bad.Env.Set("IDENTITY", "abc")
	if err := validateInstallOptions(bad); err == nil {
		t.Error("non-integer IDENTITY was accepted")
	}

	bad = good()
	bad.Env.Set("CHECK_INTERVAL", "0")
	if err := validateInstallOptions(bad); err == nil {
		t.Error("zero CHECK_INTERVAL was accepted")
	}

	bad = good()
	bad.SourceExe = filepath.Join(dir, "missing.exe")
	if err := validateInstallOptions(bad); err == nil {
		t.Error("missing source executable was accepted")
	}
}

func TestInspectUpdateDetectsIdenticalAndArchChange(t *testing.T) {
	dir := t.TempDir()

	same1 := filepath.Join(dir, "procsentinel-agent64.exe")
	same2 := filepath.Join(dir, "copy", "procsentinel-agent64.exe")
	other := filepath.Join(dir, "procsentinel-agent32.exe")

	if err := os.MkdirAll(filepath.Dir(same2), 0755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{same1, same2} {
		if err := os.WriteFile(p, []byte("identical payload"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(other, []byte("different payload"), 0644); err != nil {
		t.Fatal(err)
	}

	info, err := inspectUpdate(same2, same1)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Identical {
		t.Error("byte-identical files not reported as identical")
	}
	if info.ArchChange {
		t.Error("same filename reported as an architecture change")
	}

	info, err = inspectUpdate(other, same1)
	if err != nil {
		t.Fatal(err)
	}
	if info.Identical {
		t.Error("different files reported as identical")
	}
	if !info.ArchChange {
		t.Error("32-bit replacing 64-bit not reported as an architecture change")
	}

	// An architecture change must be refused rather than silently overwriting a
	// file the service registration no longer points at.
	if err := performUpdate(info, func(string) {}); err == nil {
		t.Error("performUpdate accepted an architecture change")
	}
}

func TestIsPlaceholder(t *testing.T) {
	for _, v := range []string{"your_server_address", "your_token_here", "YOUR_TOKEN"} {
		if !isPlaceholder(v) {
			t.Errorf("isPlaceholder(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"http://host:8080", "abc123", ""} {
		if isPlaceholder(v) {
			t.Errorf("isPlaceholder(%q) = true, want false", v)
		}
	}
}

func TestAgentSearchPathsFollowDistLayout(t *testing.T) {
	paths := agentSearchPaths(`C:\dist\agent`, "64")

	want := filepath.Join(`C:\dist\agent`, "bin", "agent", "procsentinel-agent64.exe")
	if paths[0] != want {
		t.Errorf("first search path = %q, want %q", paths[0], want)
	}
	if len(paths) != 3 {
		t.Errorf("got %d search paths, want 3", len(paths))
	}
}

func TestSelfProcessNameIsProtectedByBuiltinList(t *testing.T) {
	// The console must survive whitelist enforcement, otherwise the agent closes
	// it about a second after it opens.
	for _, name := range []string{"guardian.exe"} {
		if !isBuiltinProtected(name) {
			t.Errorf("%s is not in the builtin protected list", name)
		}
	}
}

func TestSysnativeToSystem32(t *testing.T) {
	cases := map[string]string{
		`C:\WINDOWS\Sysnative\ProcSentinel\agent\procsentinel-agent64.exe`: `C:\WINDOWS\System32\ProcSentinel\agent\procsentinel-agent64.exe`,
		`C:\Windows\sysnative\ProcSentinel\agent.exe`:                      `C:\Windows\System32\ProcSentinel\agent.exe`,
		`C:\Windows\System32\ProcSentinel\agent.exe`:                       `C:\Windows\System32\ProcSentinel\agent.exe`,
		`D:\Tools\agent.exe`: `D:\Tools\agent.exe`,
	}
	for in, want := range cases {
		if got := sysnativeToSystem32(in); got != want {
			t.Errorf("sysnativeToSystem32(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOSArchPrefersTheOperatingSystem(t *testing.T) {
	// The console ships as a 32-bit build, so it must not report its own
	// architecture: on 64-bit Windows it has to install the 64-bit agent.
	t.Setenv("PROCESSOR_ARCHITEW6432", "AMD64")
	t.Setenv("PROCESSOR_ARCHITECTURE", "x86")
	if got := osArch(); got != "64" {
		t.Errorf("osArch() under WOW64 = %q, want 64", got)
	}

	t.Setenv("PROCESSOR_ARCHITEW6432", "")
	t.Setenv("PROCESSOR_ARCHITECTURE", "x86")
	if got := osArch(); got != "32" {
		t.Errorf("osArch() on real 32-bit Windows = %q, want 32", got)
	}

	t.Setenv("PROCESSOR_ARCHITECTURE", "AMD64")
	if got := osArch(); got != "64" {
		t.Errorf("osArch() on 64-bit Windows = %q, want 64", got)
	}
}

func TestPathsHandedOutsideStayCanonical(t *testing.T) {
	// Sysnative is a private alias for 32-bit processes. It must never reach the
	// UI, a dialog, a service registration, or Explorer -- only our own file
	// operations may use it.
	dir, _ := installDir()
	if strings.Contains(strings.ToLower(dir), "sysnative") {
		t.Errorf("installDir() = %q, want a canonical System32 path", dir)
	}
	if strings.Contains(strings.ToLower(defaultInstallDir), "sysnative") {
		t.Errorf("defaultInstallDir = %q, want a canonical System32 path", defaultInstallDir)
	}
}

func TestIoPathLeavesUnrelatedPathsAlone(t *testing.T) {
	// Whatever the build architecture, a path outside System32 is never rewritten.
	for _, p := range []string{`D:\Tools\agent.exe`, `C:\ProgramData\ProcSentinel`, t.TempDir()} {
		if got := ioPath(p); got != p {
			t.Errorf("ioPath(%q) = %q, want it unchanged", p, got)
		}
	}
}
