# Guardian Console Specification

## Overview

Guardian Console (`tools/whitelist-gui`, shipped as `Guardian.exe`) is a Windows GUI utility for managing the ProcSentinel agent on a single machine. It covers two jobs:

1. **Whitelist editing** — inspecting and editing the agent's local list of allowed programs, `whitelist.txt`.
2. **Agent lifecycle** — detecting whether the agent is installed and healthy, and installing, updating, starting, stopping, restarting or removing it.

It is an operator tool, not a runtime component: it never talks to the ProcSentinel server and never kills processes.

Built with Go + [`github.com/lxn/walk`](https://github.com/lxn/walk) (native Win32, no CGO). Output is a single self-contained executable with an embedded `requireAdministrator` manifest.

The user interface is in English throughout.

`Guardian.exe` is built for **x86** on purpose, which makes it the universal installer: see *Universal Build*.

## Relationship to the Agent

`whitelist.txt` is consumed by the agent's `loadWhitelist()` / `isSystemProcess()` in `agent/main.go`. Three properties of that code define the tool's behavior and must be reflected in the UI:

1. **Mode-dependent.** The file is only consulted when the server-supplied mode is `whitelist`. In `blacklist` and `free` modes it has no effect at all. The tool therefore surfaces the current mode from `sync.json`.
2. **Read once.** `initWhitelist()` runs at service startup and the file is never re-read. Edits do not take effect until `ProcSentinelAgent` is restarted; the tool offers to do that after every save.
3. **Not the only protection.** `isSystemProcess()` also consults a hardcoded list of system-critical processes. Entries already covered by that list gain nothing from being in the file, and the UI labels them distinctly.

Outside the install and uninstall flows the tool treats the agent's own state as read-only: it never modifies `sync.json`, never touches the server's application list, and never rewrites `.env` during ordinary whitelist editing.

## File Structure

| File                      | Purpose                                                                 |
|---------------------------|-------------------------------------------------------------------------|
| `main.go`                 | Window construction, event handlers, data flow between disk and tables   |
| `paths.go`                | Install directory discovery, `ImagePath` parsing, WOW64 redirection fix  |
| `whitelist.go`            | `whitelist.txt` read/write, backup, manual-entry normalization           |
| `syncstate.go`            | Read-only parsing of the agent's `sync.json`                            |
| `procs.go`                | Running-process enumeration via `tasklist`, CSV name extraction          |
| `service.go`              | `ProcSentinelAgent` state query, stop/start/restart, elevation check     |
| `health.go`               | Installation state model: combines service, filesystem and config checks |
| `install.go`              | Agent source discovery, config collection, install orchestration         |
| `update.go`               | In-place executable replacement, hash comparison, rollback copy          |
| `uninstall.go`            | Uninstall orchestration, target-path safety validation, config backup    |
| `envfile.go`              | `.env` read/write for the agent configuration                            |
| `builtin.go`              | Mirror of the agent's hardcoded protected-process list                   |
| `model.go`                | Filterable `walk.TableModel` shared by both tables                       |
| `explorer.go`             | "Open folder" helper                                                     |
| `guardian-mark.png`       | Source image for the icon, and the embedded window-icon fallback          |
| `guardian.ico`            | Application icon, generated from `guardian-mark.png` by `tools/mkico`    |
| `icon.go`                 | Window icon loading: PE resource first, embedded PNG as fallback         |
| `whitelist-gui.manifest`  | Release manifest: UAC elevation + Common Controls 6.0 + DPI awareness    |
| `whitelist-gui-test.manifest` | Test manifest: same controls dependency, `asInvoker` instead of admin |
| `build.ps1`               | Manifest resource generation, build, artifact cleanup                    |
| `test.ps1`                | Test-manifest resource generation, test run, artifact cleanup            |
| `parse_test.go`           | Unit tests for parsing, file I/O, and model filtering                    |
| `window_test.go`          | Opt-in GUI smoke test (`PS_GUI_TEST=1`)                                  |

## Installation Discovery

The agent path is resolved at startup, in order:

1. `HKLM\SYSTEM\CurrentControlSet\Services\ProcSentinelAgent` → `ImagePath`, stripped of quotes and trailing arguments, then `filepath.Dir()`. Source shown as *from the service registry*.
2. Fallback to `C:\Windows\System32\ProcSentinel\agent` (the path hardcoded in `Install-Agent32.ps1` / `Install-Agent64.ps1`). Source shown as *default path, service not found*.
3. Manual override via the "Change..." folder picker. Source shown as *chosen manually*.

The resolved path and its source are always visible in the info panel, so the operator can tell whether the tool is looking at a real installation or a guess.

### WOW64 Redirection

A 32-bit build running on 64-bit Windows has `System32` silently redirected to `SysWOW64` by the file system redirector, which would mean editing the wrong (or a non-existent) file. `fixRedirection()` rewrites a leading `%WINDIR%\System32` to `%WINDIR%\Sysnative` when `GOARCH == "386"` and `PROCESSOR_ARCHITEW6432` is set. The 64-bit build is unaffected.

## Universal Build

`Guardian.exe` is a 32-bit binary. This is not an oversight -- it is what makes one file work everywhere:

* A 64-bit executable cannot start at all on 32-bit Windows.
* A 32-bit executable runs on both, through WOW64 on 64-bit systems.

So the shipped console is x86, detects the **operating system's** architecture at run time, and installs the matching agent.

There is deliberately **no second, native artifact**. One shipped binary means one thing to distribute, and one fewer guessable name in the agent's protected process list -- see *Cost of a Generic Name*.

### Consequences

| Concern | Handling |
|---|---|
| File system redirection | A 32-bit process reading `System32` is silently redirected to `SysWOW64`, where the agent folder does not exist. `fixRedirection()` rewrites the prefix to `Sysnative`. Verified experimentally: without it `ReadDir` on the agent folder fails with "cannot find the path"; with it the folder is listed correctly. |
| Architecture detection | `osArch()` reads `PROCESSOR_ARCHITEW6432` first, which is set only inside WOW64, and falls back to `PROCESSOR_ARCHITECTURE`. It must never report the console's own `runtime.GOARCH`, or a 32-bit console would install a 32-bit agent onto 64-bit Windows. |
| Service image path | The console has to launch the agent through the `Sysnative` alias, because a 32-bit process cannot execute anything under the real `System32`. The agent then registers itself using `os.Executable()`, which may record that alias -- and `Sysnative` does not exist for the 64-bit service host, so such a registration would never start. `normalizeServiceImagePath()` reads the registration back after `-install` and rewrites `\Sysnative\` to `\System32\` when needed. |
| Registry | `HKLM\SYSTEM\CurrentControlSet\Services` is a shared hive with no WOW64 reflection, so the `ImagePath` lookup works unchanged from a 32-bit process. |
| Alias containment | `Sysnative` is applied by `ioPath()` at the moment of a file operation and nowhere else. Every path the program stores, compares, displays or hands to another process stays canonical `System32`. Leaking the alias outward breaks real things: the 64-bit Explorer cannot resolve it, so "Open folder" would fail, and the path shown in the banner and the uninstall dialog would be one the operator cannot paste anywhere. |

## Installation State Detection

"Installed" is not a boolean. A machine can have the service registered while the directory holds nothing but the executable — no `.env`, no `whitelist.txt`, no `sync.json` — which is registered but non-functional. The tool therefore evaluates independent signals and folds them into one reported state.

### Signals

| Signal | Source | Meaning when absent or wrong |
|---|---|---|
| Service registered | `mgr.OpenService(ProcSentinelAgent)` | Not installed |
| Service state | `Service.Query()` | Installed but not enforcing |
| Start type | `Service.Config().StartType` | Will not come back after reboot |
| Executable present | `Stat(ImagePath)` | Broken install: service points at a missing file |
| `.env` present, `SERVER_ADDRESS` and `TOKEN` set | parse | Agent falls back to `http://localhost:8080` and never syncs |
| `sync.json` present | `Stat` | No successful sync has ever happened |
| `sync.json` fresh | mtime vs `3 × CHECK_INTERVAL` | Server unreachable, wrong token, or wrong identity |
| `whitelist.txt` present | `Stat` | Agent has not completed a first run |

`sync.json` mtime is the load-bearing liveness signal. The agent rewrites the file on every successful poll, so a stale file proves the service is running but not reaching the server — a distinction "service is Running" cannot make on its own.

### Reported States

| State | Condition |
|---|---|
| `Not installed` | Service not registered |
| `Installation is broken` | Registered, but executable missing at `ImagePath` |
| `Installed, stopped` | Registered, executable present, service not running |
| `Running, no contact with the server` | Running, but `sync.json` missing or stale |
| `Running` | Running with a fresh `sync.json` |

Configuration warnings (missing `.env`, missing `TOKEN`, missing `whitelist.txt`, non-automatic start type) are reported alongside the state rather than folded into it, since any of them can accompany any state.

## Agent Lifecycle Operations

Implemented natively in Go rather than by invoking the PowerShell installers: `Install-Agent64.ps1` is interactive (`Read-Host` for `IDENTITY`), which cannot work from a GUI, and its outcome is only observable through console text and `$LASTEXITCODE`.

One deliberate exception: **service registration and deregistration stay delegated to the agent binary** via `procsentinel-agent64.exe -install` / `-remove`. The agent already owns its service configuration — display name, description, `StartAutomatic`, and the event-log source created by `eventlog.InstallAsEventCreate` — and duplicating that in the GUI would be a second source of truth for the agent's own identity. Start and stop, by contrast, go directly through the SCM, since they carry no configuration.

### Locating the Agent Executable

Resolved relative to the GUI executable's own directory, matching the shipped `dist/agent/` layout:

| Order | Path relative to the GUI executable |
|---|---|
| 1 | `bin\agent\procsentinel-agent{64,32}.exe` |
| 2 | `agent\procsentinel-agent{64,32}.exe` |
| 3 | `procsentinel-agent{64,32}.exe` |
| 4 | File picker, if none of the above matched |

Architecture is chosen from the OS, not from the GUI's own build: `PROCESSOR_ARCHITECTURE` / `PROCESSOR_ARCHITEW6432`, the same rule `Install-Guardian.bat` uses.

### Install

1. Verify elevation. If the service already exists, confirm a reinstall.
2. Locate the source executable as above.
3. Collect configuration in a dialog: `SERVER_ADDRESS`, `TOKEN`, `CHECK_INTERVAL`, `IDENTITY`. Prefilled from `agent.env` next to the GUI, then overridden by the existing installation's `.env` when reinstalling.
4. Validate: `SERVER_ADDRESS` non-empty and parseable as a URL; `IDENTITY` empty or an integer; `CHECK_INTERVAL` empty or a positive integer.
5. Create the installation directory.
6. Stop and deregister any existing service (`-remove`).
7. Copy the executable.
8. Write `.env`.
9. Register the service (`-install`).
10. Verify the registered image path and repair it if it points at `Sysnative` (see *Universal Build*).
11. Start the service through the SCM.
12. Wait for the agent to generate `whitelist.txt` (bounded poll), then load it into the editor.

Step 12 matters for the UX: on a fresh install the file does not exist yet, and without the wait the editor would show an empty list immediately after a successful install.

### Update

Replaces the agent executable while preserving `.env`, `whitelist.txt` and `sync.json`. Available only when the service is already registered.

1. Locate the candidate executable using the same search order as install.
2. Compare it with the installed one: size, mtime, and SHA-256. If the hashes match, report *No update needed* and stop — no service interruption for a no-op.
3. Show both sides (size, mtime, short hash) and require confirmation.
4. Copy the installed executable to `%ProgramData%\ProcSentinel\backup\<timestamp>\` so a bad update can be rolled back by hand.
5. Stop the service — the running executable is locked and cannot be overwritten.
6. Copy the new executable over the old one.
7. Start the service and re-evaluate state.

Service registration is **not** touched: `ImagePath` still points at the same file, so `-remove`/`-install` would only risk losing the event-log source for no gain.

The one exception is an architecture switch. If the candidate's filename differs from the registered `ImagePath` filename — `procsentinel-agent32.exe` replacing `procsentinel-agent64.exe` or the reverse — the registration points at a name that will no longer exist. That case is routed through the full install flow instead, reusing the existing `.env`.

SHA-256 is used because the agent exposes no version: `main.go` accepts only `-install`, `-remove`, `-start`, `-stop` and `-debug`. A hash comparison is the only reliable way to tell two builds apart. Adding a `-version` flag to the agent would make this friendlier and is worth doing separately.

### Uninstall

Default is to **preserve** `whitelist.txt` and `.env`, unlike `Uninstall-Agent64.ps1`, which removes the directory wholesale. A hand-curated whitelist is real work and should not be destroyed by a button press.

1. Confirmation dialog naming the exact directory and file count, with a "Preserve `whitelist.txt` and `.env`" checkbox, checked by default.
2. Validate the deletion target (below).
3. Stop the service.
4. Deregister the service (`-remove`).
5. Copy preserved files to `%ProgramData%\ProcSentinel\backup\<timestamp>\`, outside the directory about to be deleted.
6. Delete the installation directory recursively.
7. Re-evaluate state.

### Deletion Target Validation

The directory is derived from `ImagePath`, a registry value any administrator — or any malware with administrator rights — can rewrite. Recursively deleting `filepath.Dir(ImagePath)` unchecked is therefore unsafe. Deletion proceeds only if **all** hold:

- The directory contains `procsentinel-agent32.exe` or `procsentinel-agent64.exe`.
- The directory is not a drive root, `%WINDIR%`, `%WINDIR%\System32`, `%WINDIR%\SysWOW64`, `%ProgramFiles%`, `%ProgramFiles(x86)%`, `%ProgramData%`, or a user profile root.
- The path is at least three components deep.

A failed check aborts with an explanation naming the offending path; it is never downgraded to a warning the operator can click past.

## Self-Protection

In `whitelist` mode the agent kills every process absent from the server list, `whitelist.txt`, and its own builtin protected list. `Guardian.exe` is in none of them, so the service — running as LocalSystem, able to terminate elevated processes — will close this tool roughly one second after it opens, mid-edit.

Two mitigations, both required:

1. **Agent-side:** add `guardian.exe` to `isSystemProcess()` in `agent/main.go`, mirrored into `builtin.go`. Only the one shipped name is protected; every additional name would be another bypass for no benefit.
2. **Tool-side:** on startup and after each refresh, if the mode is `whitelist` and the tool's own process name is not protected, show a persistent warning with a one-click "Add Guardian to whitelist". This covers machines running an older agent that lacks the builtin entry.

#### Cost of a Generic Name

Protecting `guardian.exe` by name makes that name a bypass: any executable renamed to `guardian.exe` becomes unkillable in whitelist mode. This is not a new class of weakness — the agent compares process *names* throughout, so renaming a game to `notepad.exe` already defeats it — but a short, documented, guessable name lowers the bar considerably compared with the earlier `guardian-whitelist64.exe`.

The real fix is to match on the full executable path rather than the name, which would close the bypass for every whitelist entry at once. That requires replacing `tasklist` in the agent, since its CSV output carries no path. Tracked as a separate agent-side change, not a blocker for this tool.

## Files Accessed

| File                | Access | Notes                                                        |
|---------------------|--------|--------------------------------------------------------------|
In the agent's installation directory:

| File                | Access | Notes                                                        |
|---------------------|--------|--------------------------------------------------------------|
| `whitelist.txt`     | R/W    | The edit target                                              |
| `whitelist.txt.bak` | W      | Previous contents, overwritten on each save                  |
| `sync.json`         | R      | Mode, server application count, and liveness via mtime       |
| `.env`              | R/W    | Read for diagnostics; written only during install            |

Elsewhere:

| Path | Access | Notes |
|---|---|---|
| `<gui dir>\agent.env` | R | Configuration template, prefills the install dialog |
| `<gui dir>\bin\agent\*.exe` | R | Install source |
| `%ProgramData%\ProcSentinel\backup\<timestamp>\` | W | Preserved config on uninstall |

## whitelist.txt Format

### Reading
Matches the agent's parser exactly: split on `\n`, trim each line, skip blanks and lines starting with `#`, lowercase, de-duplicate. A missing file is not an error — the agent creates it on first run — and is reported as *missing* with the list left empty.

### Writing
Sorted ascending, lowercase, one name per line, CRLF terminated. CRLF is for Notepad's benefit; the agent's `TrimSpace` handles it either way. `#` comments present in a hand-edited file are **not preserved** across a save.

### Manual Entry Normalization
`normalizeName()` accepts what an operator is likely to paste: trims whitespace, strips surrounding quotes, lowercases, and reduces a full path to its base name (`C:\Windows\notepad.exe` → `notepad.exe`). A single input may list several names separated by `;`.

## UI Layout

Install and uninstall are rare and destructive; they do not belong next to the everyday editing buttons. The window is therefore split into two tabs, with the state banner shared above them.

```
┌──────────────────────────────────────────────────────────────────┐
│ State: <state>    Folder: <path> (<source>)         [Change...]  │
│ <warning banner: self-protection / config problems>  [Add Guard.]│
├──[ Allowed programs ]──[ Status and installation ]───────────────┤
│                                                                  │
│ ┌─ Allowed programs (whitelist.txt) ─┬─ Running processes ─────┐ │
│ │ [filter...]                        │ [filter...] [ ] only    │ │
│ │ ┌───────────┬────────────────────┐ │ ┌──────────┬──────────┐ │ │
│ │ │ Program   │ Status             │ │ │ Process  │ Status   │ │ │
│ │ └───────────┴────────────────────┘ │ └──────────┴──────────┘ │ │
│ │ [Add manually...] [Remove selected]│ [Refresh] [<- Add]      │ │
│ └────────────────────────────────────┴─────────────────────────┘ │
│ [Save] [Reload from disk] [Open folder]                          │
└──────────────────────────────────────────────────────────────────┘
<status line>
```

Second tab:

```
├──[ Allowed programs ]──[ Status and installation ]───────────────┤
│ ┌─ Diagnostics ────────────────────────────────────────────────┐ │
│ │ Service:            <state>, <start type>                    │ │
│ │ Executable:         <path> / not found                       │ │
│ │ Configuration:      SERVER_ADDRESS <set>, TOKEN <set>        │ │
│ │ Last sync:          <n>s ago / never                         │ │
│ │ Mode (from server): <mode>, <n> applications                 │ │
│ │ whitelist.txt:      <n> entries / missing                    │ │
│ └──────────────────────────────────────────────────────────────┘ │
│ [Install] [Update agent] [Uninstall] │ [Start] [Stop] [Restart]  │
│ ┌─ Agent event log ────────────────────────────────────────────┐ │
│ └──────────────────────────────────────────────────────────────┘ │
```

Buttons enable by state:

| Button | Enabled when |
|---|---|
| Install | Service not registered, or installation is broken |
| Update agent | Service registered **and** a candidate executable was found |
| Uninstall | Service registered |
| Start | Registered and not running |
| Stop / Restart | Running |

Long operations disable the window and run off the UI thread, re-entering through `mw.Synchronize()`.

The event-log pane reads the `ProcSentinelAgent` source the agent writes to via `eventlog`, which is the only place its sync failures and kill decisions are recorded.

Both tables support multi-selection; double-click is a shortcut for the panel's primary action (remove on the left, add on the right). Filters are case-insensitive substring matches. Selection indexes are mapped back to names through the filtered view, never through the underlying slice.

### Process Status Classification

Right-hand table, evaluated in this order:

| Condition                          | Label                   | Killable |
|------------------------------------|-------------------------|----------|
| Present in `whitelist.txt`         | `in whitelist.txt`      | no       |
| Present in the agent's builtin list| `built-in protection`   | no       |
| Neither                            | `will be closed`        | **yes**  |

Killable rows sort to the top regardless of alphabetical order — they are what the operator is looking for. The "only those the agent would close" checkbox filters to them exclusively.

Left-hand table annotates each entry with `running` when the process is currently running and `protected without the file` when the entry is redundant with the builtin list.

## Editing Semantics

Edits are held in memory until saved. The window title carries a `*` while unsaved changes exist. Closing with unsaved changes prompts Save / Discard / Cancel; a failed save cancels the close. "Reload from disk" prompts before discarding.

Saving writes the backup first, then the file, then offers a service restart.

## Service Control

| Operation | Behavior                                                                 |
|-----------|--------------------------------------------------------------------------|
| Query     | `mgr.Service.Query()`, mapped to a human-readable state label; failure to open the service reads as *not installed* |
| Stop      | Skips if already stopped; sends `svc.Stop` unless already stopping; waits up to 30 s |
| Start     | Skips if already running; waits up to 30 s                               |
| Restart   | Stop then Start                                                          |

Restart runs on a background goroutine with the window disabled, and re-enters the UI thread through `mw.Synchronize()`. State is polled at 300 ms intervals.

## Privileges

The embedded manifest requests `requireAdministrator`, so UAC elevates at launch. This is mandatory rather than optional: the default install directory is under `System32` and service control requires an elevated SCM handle.

`isElevated()` additionally probes `mgr.Connect()` at startup and warns if it fails — this covers a binary built without the manifest resource.

## Build

```powershell
./tools/whitelist-gui/build.ps1
```

| Parameter | Values   | Default      |
|-----------|----------|--------------|
| `-OutDir` | any path | `dist/agent` |

One target, one output: `dist/agent/Guardian.exe`, built for `386`, running on both 32- and 64-bit Windows.

### Application Icon

The mark is the green Guardian shield, kept in the repository as `guardian-mark.png` (220×220). Regenerate the `.ico` after changing it:

```powershell
cd tools/mkico
go run . ../whitelist-gui/guardian-mark.png ../whitelist-gui/guardian.ico
```

`tools/mkico` is a separate module so that `golang.org/x/image` — needed only to decode WebP sources — stays out of the console's dependencies. It centres the source on a square canvas, scales with Catmull-Rom, and writes sizes 16 through 256. Everything below 256 is a BMP/DIB entry and 256 is PNG: that is the layout every Windows shell understands, whereas PNG entries at small sizes are only reliable on newer ones.

### Two Icons, Two Mechanisms

Embedding `guardian.ico` in the PE resources is what makes **Explorer and the taskbar** show it. It does **not** set the icon of the window itself — walk leaves the title bar on the default icon unless told otherwise, which is why the top-left corner was blank at first.

`appIcon()` in `icon.go` covers both:

1. `walk.NewIconFromResourceId(2)` reads the embedded group icon, giving crisp hand-scaled sizes. Id 2 is what `rsrc` assigns when `build.ps1` passes the manifest and icon together: manifest 1, group icon 2, individual images 3 and up. Verify with `python tools/mkico/peres.py dist/agent/Guardian.exe`.
2. If there is no resource section — a bare `go build`, or the test binary — it falls back to the `go:embed`ed `guardian-mark.png`.

A missing icon never blocks startup; it is cosmetic and the window opens either way.

`TestAppIcon` covers this. It deliberately does not attach the icon to a window: `SetIcon` schedules a re-layout, and a test that disposes the form immediately afterwards races walk's layout goroutine and panics with "send on closed channel".

Built with `CGO_ENABLED=0` and `-ldflags "-s -w -H windowsgui"` — PE subsystem 2, so no console window appears.

### Manifest Resource Lifecycle

`build.ps1` generates `rsrc_windows_<arch>.syso` from `whitelist-gui.manifest` via `github.com/akavel/rsrc@v0.10.2` (requires network on first run) and **deletes it after the build**. This is load-bearing, not tidiness: the Go linker embeds a `.syso` into *every* binary in the package, including the test binary, which then demands elevation and makes `go test` fail with `The requested operation requires elevation`. `.syso` files are gitignored.

## Tests

```powershell
./tools/whitelist-gui/test.ps1            # everything, including the window test
./tools/whitelist-gui/test.ps1 -SkipGui   # logic only
go test ./tools/whitelist-gui/...         # logic only, window test skips itself
```

| Test                                   | Covers                                                      |
|----------------------------------------|-------------------------------------------------------------|
| `TestUnquoteImagePath`                 | Quoted, unquoted, and argument-bearing `ImagePath` values    |
| `TestExtractProcessName`               | `tasklist` CSV parsing, including malformed lines            |
| `TestNormalizeName`                    | Manual-entry cleanup                                         |
| `TestLoadWhitelistMissingFileIsNotAnError` | Absent file treated as empty, not as failure             |
| `TestLoadWhitelistParsing`             | Comments, blanks, case folding, de-duplication               |
| `TestSaveWhitelistRoundTripAndBackup`  | Backup contents and sorted round-trip                        |
| `TestRowModelFilterAndSelectionMapping`| View-index → name mapping under an active filter             |
| `TestBuildWindow`                      | Declarative layout construction (opt-in, `PS_GUI_TEST=1`)    |

### Why Two Manifests

`TestBuildWindow` needs an interactive desktop session and is skipped unless `PS_GUI_TEST=1`. It also needs Common Controls 6.0 actually linked into the test binary — without it, `MainWindow.Create()` fails with `TTM_ADDTOOL failed` as soon as walk attaches a tooltip to the first widget. But the release manifest additionally requests `requireAdministrator`, and `go test` cannot launch an elevated binary at all (`The requested operation requires elevation`).

Hence two manifests: `test.ps1` embeds the `asInvoker` variant for the duration of the run, `build.ps1` embeds the elevating one, and both delete the generated `.syso` afterwards so neither leaks into the other's binary.

## Out of Scope

- Editing the server-side application list or the server mode
- Editing `.env` outside of the install flow
- Live monitoring — the process list and diagnostics refresh only on demand
- Any network communication, including reachability checks against `SERVER_ADDRESS`
- Managing more than one machine

## Known Limitations

| Item | Detail |
|------|--------|
| Builtin list drift | `builtin.go` duplicates the hardcoded list in `agent/main.go`. If the agent's list changes and this one does not, the Status column silently lies. There is no automated check. |
| Comment loss | Hand-written `#` comments in `whitelist.txt` are dropped on save. |
| Single backup slot | `whitelist.txt.bak` is overwritten each save; only one undo level exists. |
| Elevation probe | `isElevated()` infers elevation from SCM access rather than inspecting the process token. |
| GUI test needs a desktop | `TestBuildWindow` cannot run on a headless CI agent, and needs `test.ps1` rather than a bare `go test` to get its manifest. |
| Install logic duplicated | The Go install flow and `Install-Agent{32,64}.ps1` implement the same procedure separately and will drift. Accepted deliberately — see Decisions. |
| Token shown in clear text | The install dialog displays `TOKEN` unmasked. Acceptable for an elevated admin tool, but worth revisiting. |
| Liveness heuristic | A stale `sync.json` is treated as "no server connection", but the same symptom is produced by a wrong token or a wrong `IDENTITY`. The tool cannot tell them apart without talking to the server, which is out of scope. |
| No agent version | The agent has no `-version` flag, so update comparison relies on SHA-256 rather than a version string. |
| Generic name is a bypass | Protecting `guardian.exe` by name lets any executable renamed to `guardian.exe` survive whitelist mode. See *Cost of a Generic Name*. |
| Sysnative image path unverified in the field | `normalizeServiceImagePath()` repairs a registration recorded through the WOW64 alias, but whether Windows actually produces such a registration was not reproduced -- the repair is defensive. It is a no-op when the path is already correct. |

## Decisions

| Decision | Rationale |
|---|---|
| User interface in English | Requested; keeps the console consistent with the specs and with Windows tooling it sits next to. |
| `Guardian.exe` ships as a 32-bit build, and is the only one built | One binary that starts on both 32- and 64-bit Windows, so a native variant would add a second thing to ship and a second protected name for no gain. See *Universal Build*. |
| Executable named `Guardian.exe`, and it is the only artifact | Matches the product naming used by `guardian.apk` and `guardian-server`, and describes a tool that now manages the whole agent lifecycle rather than just the whitelist. Cost is documented under *Cost of a Generic Name*. |
| Update action included | Third lifecycle action alongside install and uninstall; replacing a binary while keeping configuration is the common case in the field. |
| Install logic **not** unified with the PowerShell scripts | A shared CLI mode would need `AttachConsole` plumbing because the GUI binary has no console (`-H windowsgui`). The cost was judged higher than the drift risk. The scripts stay the headless path; the Go flow stays the interactive one. Changes to the install procedure must be applied to both. |
| Service registration delegated to the agent binary | The agent owns its own service configuration and event-log source; duplicating it would create a second source of truth. |

## Note on Existing Specs

`specs/agent.md` describes system-process protection lists for Linux and macOS and states that Windows process names come from `fields[0]`. Neither matches the current `agent/main.go`, which has a Windows-only protected list and parses quoted CSV. Worth correcting separately if specs are to be the source of truth going forward.
