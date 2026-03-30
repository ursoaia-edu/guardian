# Agent Architecture Specification

## Overview

The ProcSentinel agent is a Go client that runs on target machines, polls the server for the current sync state (applications, mode, client entries), and enforces process rules. It supports blacklist mode (kill matching processes) and whitelist mode (kill everything except allowed processes). Designed primarily as a Windows service but also runs in console mode on Linux and macOS.

## File Structure

| File                   | Build Tag    | Purpose                                          |
|------------------------|--------------|--------------------------------------------------|
| `main.go`              | (none)       | Core logic: sync polling, process list/kill, console entry point, file persistence |
| `main_windows.go`      | `windows`    | Windows service detection via `svc.IsWindowsService()` |
| `main_stub.go`         | `!windows`   | Stub: `isWindowsService()` always returns false  |
| `service_windows.go`   | `windows`    | Full Windows service implementation (install, remove, start, stop, run) |
| `service_stub.go`      | `!windows`   | Stubs for service management functions, `shutdownPCService`, `svcName` const |
| `shutdown_windows.go`  | `windows`    | Windows shutdown via Win32 API (`InitiateSystemShutdownExW`) |

## Execution Modes

### Console Mode (all platforms)
Default mode when not running as a Windows service. Entry point: `runConsole()`.

### Windows Service Mode
Registered as service name `ProcSentinelAgent`. The service:
- Accepts Stop, Shutdown, Pause, and Continue commands
- Runs the agent loop in a goroutine with a `stopCh` channel for graceful shutdown
- Logs to Windows Event Log
- Changes working directory to the executable's directory (to find `.env`)

### CLI Flags
| Flag        | Purpose                        |
|-------------|--------------------------------|
| `-install`  | Install as Windows service     |
| `-remove`   | Remove Windows service         |
| `-start`    | Start the Windows service      |
| `-stop`     | Stop the Windows service       |
| `-debug`    | Run service in debug mode      |

## Data Model

The agent works with a `SyncResponse` struct received from `GET /client/sync`:

```go
type SyncResponse struct {
    Applications []ClientApplication `json:"applications"`
    Mode         string              `json:"mode"`
    Client       []ClientEntry       `json:"client"`
}
```

- `Applications` — list of apps with name and mode, pre-filtered by the server to match current mode
- `Mode` — `"blacklist"`, `"whitelist"`, or `"free"`
- `Client` — key-value entries (e.g. `power` status)

## Polling Loop

Two concurrent loops run after startup:

### 1. Server Sync (background goroutine)
- **Console mode:** polls every 10 seconds (hardcoded)
- **Service mode:** polls every 20 seconds (default), configurable via `CHECK_INTERVAL` env var
- Calls `GET /client/sync?identity=<IDENTITY>` with Bearer token auth
- On success: saves full response to `sync.json`
- On failure: loads last known state from `sync.json`

### 2. Process Monitor (main loop)
- Checks every 1 second via loop (console) or `time.Ticker` (service)
- Skips if no applications in current state
- Checks client entries first (e.g. power), then enforces app rules based on mode

## Process Enforcement

### Blacklist Mode
Kill processes that match any application name in the list. Case-insensitive substring match.

### Whitelist Mode
Kill processes NOT in the allowed list, with system process protection. Parses each line of the process list to extract the process name, then kills it if:
1. It's not in the allowed set (case-insensitive)
2. It's not a system-critical process

### Process Name Extraction
| OS              | Format                                          | Extraction                   |
|-----------------|-------------------------------------------------|------------------------------|
| Windows         | `tasklist` — first field is process name        | `fields[0]`                  |
| Linux / macOS   | `ps aux` — 11th field is command                | `filepath.Base(fields[10])`  |

### Process List/Kill (platform-specific)
| OS              | List Command   | Kill Command             |
|-----------------|----------------|--------------------------|
| Windows         | `tasklist`     | `taskkill /F /IM <name>` |
| Linux / macOS   | `ps aux`       | `pkill -f <name>`        |

### System Process Protection
In whitelist mode, the agent maintains a hardcoded list of system-critical processes that are never killed:

- **Windows:** `system`, `csrss.exe`, `svchost.exe`, `explorer.exe`, `dwm.exe`, `lsass.exe`, `services.exe`, `winlogon.exe`, `conhost.exe`, `cmd.exe`, `powershell.exe`, `procsentinel-agent*.exe`, etc.
- **Linux:** `init`, `systemd`, `bash`, `zsh`, `sh`, `sshd`, `cron`, `dbus-daemon`, `procsentinel-agent`, `ps`, `pkill`, etc.
- **macOS:** `launchd`, `WindowServer`, `kernel_task`, `loginwindow`, `Finder`, `Dock`, etc.

### Whitelist File
On first run in whitelist mode, the agent generates `whitelist.txt` from currently running processes. This file is loaded once at startup into a `userWhitelist` map and used alongside the system process list to determine which processes are safe.

## Client Entries

The agent reads client entries from the sync response to handle system-level commands:

| Entry   | Behavior when `status: false`                     |
|---------|---------------------------------------------------|
| `power` | Triggers OS shutdown via `shutdownPCService()`    |

### Windows Shutdown Sequence
1. Open process token with `TOKEN_ADJUST_PRIVILEGES | TOKEN_QUERY`
2. Lookup LUID for `SeShutdownPrivilege`
3. Enable the privilege via `AdjustTokenPrivileges`
4. Call `InitiateSystemShutdownExW` with `bForceAppsClosed=TRUE`, `bRebootAfterShutdown=FALSE`

Non-Windows platforms return an error from the `shutdownPCService()` stub.

## Configuration

Loaded from `.env` file in working directory (or executable directory for Windows service mode).

| Variable         | Purpose                              | Default                        |
|------------------|--------------------------------------|--------------------------------|
| `SERVER_ADDRESS` | Server URL                           | `http://localhost:8080`        |
| `TOKEN`          | Bearer token for server auth         | hardcoded fallback             |
| `IDENTITY`       | Computer identity integer            | (none — omitted from request)  |
| `CHECK_INTERVAL` | Server poll interval in seconds (service mode only) | `20`          |

## Offline Persistence

The agent saves the sync response to `sync.json` in the working directory every time it successfully syncs with the server. When the server is unreachable:

1. On initial startup: loads from `sync.json`. If the file doesn't exist, starts with empty state.
2. During polling: loads from `sync.json`, preserving the last known state.
3. When the server comes back online: fetches current state and overwrites `sync.json`.

This ensures the agent continues enforcing rules even when the PC is offline, including the correct mode and client entries.

## Concurrency Notes

- The `SyncResponse` pointer is shared between the poll goroutine and the monitor loop. Local copies of applications and mode are made each iteration to reduce the race window, but there is no mutex — this is a known data race.
- The Windows service mode uses a `stopCh` channel for coordinated shutdown between the service handler and agent goroutine.

## Build

| Script            | Output                                              |
|-------------------|-----------------------------------------------------|
| `agent/build64.ps1` | `dist/agent/bin/agent/procsentinel-agent64.exe`   |
| `agent/build32.ps1` | `dist/agent/bin/agent/procsentinel-agent32.exe`   |
