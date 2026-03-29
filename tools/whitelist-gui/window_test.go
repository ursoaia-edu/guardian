package main

import (
	"os"
	"testing"
)

// TestBuildWindow is a smoke test for the declarative layout: a typo in a
// widget property only shows up when the window is actually created. It needs
// an interactive desktop session, so it is opt-in.
//
//	set PS_GUI_TEST=1 && go test -run TestBuildWindow
func TestBuildWindow(t *testing.T) {
	if os.Getenv("PS_GUI_TEST") == "" {
		t.Skip("set PS_GUI_TEST=1 to run the window smoke test")
	}

	a := &app{
		wl:        map[string]bool{},
		running:   map[string]bool{},
		wlModel:   &rowModel{},
		procModel: &rowModel{},
	}
	a.dir, a.dirSource = `C:\nonexistent`, "test"

	if err := a.build(); err != nil {
		t.Fatalf("build: %v", err)
	}
	defer a.mw.Dispose()

	// Populating the models exercises the paths that touch every widget the
	// handlers reach for, without opening any dialogs.
	a.running = map[string]bool{"chrome.exe": true, "explorer.exe": true}
	a.wl = map[string]bool{"chrome.exe": true}
	a.rebuild()

	if a.wlModel.RowCount() != 1 {
		t.Errorf("whitelist rows = %d, want 1", a.wlModel.RowCount())
	}
	if a.procModel.RowCount() != 2 {
		t.Errorf("process rows = %d, want 2", a.procModel.RowCount())
	}

	// explorer.exe is protected by the agent's built-in list, chrome.exe by the
	// file, so neither should be reported as killable.
	a.procModel.SetOnlyKillable(true)
	if got := a.procModel.RowCount(); got != 0 {
		t.Errorf("killable rows = %d, want 0", got)
	}

	a.setStatus("ok")
	a.setDirty(true)
	a.setDirty(false)
}

// TestAppIcon covers the title bar icon, which is set separately from the PE
// resource icon and whose absence is invisible until someone opens the window.
//
// It does not touch a window on purpose: SetIcon schedules a re-layout, and a
// test that disposes the form right afterwards races walk's layout goroutine.
func TestAppIcon(t *testing.T) {
	icon := appIcon()
	if icon == nil {
		t.Fatal("appIcon() = nil, want an icon from the resource or the embedded fallback")
	}
	defer icon.Dispose()

	if len(guardianMark) == 0 {
		t.Error("guardian-mark.png was not embedded")
	}
}
