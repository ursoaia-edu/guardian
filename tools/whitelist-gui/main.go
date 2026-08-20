package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

const appTitle = "Guardian - ProcSentinel Agent Console"

type app struct {
	mw *walk.MainWindow

	dir string
	// dirSource explains where dir came from, shown next to the path.
	dirSource string
	// dirManual suppresses re-resolution from the registry after lifecycle
	// operations, so an explicit choice is not silently overwritten.
	dirManual bool
	dirty     bool

	hlth health

	// wl is the in-memory whitelist being edited; running is the set of
	// process names seen at the last refresh.
	wl      map[string]bool
	running map[string]bool

	wlModel   *rowModel
	procModel *rowModel

	wlTable   *walk.TableView
	procTable *walk.TableView

	wlFilter   *walk.LineEdit
	procFilter *walk.LineEdit

	stateLabel   *walk.Label
	dirLabel     *walk.Label
	warnLabel    *walk.Label
	selfFixBtn   *walk.PushButton
	wlCountLabel *walk.Label
	statusLabel  *walk.Label

	svcLabel      *walk.Label
	exeLabel      *walk.Label
	confLabel     *walk.Label
	syncLabel     *walk.Label
	modeLabel     *walk.Label
	whitelistInfo *walk.Label
	logEdit       *walk.TextEdit

	installBtn   *walk.PushButton
	updateBtn    *walk.PushButton
	uninstallBtn *walk.PushButton
	startBtn     *walk.PushButton
	stopBtn      *walk.PushButton
	restartBtn   *walk.PushButton
	saveBtn      *walk.PushButton

	onlyKillable *walk.CheckBox
}

func main() {
	a := &app{
		wl:        map[string]bool{},
		running:   map[string]bool{},
		wlModel:   &rowModel{},
		procModel: &rowModel{},
	}
	a.resolveDir()

	if err := a.build(); err != nil {
		walk.MsgBox(nil, "Error", "Could not create the window:\n\n"+err.Error(), walk.MsgBoxIconError)
		os.Exit(1)
	}

	// The PE resource icon covers Explorer and the taskbar; the title bar needs
	// this. Losing the icon is not worth refusing to start over.
	if icon := appIcon(); icon != nil {
		_ = a.mw.SetIcon(icon)
	}

	a.mw.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		if !a.dirty {
			return
		}
		switch walk.MsgBox(a.mw, "Unsaved changes",
			"The list has been modified but not saved. Save before closing?",
			walk.MsgBoxYesNoCancel|walk.MsgBoxIconQuestion) {
		case walk.DlgCmdYes:
			if !a.saveToDisk() {
				*canceled = true
			}
		case walk.DlgCmdCancel:
			*canceled = true
		}
	})

	if !isElevated() {
		walk.MsgBox(a.mw, "Administrator rights required",
			"This program is running without administrator rights.\n\n"+
				"The agent folder lives under System32, so saving, installing and "+
				"service control will not work. Restart it as administrator.",
			walk.MsgBoxIconWarning)
	}

	a.reloadFromDisk()
	a.refreshAll()

	a.mw.Run()
}

// resolveDir re-derives the agent directory unless the operator picked one.
func (a *app) resolveDir() {
	if a.dirManual {
		return
	}
	dir, fromReg := installDir()
	a.dir = dir
	if fromReg {
		a.dirSource = "from the service registry"
	} else {
		a.dirSource = "default path, service not found"
	}
}

func (a *app) build() error {
	return MainWindow{
		AssignTo: &a.mw,
		Title:    appTitle,
		MinSize:  Size{Width: 980, Height: 660},
		Size:     Size{Width: 1080, Height: 740},
		Layout:   VBox{},
		Children: []Widget{
			a.bannerPanel(),
			TabWidget{
				Pages: []TabPage{
					a.whitelistPage(),
					a.lifecyclePage(),
				},
			},
			Label{AssignTo: &a.statusLabel, Text: ""},
		},
	}.Create()
}

