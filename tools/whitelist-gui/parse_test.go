package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestUnquoteImagePath(t *testing.T) {
	cases := map[string]string{
		`"C:\Windows\System32\ProcSentinel\agent\procsentinel-agent64.exe"`: `C:\Windows\System32\ProcSentinel\agent\procsentinel-agent64.exe`,
		`C:\Windows\System32\ProcSentinel\agent\procsentinel-agent64.exe`:   `C:\Windows\System32\ProcSentinel\agent\procsentinel-agent64.exe`,
		`"C:\Program Files\PS\agent.exe" -service`:                          `C:\Program Files\PS\agent.exe`,
		`C:\ps\agent.exe -service`:                                          `C:\ps\agent.exe`,
	}
	for in, want := range cases {
		if got := unquoteImagePath(in); got != want {
			t.Errorf("unquoteImagePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExtractProcessName(t *testing.T) {
	cases := map[string]string{
		`"chrome.exe","1234","Console","1","100,000 K"`:  "chrome.exe",
		`"System Idle Process","0","Services","0","8 K"`: "System Idle Process",
		``:          "",
		`not csv`:   "",
		`"unclosed`: "",
	}
	for in, want := range cases {
		if got := extractProcessName(in); got != want {
			t.Errorf("extractProcessName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeName(t *testing.T) {
	cases := map[string]string{
		"  Notepad.EXE ":         "notepad.exe",
		`"chrome.exe"`:           "chrome.exe",
		`C:\Windows\notepad.exe`: "notepad.exe",
		`/usr/bin/foo`:           "foo",
		"":                       "",
	}
	for in, want := range cases {
		if got := normalizeName(in); got != want {
			t.Errorf("normalizeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLoadWhitelistMissingFileIsNotAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "whitelist.txt")

	names, exists, err := loadWhitelist(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exists {
		t.Error("exists = true for a missing file")
	}
	if len(names) != 0 {
		t.Errorf("names = %v, want empty", names)
	}
}

func TestLoadWhitelistParsing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "whitelist.txt")
	content := "Chrome.exe\r\n\r\n# a comment\n  notepad.exe  \nCHROME.EXE\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	names, exists, err := loadWhitelist(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !exists {
		t.Error("exists = false for an existing file")
	}

	want := []string{"chrome.exe", "notepad.exe"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("names = %v, want %v", names, want)
	}
}

func TestSaveWhitelistRoundTripAndBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "whitelist.txt")
	if err := os.WriteFile(path, []byte("old.exe\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := saveWhitelist(path, []string{"Zeta.exe", "alpha.exe"}); err != nil {
		t.Fatalf("saveWhitelist: %v", err)
	}

	backup, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("backup not written: %v", err)
	}
	if string(backup) != "old.exe\n" {
		t.Errorf("backup = %q, want the previous content", string(backup))
	}

	names, _, err := loadWhitelist(path)
	if err != nil {
		t.Fatalf("loadWhitelist: %v", err)
	}
	want := []string{"alpha.exe", "zeta.exe"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("names = %v, want %v", names, want)
	}
}

func TestRowModelFilterAndSelectionMapping(t *testing.T) {
	m := &rowModel{}
	m.SetRows([]row{
		{Name: "chrome.exe", Killable: true},
		{Name: "notepad.exe"},
		{Name: "chromium.exe", Killable: true},
	})

	if m.RowCount() != 3 {
		t.Fatalf("RowCount = %d, want 3", m.RowCount())
	}

	m.SetFilter("chrom")
	if m.RowCount() != 2 {
		t.Fatalf("filtered RowCount = %d, want 2", m.RowCount())
	}

	// Selection indexes are view indexes; they must map back to the right names
	// after filtering, not to positions in the unfiltered slice.
	got := m.namesAt([]int{1})
	if !reflect.DeepEqual(got, []string{"chromium.exe"}) {
		t.Errorf("namesAt([1]) = %v, want [chromium.exe]", got)
	}

	m.SetFilter("")
	m.SetOnlyKillable(true)
	if m.RowCount() != 2 {
		t.Errorf("onlyKillable RowCount = %d, want 2", m.RowCount())
	}
}
