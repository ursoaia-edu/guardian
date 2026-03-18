package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

// inputDialog asks for a single line of text; walk has no built-in input box.
func inputDialog(owner walk.Form, title, prompt string) (string, bool) {
	var dlg *walk.Dialog
	var edit *walk.LineEdit
	var okBtn, cancelBtn *walk.PushButton
	var result string

	cmd, err := Dialog{
		AssignTo:      &dlg,
		Title:         title,
		MinSize:       Size{Width: 420, Height: 160},
		Layout:        VBox{},
		DefaultButton: &okBtn,
		CancelButton:  &cancelBtn,
		Children: []Widget{
			Label{Text: prompt},
			LineEdit{AssignTo: &edit, CueBanner: "for example: notepad.exe"},
			Label{Text: "Several names may be listed, separated by semicolons."},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					HSpacer{},
					PushButton{AssignTo: &okBtn, Text: "Add", OnClicked: func() {
						result = edit.Text()
						dlg.Accept()
					}},
					PushButton{AssignTo: &cancelBtn, Text: "Cancel", OnClicked: func() {
						dlg.Cancel()
					}},
				},
			},
		},
	}.Run(owner)

	if err != nil || cmd != walk.DlgCmdOK {
		return "", false
	}
	return result, true
}

// isPlaceholder recognises the dummy values shipped in agent.env, so the
// install dialog starts empty rather than prefilled with "your_token_here".
func isPlaceholder(v string) bool {
	return strings.HasPrefix(strings.ToLower(v), "your_")
}

// prefillEnv builds the starting configuration: the template shipped next to
// the console, overridden by an existing installation's own settings.
func prefillEnv(installDirPath string) *envConfig {
	env := newEnvConfig()

	merge := func(src *envConfig) {
		if src == nil {
			return
		}
		for _, k := range knownEnvOrder {
			if v := src.Get(k); v != "" && !isPlaceholder(v) {
				env.Set(k, v)
			}
		}
	}

	if tpl, err := loadEnvConfig(templateEnvPath()); err == nil {
		merge(tpl)
	}
	if cur, err := loadEnvConfig(filepath.Join(installDirPath, envFileName)); err == nil {
		merge(cur)
	}
	return env
}