func (a *app) bannerPanel() Widget {
	return Composite{
		Layout: Grid{Columns: 3, MarginsZero: true},
		Children: []Widget{
			Label{Text: "State:"},
			Label{AssignTo: &a.stateLabel, Text: "—"},
			PushButton{Text: "Refresh", OnClicked: func() { a.refreshAll() }},

			Label{Text: "Folder:"},
			Label{AssignTo: &a.dirLabel, Text: a.dirText()},
			PushButton{Text: "Change...", OnClicked: a.onPickFolder},

			Label{AssignTo: &a.warnLabel, Text: "", ColumnSpan: 2},
			PushButton{
				AssignTo: &a.selfFixBtn,
				Text:     "Add Guardian to whitelist",
				OnClicked: func() {
					a.addNames([]string{selfProcessName()})
				},
			},
		},
	}
}

func (a *app) whitelistPage() TabPage {
	return TabPage{
		Title:  "Allowed programs",
		Layout: VBox{},
		Children: []Widget{
			HSplitter{
				Children: []Widget{
					a.whitelistPanel(),
					a.processPanel(),
				},
			},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					PushButton{AssignTo: &a.saveBtn, Text: "Save", MinSize: Size{Width: 120}, OnClicked: a.onSave},
					PushButton{Text: "Reload from disk", OnClicked: a.onReload},
					PushButton{Text: "Open folder", OnClicked: a.onOpenFolder},
					HSpacer{},
				},
			},
		},
	}
}

func (a *app) whitelistPanel() Widget {
	return GroupBox{
		Title:  "Allowed programs (whitelist.txt)",
		Layout: VBox{},
		Children: []Widget{
			LineEdit{
				AssignTo:  &a.wlFilter,
				CueBanner: "filter...",
				OnTextChanged: func() {
					a.wlModel.SetFilter(a.wlFilter.Text())
				},
			},
			TableView{
				AssignTo:        &a.wlTable,
				Model:           a.wlModel,
				MultiSelection:  true,
				OnItemActivated: a.onRemoveSelected,
				Columns: []TableViewColumn{
					{Title: "Program", Width: 220},
					{Title: "Status", Width: 180},
				},
			},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					PushButton{Text: "Add manually...", OnClicked: a.onAddManual},
					PushButton{Text: "Remove selected", OnClicked: a.onRemoveSelected},
					HSpacer{},
					Label{AssignTo: &a.wlCountLabel, Text: ""},
				},
			},
		},
	}
}

func (a *app) processPanel() Widget {
	return GroupBox{
		Title:  "Currently running processes",
		Layout: VBox{},
		Children: []Widget{
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					LineEdit{
						AssignTo:  &a.procFilter,
						CueBanner: "filter...",
						OnTextChanged: func() {
							a.procModel.SetFilter(a.procFilter.Text())
						},
					},
					CheckBox{
						AssignTo: &a.onlyKillable,
						Text:     "only those the agent would close",
						OnCheckedChanged: func() {
							a.procModel.SetOnlyKillable(a.onlyKillable.Checked())
						},
					},
				},
			},
			TableView{
				AssignTo:        &a.procTable,
				Model:           a.procModel,
				MultiSelection:  true,
				OnItemActivated: a.onAddSelected,
				Columns: []TableViewColumn{
					{Title: "Process", Width: 220},
					{Title: "Status", Width: 180},
				},
			},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					PushButton{Text: "Refresh", OnClicked: func() { a.refreshAll() }},
					PushButton{Text: "<- Add to whitelist", OnClicked: a.onAddSelected},
					HSpacer{},
				},
			},
		},
	}
}

