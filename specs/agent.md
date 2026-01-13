# Agent Architecture Specification

## Overview

The ProcSentinel agent is a Go client that runs on target machines, polls the server for a list of blocked applications, and kills matching processes. It is designed primarily as a Windows service but also runs in console mode on Linux and macOS.

## File Structure

| File                   | Build Tag    | Purpose                                          |
|------------------------|--------------|--------------------------------------------------|
| `main.go`              | (none)       | Core logic: server polling, process list/kill, console entry point |
| `main_windows.go`      | `windows`    | Windows service detection via `svc.IsWindowsService()` |
| `main_stub.go`         | `!windows`   | Stub: `isWindowsService()` always returns false  |
| `service_windows.go`   | `windows`    | Full Windows service implementation (install, remove, start, stop, run) |
| `service_stub.go`      | `!windows`   | Stubs for service management functions + `svcName` const |
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

## Polling Loop

Two concurrent loops run after startup:

### 1. Server Poll (background goroutine)
- **Console mode:** polls every 10 seconds (hardcoded)
- **Service mode:** polls every 20 seconds (default), configurable via `CHECK_INTERVAL` env var
- Calls `GET /client/applications?identity=<IDENTITY>` with Bearer token auth
- On success: saves the list to `apps.txt` for offline use
- On failure: loads the last known list from `apps.txt` (fails closed — keeps enforcing cached blocks)

### 2. Process Monitor (main loop)
- **Console mode:** checks every 1 second (or 60 seconds if shutdown is triggered)
- **Service mode:** checks every 1 second via `time.Ticker`
- Gets the full process list, then iterates the blocked list:

#### Process List/Kill (platform-specific)
| OS              | List Command   | Kill Command             |
|-----------------|----------------|--------------------------|
| Windows         | `tasklist`     | `taskkill /F /IM <name>` |
| Linux / macOS   | `ps aux`       | `pkill -f <name>`        |

#### Matching
Process matching is case-insensitive substring match: `strings.Contains(processText, strings.ToLower(name))`.

## Special Commands

The server can inject special command strings into the blocked applications list:

| Command           | Behavior                                                              |
|-------------------|-----------------------------------------------------------------------|
| `force_poweroff`  | Triggers OS shutdown. On Windows: uses Win32 API with `SeShutdownPrivilege`. Console mode: sleeps 60s between checks after trigger. |
| `force_shutdown`  | Same behavior as `force_poweroff`                                     |

### Windows Shutdown Sequence
1. Open process token with `TOKEN_ADJUST_PRIVILEGES | TOKEN_QUERY`
2. Lookup LUID for `SeShutdownPrivilege`
3. Enable the privilege via `AdjustTokenPrivileges`
4. Call `InitiateSystemShutdownExW` with `bForceAppsClosed=TRUE`, `bRebootAfterShutdown=FALSE`

Non-Windows platforms have no `shutdownPC()` implementation (only the service stub exists).

## Configuration

Loaded from `.env` file in working directory (or executable directory for Windows service mode).

| Variable         | Purpose                              | Default                        |
|------------------|--------------------------------------|--------------------------------|
| `SERVER_ADDRESS` | Server URL                           | `http://localhost:8080`        |
| `TOKEN`          | Bearer token for server auth         | hardcoded fallback             |
| `IDENTITY`       | Computer identity integer            | (none — omitted from request)  |
| `CHECK_INTERVAL` | Server poll interval in seconds (service mode only) | `20`          |

## Offline Persistence

The agent saves the blocked applications list to `apps.txt` (one app per line) in the working directory every time it successfully fetches from the server. When the server is unreachable:

1. On initial startup: loads from `apps.txt`. If the file doesn't exist, starts with an empty list.
2. During polling: loads from `apps.txt`, preserving the last known blocked list.
3. When the server comes back online: fetches the current list and overwrites `apps.txt`.

This ensures the agent continues enforcing blocks even when the PC is offline.

## Concurrency Notes

- The blocked list (`[]string`) is shared between the poll goroutine and the monitor loop via pointer. A local copy is made each iteration to reduce race window, but there is no mutex — this is a known data race.
- The Windows service mode uses a `stopCh` channel for coordinated shutdown between the service handler and agent goroutine.

## Build

Requires `CGO_ENABLED=1` (for go-sqlite3 dependency at module level).

| Script            | Output                                        |
|-------------------|-----------------------------------------------|
| `agent/build64.ps1` | `dist/bin/agent/procsentinel-agent64.exe`   |
| `agent/build32.ps1` | `dist/bin/agent/procsentinel-agent32.exe`   |