// installDialog collects everything performInstall needs. Validation runs
// before the dialog closes, so an invalid entry can be corrected in place.
func (a *app) installDialog() (installOptions, bool) {
	arch := osArch()
	env := prefillEnv(a.dir)

	targetDir := a.dir
	if !a.dirManual {
		targetDir = defaultInstallDir
	}

	var dlg *walk.Dialog
	var okBtn, cancelBtn *walk.PushButton
	var srcEdit, dirEdit, serverEdit, tokenEdit, intervalEdit, identityEdit *walk.LineEdit

	var result installOptions

	cmd, err := Dialog{
		AssignTo:      &dlg,
		Title:         "Install the ProcSentinel agent",
		MinSize:       Size{Width: 620, Height: 340},
		Layout:        VBox{},
		DefaultButton: &okBtn,
		CancelButton:  &cancelBtn,
		Children: []Widget{
			Label{Text: fmt.Sprintf("Detected Windows architecture: %s-bit", archLabel(arch))},
			Composite{
				Layout: Grid{Columns: 3},
				Children: []Widget{
					Label{Text: "Agent executable:"},
					LineEdit{AssignTo: &srcEdit, Text: findAgentExe(guiDir(), arch)},
					PushButton{Text: "Browse...", OnClicked: func() {
						fd := new(walk.FileDialog)
						fd.Title = "Select " + agentExeName(arch)
						fd.Filter = "Executable files (*.exe)|*.exe"
						fd.InitialDirPath = guiDir()
						if ok, err := fd.ShowOpen(dlg); err == nil && ok {
							srcEdit.SetText(fd.FilePath)
						}
					}},

					Label{Text: "Installation folder:"},
					LineEdit{AssignTo: &dirEdit, Text: targetDir},
					PushButton{Text: "Browse...", OnClicked: func() {
						fd := new(walk.FileDialog)
						fd.Title = "Installation folder"
						fd.FilePath = dirEdit.Text()
						if ok, err := fd.ShowBrowseFolder(dlg); err == nil && ok {
							dirEdit.SetText(fd.FilePath)
						}
					}},

					Label{Text: "SERVER_ADDRESS:"},
					LineEdit{AssignTo: &serverEdit, Text: env.Get("SERVER_ADDRESS"), CueBanner: "http://192.168.1.10:8080"},
					Label{Text: ""},

					Label{Text: "TOKEN:"},
					LineEdit{AssignTo: &tokenEdit, Text: env.Get("TOKEN")},
					Label{Text: ""},

					Label{Text: "CHECK_INTERVAL, s:"},
					LineEdit{AssignTo: &intervalEdit, Text: env.Get("CHECK_INTERVAL"), CueBanner: "20"},
					Label{Text: ""},

					Label{Text: "IDENTITY:"},
					LineEdit{AssignTo: &identityEdit, Text: env.Get("IDENTITY"), CueBanner: "optional, integer"},
					Label{Text: ""},
				},
			},
			Label{Text: "An existing installation will be stopped and replaced."},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					HSpacer{},
					PushButton{AssignTo: &okBtn, Text: "Install", OnClicked: func() {
						cfg := newEnvConfig()
						cfg.Set("SERVER_ADDRESS", strings.TrimSpace(serverEdit.Text()))
						cfg.Set("TOKEN", strings.TrimSpace(tokenEdit.Text()))
						cfg.Set("CHECK_INTERVAL", strings.TrimSpace(intervalEdit.Text()))
						cfg.Set("IDENTITY", strings.TrimSpace(identityEdit.Text()))

						candidate := installOptions{
							SourceExe: strings.TrimSpace(srcEdit.Text()),
							TargetDir: strings.TrimSpace(dirEdit.Text()),
							Env:       cfg,
						}
						if err := validateInstallOptions(candidate); err != nil {
							walk.MsgBox(dlg, "Check the values", err.Error(), walk.MsgBoxIconWarning)
							return
						}

						result = candidate
						dlg.Accept()
					}},
					PushButton{AssignTo: &cancelBtn, Text: "Cancel", OnClicked: func() { dlg.Cancel() }},
				},
			},
		},
	}.Run(a.mw)

	if err != nil || cmd != walk.DlgCmdOK {
		return installOptions{}, false
	}
	return result, true
}

func archLabel(arch string) string {
	if arch == "32" {
		return "32"
	}
	return "64"
}

// uninstallDialog confirms removal and returns whether the configuration
// should be preserved. Preserving is the default: a hand-curated whitelist is
// real work and should not vanish on a button press.
func uninstallDialog(owner walk.Form, dir string, fileCount int) (keepConfig bool, ok bool) {
	var dlg *walk.Dialog
	var okBtn, cancelBtn *walk.PushButton
	var keepBox *walk.CheckBox

	cmd, err := Dialog{
		AssignTo:      &dlg,
		Title:         "Uninstall the agent",
		MinSize:       Size{Width: 560, Height: 230},
		Layout:        VBox{},
		DefaultButton: &cancelBtn,
		CancelButton:  &cancelBtn,
		Children: []Widget{
			Label{Text: "The ProcSentinelAgent service will be stopped and deregistered."},
			Label{Text: fmt.Sprintf("The folder and everything in it will be deleted (%d files):", fileCount)},
			Label{Text: "    " + dir},
			CheckBox{
				AssignTo: &keepBox,
				Text:     "Preserve whitelist.txt and .env in a backup",
				Checked:  true,
			},
			Label{Text: "The backup is written to %ProgramData%\\ProcSentinel\\backup."},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					HSpacer{},
					PushButton{AssignTo: &okBtn, Text: "Uninstall", OnClicked: func() {
						keepConfig = keepBox.Checked()
						dlg.Accept()
					}},
					PushButton{AssignTo: &cancelBtn, Text: "Cancel", OnClicked: func() { dlg.Cancel() }},
				},
			},
		},
	}.Run(owner)

	if err != nil || cmd != walk.DlgCmdOK {
		return false, false
	}
	return keepConfig, true
}