func (a *app) lifecyclePage() TabPage {
	return TabPage{
		Title:  "Status and installation",
		Layout: VBox{},
		Children: []Widget{
			GroupBox{
				Title:  "Diagnostics",
				Layout: Grid{Columns: 2},
				Children: []Widget{
					Label{Text: "Service:"}, Label{AssignTo: &a.svcLabel, Text: "—"},
					Label{Text: "Executable:"}, Label{AssignTo: &a.exeLabel, Text: "—"},
					Label{Text: "Configuration:"}, Label{AssignTo: &a.confLabel, Text: "—"},
					Label{Text: "Last sync:"}, Label{AssignTo: &a.syncLabel, Text: "—"},
					Label{Text: "Mode (from server):"}, Label{AssignTo: &a.modeLabel, Text: "—"},
					Label{Text: "whitelist.txt:"}, Label{AssignTo: &a.whitelistInfo, Text: "—"},
				},
			},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					PushButton{AssignTo: &a.installBtn, Text: "Install", MinSize: Size{Width: 110}, OnClicked: a.onInstall},
					PushButton{AssignTo: &a.updateBtn, Text: "Update agent", OnClicked: a.onUpdate},
					PushButton{AssignTo: &a.uninstallBtn, Text: "Uninstall", OnClicked: a.onUninstall},
					VSeparator{},
					PushButton{AssignTo: &a.startBtn, Text: "Start", OnClicked: a.onStart},
					PushButton{AssignTo: &a.stopBtn, Text: "Stop", OnClicked: a.onStop},
					PushButton{AssignTo: &a.restartBtn, Text: "Restart", OnClicked: a.onRestart},
					HSpacer{},
				},
			},
			GroupBox{
				Title:  "Agent event log",
				Layout: VBox{},
				Children: []Widget{
					TextEdit{AssignTo: &a.logEdit, ReadOnly: true, VScroll: true},
					Composite{
						Layout: HBox{MarginsZero: true},
						Children: []Widget{
							PushButton{Text: "Reload log", OnClicked: a.refreshEventLog},
							HSpacer{},
						},
					},
				},
			},
		},
	}
}

// --- paths ---

func (a *app) whitelistPath() string { return filepath.Join(a.dir, whitelistFileName) }
func (a *app) syncPath() string      { return filepath.Join(a.dir, syncFileName) }

// selfProcessName is this console's own executable name, lowercased, as the
// agent would see it in the tasklist output.
func selfProcessName() string {
	exe, err := os.Executable()
	if err != nil {
		return "guardian.exe"
	}
	return strings.ToLower(filepath.Base(exe))
}

// --- data flow ---

func (a *app) reloadFromDisk() {
	names, exists, err := loadWhitelist(a.whitelistPath())
	if err != nil {
		a.errBox("Could not read whitelist.txt", err)
		return
	}

	a.wl = make(map[string]bool, len(names))
	for _, n := range names {
		a.wl[n] = true
	}
	a.setDirty(false)

	if exists {
		a.setStatus("Loaded from " + a.whitelistPath())
	} else {
		a.setStatus("whitelist.txt does not exist yet - the agent creates it on its first run. " +
			"You can build the list here and save it.")
	}
	a.rebuild()
}

// refreshAll re-reads everything that can change while the window is open.
func (a *app) refreshAll() {
	a.resolveDir()
	a.dirLabel.SetText(a.dirText())

	procs, err := runningProcesses()
	if err != nil {
		a.setStatus("Could not read the process list: " + err.Error())
	}
	a.running = make(map[string]bool, len(procs))
	for _, p := range procs {
		a.running[p] = true
	}

	a.hlth = probeHealth(a.dir)

	a.updateDiagnostics()
	a.updateButtons()
	a.updateWarnings()
	a.rebuild()
}

func (a *app) updateDiagnostics() {
	h := a.hlth

	a.stateLabel.SetText(h.State.String())

	startType := "manual start"
	if h.StartAuto {
		startType = "automatic start"
	}
	if h.Registered {
		a.svcLabel.SetText(fmt.Sprintf("%s, %s", h.ServiceState, startType))
	} else {
		a.svcLabel.SetText("not registered")
	}

	switch {
	case h.ExePath == "":
		a.exeLabel.SetText("not found")
	case h.ExeExists:
		a.exeLabel.SetText(h.ExePath)
	default:
		a.exeLabel.SetText(h.ExePath + "  - file is missing")
	}

	switch {
	case !h.EnvExists:
		a.confLabel.SetText(".env is missing")
	default:
		a.confLabel.SetText(fmt.Sprintf("SERVER_ADDRESS %s, TOKEN %s, interval %ds",
			okMark(h.HasServerAddr), okMark(h.HasToken), h.CheckInterval))
	}

	a.syncLabel.SetText(h.SyncSummary())

	if h.Mode == "" {
		a.modeLabel.SetText("unknown")
	} else {
		a.modeLabel.SetText(fmt.Sprintf("%s, %d applications", h.Mode, h.AppCount))
	}

	if h.WhitelistExists {
		a.whitelistInfo.SetText(fmt.Sprintf("%d entries", h.WhitelistCount))
	} else {
		a.whitelistInfo.SetText("missing")
	}
}

