# Guardian Console

A Windows GUI for managing the ProcSentinel agent on a single machine:
diagnostics, install / update / uninstall, and editing the local list of allowed
programs.

Specification: [`specs/whitelist-gui.md`](../../specs/whitelist-gui.md).

## What it does

**"Allowed programs" tab** — edits `whitelist.txt`: the allowed list on the
left, running processes on the right, add/remove, filters. Processes the agent
would close in whitelist mode sort to the top and are labelled separately from
those covered by the agent's built-in protected list.

**"Status and installation" tab** — diagnostics (service, executable,
configuration, freshness of the last sync, server-supplied mode), the Install /
Update agent / Uninstall buttons, service control, and a tail of the
`ProcSentinelAgent` event log.

## Where the agent's files live

`C:\Windows\System32\ProcSentinel\agent\` by default, but the path is read from
the registry — `HKLM\SYSTEM\CurrentControlSet\Services\ProcSentinelAgent\ImagePath` —
so a non-standard installation is found too.

| File | Role |
|---|---|
| `whitelist.txt` | the local list of allowed programs; the main thing this tool edits |
| `sync.json` | cached server response; its mtime is the liveness signal |
| `.env` | agent configuration; written only during install |

## Agent behaviour worth knowing

* `whitelist.txt` has any effect **only** in `whitelist` mode. In `blacklist`
  and `free` it is not consulted at all — the current mode is shown in the
  diagnostics.
* The agent reads the file **once, at service startup**. After saving, the
  console offers to restart the service.
* Besides the file there is a built-in list of protected processes compiled into
  the agent. Those entries are labelled "built-in protection" — adding them to
  the file achieves nothing.

## Self-protection

In whitelist mode the agent closes everything not on its lists, and it runs as
LocalSystem — so it can close this console too. That is why `guardian.exe` is
in the agent's `isSystemProcess()`, and why the console checks itself at startup
and offers to add itself to `whitelist.txt` when running against an older
agent.

The price: any process named `guardian.exe` becomes unkillable. The agent's
protection is name-based throughout, so this is not a new class of weakness, but
a short name makes the bypass easier. The real fix is matching on the full
process path — see *Cost of a Generic Name* in the specification.

## Privileges

Administrator rights are required: the folder is under `System32` and service
control needs an elevated SCM handle. A `requireAdministrator` manifest is
embedded, so UAC prompts at launch.

A 32-bit build on 64-bit Windows rewrites `System32` → `Sysnative`; without it
the file system redirector would send it to `SysWOW64`, where the agent folder
does not exist at all.

## Build

```powershell
./tools/whitelist-gui/build.ps1   # -> dist/agent/Guardian.exe
```

`Guardian.exe` is built as 32-bit on purpose: a 64-bit binary cannot start on
32-bit Windows, while a 32-bit one runs on both through WOW64. The OS
architecture is detected at run time and the matching agent is installed.

There is no separate native build. One artifact means one thing to ship, and
one fewer guessable name protected in the agent.

A single file with no dependencies; CGO is not needed. The script generates
`rsrc_windows_<arch>.syso` from the manifest and the icon, then deletes it after
the build.

## Icon

The mark is the green Guardian shield, kept here as `guardian-mark.png`
(220×220). Regenerate the `.ico` after changing it:

```powershell
cd tools/mkico
go run . ../whitelist-gui/guardian-mark.png ../whitelist-gui/guardian.ico
```

`tools/mkico` is a separate module, so `golang.org/x/image` — needed only to
decode WebP sources — stays out of the console's own dependencies.

Note that there are **two** icons to satisfy. The one embedded in the PE
resources is what Explorer and the taskbar use; the window's own title bar icon
must be set explicitly, or it stays on the Windows default. `icon.go` handles
both: it reads the embedded resource, and falls back to a `go:embed`ed copy of
`guardian-mark.png` for builds without a resource section.

## Tests

```powershell
./tools/whitelist-gui/test.ps1            # everything, including the window test
./tools/whitelist-gui/test.ps1 -SkipGui   # logic only
```

A bare `go test ./...` works too, but the window test skips itself: it needs the
Common Controls 6.0 manifest that `test.ps1` supplies. See *Why Two Manifests*
in the specification.

## Maintenance note

Two places duplicate logic and must be kept in sync by hand:

* `builtin.go` — a copy of the hardcoded process list from `agent/main.go`.
* The install flow — the same procedure as
  `dist/agent/bin/Install-Agent*.ps1`. Unifying them was deliberately rejected;
  see *Decisions* in the specification.
