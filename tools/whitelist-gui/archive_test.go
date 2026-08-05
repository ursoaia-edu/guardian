package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLooksLikeArchivePreview(t *testing.T) {
	const temp = `C:\Users\Kate\AppData\Local\Temp`
	cases := []struct {
		name string
		dir  string
		want bool
	}{
		{"explorer's own scratch folder", temp + `\Temp1_Guardian.zip`, true},
		{"explorer, opened a second time", temp + `\Temp7_Guardian (1).zip`, true},
		{"winrar", temp + `\Rar$EXa0.789`, true},
		{"7-zip", temp + `\7zO8A3C2E1B`, true},
		{"the archive itself as a path segment", `C:\Downloads\Guardian.zip\`, true},
		{"a .rar presented as a folder", `D:\stuff\guardian.rar`, true},
		{"an extracted folder in Downloads", `C:\Users\Kate\Downloads\Guardian`, false},
		{"the installed agent folder", `C:\Windows\System32\ProcSentinel\agent`, false},
		{"a folder that merely starts with temp", `C:\Templates\Guardian`, false},
		{"7z outside TEMP is somebody's own folder", `D:\7zips\Guardian`, false},
		{"empty", "", false},
	}
	for _, c := range cases {
		if got := looksLikeArchivePreview(c.dir, temp); got != c.want {
			t.Errorf("%s: looksLikeArchivePreview(%q) = %v, want %v", c.name, c.dir, got, c.want)
		}
	}
}

// Forward slashes reach this code from a shortcut or a shell that normalises
// them, and the verdict must not change.
func TestLooksLikeArchivePreviewAcceptsForwardSlashes(t *testing.T) {
	const temp = `C:\Users\Kate\AppData\Local\Temp`
	if !looksLikeArchivePreview("C:/Users/Kate/AppData/Local/Temp/Temp1_Guardian.zip", temp) {
		t.Error("a forward-slash path was not recognised as an archive preview")
	}
}

func TestInspectSurroundings(t *testing.T) {
	const temp = `C:\Users\Kate\AppData\Local\Temp`
	cases := []struct {
		name    string
		dir     string
		payload bool
		want    archiveVerdict
	}{
		// The payload is what actually matters: a folder holding the agent
		// binaries works even if it is named like a temporary one.
		{"extracted folder", `C:\Users\Kate\Downloads\Guardian`, true, archiveOK},
		{"temp folder that somehow has the files", temp + `\Temp1_Guardian.zip`, true, archiveOK},
		{"run from inside the ZIP", temp + `\Temp1_Guardian.zip`, false, archiveUnextracted},
		{"console copied somewhere on its own", `C:\Tools`, false, archivePayloadMissing},
	}
	for _, c := range cases {
		if got := inspectSurroundings(c.dir, temp, c.payload); got != c.want {
			t.Errorf("%s: inspectSurroundings(%q, payload=%v) = %v, want %v",
				c.name, c.dir, c.payload, got, c.want)
		}
	}
}

// payloadPresent must agree with the search findAgentExe does, including the
// dist/agent layout the archive ships.
func TestPayloadPresentFindsTheShippedLayout(t *testing.T) {
	dir := t.TempDir()
	if payloadPresent(dir) {
		t.Fatal("an empty folder reported a payload")
	}
	nested := filepath.Join(dir, "bin", "agent")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, agentExeName("64")), []byte("MZ"), 0o644); err != nil {
		t.Fatalf("write agent: %v", err)
	}
	if !payloadPresent(dir) {
		t.Error("the shipped bin/agent layout was not recognised")
	}
}