func okMark(ok bool) string {
	if ok {
		return "set"
	}
	return "NOT SET"
}

func (a *app) updateButtons() {
	h := a.hlth
	canInstall := !h.Registered || h.State == stateBroken
	candidate := findAgentExe(guiDir(), osArch())

	a.installBtn.SetEnabled(canInstall)
	a.updateBtn.SetEnabled(h.Registered && h.ExeExists && candidate != "")
	a.uninstallBtn.SetEnabled(h.Registered)
	a.startBtn.SetEnabled(h.Registered && !h.Running)
	a.stopBtn.SetEnabled(h.Registered && h.Running)
	a.restartBtn.SetEnabled(h.Registered && h.Running)
}

// updateWarnings surfaces configuration problems and, most importantly, the
// case where the agent would kill this console.
func (a *app) updateWarnings() {
	var msgs []string

	selfAtRisk := a.hlth.Mode == "whitelist" && !a.selfProtected()
	if selfAtRisk {
		msgs = append(msgs,
			"WARNING: whitelist mode is active and "+selfProcessName()+" is not protected - the agent will close this window.")
	}
	msgs = append(msgs, a.hlth.Warnings...)

	a.warnLabel.SetText(strings.Join(msgs, "  •  "))
	a.selfFixBtn.SetVisible(selfAtRisk)
}

// selfProtected reports whether this console survives whitelist enforcement,
// either through the file or through the agent's builtin list.
func (a *app) selfProtected() bool {
	name := selfProcessName()
	return a.wl[name] || isBuiltinProtected(name)
}

// rebuild recomputes both tables from the in-memory state.
func (a *app) rebuild() {
	wlNames := a.whitelistNames()

	wlRows := make([]row, 0, len(wlNames))
	for _, n := range wlNames {
		var parts []string
		if a.running[n] {
			parts = append(parts, "running")
		}
		if isBuiltinProtected(n) {
			parts = append(parts, "protected without the file")
		}
		wlRows = append(wlRows, row{Name: n, Status: strings.Join(parts, ", ")})
	}
	a.wlModel.SetRows(wlRows)

	procRows := make([]row, 0, len(a.running))
	for n := range a.running {
		switch {
		case a.wl[n]:
			procRows = append(procRows, row{Name: n, Status: "in whitelist.txt"})
		case isBuiltinProtected(n):
			procRows = append(procRows, row{Name: n, Status: "built-in protection"})
		default:
			procRows = append(procRows, row{Name: n, Status: "will be closed", Killable: true})
		}
	}
	// Processes the agent would kill are the ones worth looking at, so put them
	// first regardless of alphabetical order.
	sort.Slice(procRows, func(i, j int) bool {
		if procRows[i].Killable != procRows[j].Killable {
			return procRows[i].Killable
		}
		return procRows[i].Name < procRows[j].Name
	})
	a.procModel.SetRows(procRows)

	a.wlCountLabel.SetText(fmt.Sprintf("total: %d", len(wlNames)))
}

