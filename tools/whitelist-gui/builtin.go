package main

import "strings"

// builtinProtected mirrors the hardcoded list in isSystemProcess() in
// agent/main.go. These processes are never killed regardless of whitelist.txt,
// so the UI marks them separately: adding them to the file changes nothing.
//
// Keep in sync with agent/main.go if that list changes.
var builtinProtected = map[string]bool{
	"system": true, "system idle process": true, "secure system": true, "registry": true,
	"smss.exe": true, "csrss.exe": true, "wininit.exe": true, "winlogon.exe": true,
	"services.exe": true, "lsass.exe": true, "lsaiso.exe": true,

	"svchost.exe": true, "dwm.exe": true, "fontdrvhost.exe": true, "conhost.exe": true,
	"wudfhost.exe": true, "wmiprvse.exe": true, "dllhost.exe": true, "spoolsv.exe": true,
	"audiodg.exe": true, "dashost.exe": true, "searchindexer.exe": true,
	"searchprotocolhost.exe": true, "searchfilterhost.exe": true,

	"explorer.exe": true, "sihost.exe": true, "taskhostw.exe": true,
	"runtimebroker.exe": true, "applicationframehost.exe": true,
	"shellexperiencehost.exe": true, "startmenuexperiencehost.exe": true,
	"searchhost.exe": true, "searchui.exe": true, "searchapp.exe": true,
	"textinputhost.exe": true, "lockapp.exe": true, "widgets.exe": true,
	"widgetservice.exe": true, "comppkgsrv.exe": true,

	"userinit.exe": true, "logonui.exe": true,

	"securityhealthservice.exe": true, "securityhealthsystray.exe": true,
	"securityhealthhost.exe": true, "smartscreen.exe": true,
	"msmpeng.exe": true, "nissrv.exe": true, "mpcmdrun.exe": true,

	"ctfmon.exe": true, "tabletinputservice.exe": true,

	"lsm.exe": true, "networkservice.exe": true, "localservice.exe": true,

	"tasklist.exe": true, "taskmgr.exe": true, "cmd.exe": true,
	"powershell.exe": true, "pwsh.exe": true,
	"windowsterminal.exe": true, "wt.exe": true, "openssh.exe": true, "sshd.exe": true,

	"procsentinel-agent64.exe": true, "procsentinel-agent32.exe": true,
	"guardian.exe": true,
}

func isBuiltinProtected(name string) bool {
	return builtinProtected[strings.ToLower(name)]
}