func (a *app) whitelistNames() []string {
	names := make([]string, 0, len(a.wl))
	for n := range a.wl {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// --- whitelist actions ---

func (a *app) addNames(names []string) {
	added := 0
	for _, n := range names {
		n = normalizeName(n)
		if n == "" || a.wl[n] {
			continue
		}
		a.wl[n] = true
		added++
	}
	if added > 0 {
		a.setDirty(true)
		a.rebuild()
		a.updateWarnings()
	}
	a.setStatus(fmt.Sprintf("Added: %d.", added))
}

func (a *app) onAddSelected() {
	names := a.procModel.namesAt(a.procTable.SelectedIndexes())
	if len(names) == 0 {
		a.setStatus("Nothing selected in the process list.")
		return
	}
	a.addNames(names)
}

func (a *app) onRemoveSelected() {
	names := a.wlModel.namesAt(a.wlTable.SelectedIndexes())
	if len(names) == 0 {
		a.setStatus("Nothing selected in the allowed list.")
		return
	}

	for _, n := range names {
		delete(a.wl, n)
	}
	a.setDirty(true)
	a.rebuild()
	a.updateWarnings()
	a.setStatus(fmt.Sprintf("Removed: %d.", len(names)))
}

func (a *app) onAddManual() {
	text, ok := inputDialog(a.mw, "Add a program",
		"Process name exactly as Task Manager shows it:")
	if !ok {
		return
	}
	a.addNames(strings.Split(text, ";"))
}

func (a *app) onSave() {
	if !a.saveToDisk() {
		return
	}
	if walk.MsgBox(a.mw, "Saved",
		"whitelist.txt has been saved; the previous version is in whitelist.txt.bak.\n\n"+
			"The agent reads this file only at startup, so the changes take effect "+
			"after the service is restarted.\n\nRestart it now?",
		walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) == walk.DlgCmdYes {
		a.onRestart()
	}
}

func (a *app) saveToDisk() bool {
	names := a.whitelistNames()
	if err := saveWhitelist(a.whitelistPath(), names); err != nil {
		a.errBox("Could not save whitelist.txt", err)
		return false
	}
	a.setDirty(false)
	a.setStatus(fmt.Sprintf("Saved %d entries to %s", len(names), a.whitelistPath()))
	return true
}

func (a *app) onReload() {
	if a.dirty && walk.MsgBox(a.mw, "Unsaved changes",
		"Changes are not saved. Reload the file from disk and lose them?",
		walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) != walk.DlgCmdYes {
		return
	}
	a.reloadFromDisk()
	a.refreshAll()
}

// --- lifecycle actions ---

func (a *app) onInstall() {
	opts, ok := a.installDialog()
	if !ok {
		return
	}

	a.runLongTask("Install", func(progress func(string)) error {
		return performInstall(opts, progress)
	}, func(err error) {
		if err != nil {
			a.errBox("Installation failed", err)
			return
		}
		a.dir = opts.TargetDir
		a.dirManual = false
		a.reloadFromDisk()
		a.setStatus("The agent has been installed and started.")
	})
}

func (a *app) onUpdate() {
	candidate := findAgentExe(guiDir(), osArch())
	if candidate == "" {
		a.setStatus("No agent executable found next to Guardian.")
		return
	}

	info, err := inspectUpdate(candidate, a.hlth.ExePath)
	if err != nil {
		a.errBox("Could not compare versions", err)
		return
	}

	if info.Identical {
		walk.MsgBox(a.mw, "No update needed",
			"The installed agent is already identical to the new one:\n\n"+info.Installed.Describe(),
			walk.MsgBoxIconInformation)
		return
	}

	if info.ArchChange {
		walk.MsgBox(a.mw, "Reinstall required",
			fmt.Sprintf("The agent architecture changes: %s -> %s.\n\n"+
				"The service registration points at the previous file name, so replacing "+
				"it in place will not work. Use Uninstall, then Install.",
				filepath.Base(info.Installed.Path), filepath.Base(info.Source.Path)),
			walk.MsgBoxIconWarning)
		return
	}

	if walk.MsgBox(a.mw, "Update agent",
		"Installed:\n  "+info.Installed.Describe()+
			"\n\nNew:\n  "+info.Source.Describe()+
			"\n\nThe service will be stopped while the file is replaced. Continue?",
		walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) != walk.DlgCmdYes {
		return
	}

	a.runLongTask("Update", func(progress func(string)) error {
		return performUpdate(info, progress)
	}, func(err error) {
		if err != nil {
			a.errBox("Update failed", err)
			return
		}
		a.setStatus("The agent has been updated and the service started.")
	})
}

func (a *app) onUninstall() {
	if err := validateDeletionTarget(a.dir); err != nil {
		a.errBox("Cannot uninstall", err)
		return
	}

	keep, ok := uninstallDialog(a.mw, a.dir, countFiles(a.dir))
	if !ok {
		return
	}

	a.runLongTask("Uninstall", func(progress func(string)) error {
		backup, err := performUninstall(a.dir, keep, progress)
		if err == nil && backup != "" {
			progress("configuration preserved in " + backup)
		}
		return err
	}, func(err error) {
		if err != nil {
			a.errBox("Uninstall failed", err)
			return
		}
		a.dirManual = false
		a.reloadFromDisk()
		a.setStatus("The agent has been removed.")
	})
}

func (a *app) onStart() {
	a.runLongTask("Start service", func(func(string)) error {
		return startService()
	}, func(err error) {
		if err != nil {
			a.errBox("Could not start the service", err)
			return
		}
		a.setStatus("The service is running.")
	})
}

func (a *app) onStop() {
	a.runLongTask("Stop service", func(func(string)) error {
		return stopService()
	}, func(err error) {
		if err != nil {
			a.errBox("Could not stop the service", err)
			return
		}
		a.setStatus("The service is stopped.")
	})
}

func (a *app) onRestart() {
	a.runLongTask("Restart service", func(func(string)) error {
		return restartService()
	}, func(err error) {
		if err != nil {
			a.errBox("Could not restart the service", err)
			return
		}
		a.setStatus("The service has been restarted and the agent re-read whitelist.txt.")
	})
}

// runLongTask runs work off the UI thread with the window disabled, then
// re-enters the UI thread to report and refresh.
func (a *app) runLongTask(name string, work func(progress func(string)) error, done func(error)) {
	a.setStatus(name + "…")
	a.mw.SetEnabled(false)

	progress := func(msg string) {
		a.mw.Synchronize(func() { a.setStatus(name + ": " + msg) })
	}

	go func() {
		err := work(progress)
		a.mw.Synchronize(func() {
			a.mw.SetEnabled(true)
			done(err)
			a.refreshAll()
		})
	}()
}

func (a *app) refreshEventLog() {
	raw, err := readAgentEventLog(30)
	if err != nil {
		a.logEdit.SetText("Event log unavailable: " + err.Error())
		return
	}
	if raw == "" {
		a.logEdit.SetText("No entries. The agent writes to the log only while the service runs.")
		return
	}

	lines := summarizeEventLog(raw)
	if len(lines) == 0 {
		a.logEdit.SetText(raw)
		return
	}
	a.logEdit.SetText(strings.Join(lines, "\r\n"))
}

// --- folder ---

func (a *app) onOpenFolder() {
	if err := openInExplorer(a.dir); err != nil {
		a.errBox("Could not open the folder", err)
	}
}

func (a *app) onPickFolder() {
	dlg := new(walk.FileDialog)
	dlg.Title = "Agent installation folder"
	dlg.FilePath = a.dir

	ok, err := dlg.ShowBrowseFolder(a.mw)
	if err != nil || !ok {
		return
	}

	a.dir = dlg.FilePath
	a.dirManual = true
	a.dirSource = "chosen manually"
	a.dirLabel.SetText(a.dirText())
	a.reloadFromDisk()
	a.refreshAll()
}

// --- helpers ---

func (a *app) setDirty(v bool) {
	a.dirty = v
	if v {
		a.mw.SetTitle(appTitle + " *")
	} else {
		a.mw.SetTitle(appTitle)
	}
}

func (a *app) setStatus(s string) { a.statusLabel.SetText(s) }

func (a *app) errBox(title string, err error) {
	walk.MsgBox(a.mw, title, err.Error(), walk.MsgBoxIconError)
	a.setStatus(title + ": " + err.Error())
}

// dirText shows where the path came from, so it is obvious whether the tool is
// looking at the installed service or at a guessed default.
func (a *app) dirText() string {
	return a.dir + "   (" + a.dirSource + ")"
}
