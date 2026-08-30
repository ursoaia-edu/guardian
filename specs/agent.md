# Agent Architecture Specification

## Overview

The ProcSentinel agent is a Go client that runs on target machines, enrolls
itself with the multi-tenant server once, then polls the server for its
room's policy (applications, mode, client entries) and enforces process
rules. It supports blacklist mode (kill matching processes) and whitelist
mode (kill everything except allowed processes). Designed primarily as a
Windows service but also runs in console mode on Linux and macOS.

## File Structure

| File                   | Build Tag    | Purpose                                          |
|------------------------|--------------|--------------------------------------------------|
| `main.go`              | (none)       | Wire types, CLI flags, console entry point, process list/kill, whitelist, `sync.json` persistence |
| `agent.go`             | (none)       | The one run loop both modes share: configuration, enrollment, sync, enforcement; the `logger` seam |
| `client.go`            | (none)       | HTTP client for `/agent/enroll` and `/agent/sync`, `agent_credentials.json`, `.env` reading and the binding-token wipe |
| `passport_windows.go`  | `windows`    | Machine GUID (registry `MachineGuid`) and Windows edition/build |
| `passport_other.go`    | `!windows`   | Machine id from `/etc/machine-id` (Linux) or `IOPlatformUUID` (macOS) |
| `main_windows.go`      | `windows`    | Windows service detection via `svc.IsWindowsService()` |
| `main_stub.go`         | `!windows`   | Stub: `isWindowsService()` always returns false  |
| `service_windows.go`   | `windows`    | Windows service wrapper (install, remove, start, stop, run), event-log logger |
| `service_stub.go`      | `!windows`   | Stubs for service management functions, `shutdownPCService`, `svcName` const |
| `shutdown_windows.go`  | `windows`    | Windows shutdown via Win32 API (`InitiateSystemShutdownExW`) |
| `agent_test.go`        | (none)       | Enrollment, sync, credential and `.env` behaviour against an `httptest` server |

## Execution Modes

### Console Mode (all platforms)
Default mode when not running as a Windows service. Entry point:
`runConsole()`, which runs `runAgent` from the current directory with a
stdout logger until Ctrl+C / `SIGTERM`.

### Windows Service Mode
Registered as service name `ProcSentinelAgent`. The service:
- Accepts Stop, Shutdown, Pause, and Continue commands
- Changes working directory to the executable's directory (to find `.env`,
  `sync.json`, `whitelist.txt` and `agent_credentials.json`), then runs the
  same `runAgent` loop with an event-log logger and a `stopCh` for graceful
  shutdown

There is exactly one run loop. Console and service mode differ only in the
`logger` implementation and the default poll interval.

### CLI Flags
| Flag        | Purpose                        |
|-------------|--------------------------------|
| `-install`  | Install as Windows service     |
| `-remove`   | Remove Windows service         |
| `-start`    | Start the Windows service      |
| `-stop`     | Stop the Windows service       |
| `-debug`    | Run service in debug mode      |
| `-version`  | Print the agent version and exit |

## Configuration

Loaded from `.env` in the working directory (the executable's directory in
service mode). A real environment variable of the same name overrides the
file.

| Variable         | Purpose                              | Default                        |
|------------------|--------------------------------------|--------------------------------|
| `SERVER_ADDRESS` | Server base URL (trailing `/` tolerated) | none — logged as an error if missing |
| `BINDING_TOKEN`  | The installer's enrollment token from the cabinet. **Removed from `.env` by the agent itself** the moment enrollment succeeds | (none) |
| `CHECK_INTERVAL` | Server poll interval in seconds      | `20` service mode, `10` console mode |

`TOKEN` and `IDENTITY` — the single-tenant server's shared secret and
self-declared identity — no longer exist. An old `.env` that still carries
them is read without error and they are ignored.

## Identity and Enrollment

The agent never holds a shared secret. It joins the fleet once, by trading
the installer's **binding token** for a **per-machine agent token**:

1. On start, `agent_credentials.json` is looked for next to `.env`. If it
   exists (and was issued by the current `SERVER_ADDRESS`), the machine is
   enrolled and the sync loop starts immediately.
2. Otherwise, with a `BINDING_TOKEN` in `.env`, the agent calls
   `POST /agent/enroll` with its passport:
   `machine_guid`, `hostname`, `os_name`, `os_build`, `arch`,
   `agent_version`, and a small `hardware` object (`cpu_count`, `go_os`).
3. On `201` it writes `agent_credentials.json` (mode `0600`, atomic
   temp-file-and-rename) holding `agent_token`, `machine_guid`,
   `server_address` and `enrolled_at`, then **deletes the `BINDING_TOKEN`
   line from `.env`**, leaving every other byte of the file untouched. The
   binding token is reusable and account-wide; it has no business staying on
   the disk once the machine has a token of its own.
4. It then syncs at once.

**Machine identity** is the OS's own stable identifier — on Windows
`HKLM\SOFTWARE\Microsoft\Cryptography\MachineGuid` (read through the 64-bit
registry view so a 32-bit agent sees it too), on Linux `/etc/machine-id`, on
macOS the `IOPlatformUUID`. The server keys "same machine, reinstalled" on
it, so a reinstall re-enrolls the same row and rotates its token rather than
consuming a second seat. If no identifier can be read, a random one is
generated and held for the life of the process.

**Enrollment outcomes** are three distinct messages, because the customer
fixes each somewhere different:

| Server answer | Log message says | Then |
|---|---|---|
| `401` | the installer's token is no longer valid — download a fresh installer from the cabinet | retry every 60 s (the token may be re-enabled) |
| `402` | the plan does not cover another computer — upgrade in the cabinet | retry every 60 s (the agent picks up the upgrade by itself) |
| no connection / other | enrollment failed with the underlying error | retry every 60 s |
| no `BINDING_TOKEN` and no credentials | not enrolled; reinstall from the cabinet | retry every 60 s |

None of these exits the process. Whatever policy `sync.json` holds stays in
force throughout.

**Credentials for another server** (`server_address` in the file differs
from `SERVER_ADDRESS`) are ignored, with a warning, and the machine is treated
as not enrolled — presenting them would produce an endless stream of `401`s
indistinguishable from a revocation.

## Data Model

The agent works with a `SyncResponse` struct received from `POST /agent/sync`:

```go
type SyncResponse struct {
    Applications []ClientApplication `json:"applications"`
    Mode         string              `json:"mode"`
    Client       []ClientEntry       `json:"client"`
}
```

- `Applications` — list of apps with name and mode, pre-filtered by the
  server to match current mode
- `Mode` — `"blacklist"`, `"whitelist"`, or `"free"`
- `Client` — key-value entries (e.g. `power` status)

The shape is the one the single-tenant server spoke; the multi-tenant server
kept it on purpose.

## Polling Loop

Two concurrent loops run after startup:

### 1. Server Sync (background goroutine)
- Polls `POST /agent/sync` every `CHECK_INTERVAL` with
  `Authorization: Bearer <agent token>` and a JSON body
  `{"runtime": {"uptime_s": …, "mode": "service"|"console", "agent_version": …}}`
- On success: replaces the in-memory policy and rewrites `sync.json`. A log
  line is written only when the policy changed (mode, application count or
  power), not on every poll — three lines a minute of "nothing new" would
  bury the event log.
- On `401` (token revoked, or the machine re-enrolled elsewhere): logs that
  the server no longer accepts this computer's token and **keeps the last
  policy in force**. Deleting a computer in the cabinet must not free it.
  Re-enrolling means reinstalling from the cabinet.
- On any other failure: logs it and keeps the last policy.

#### The blocking log rides along in the same body

Every process the agent successfully kills is recorded in `blocklog.go` and
shipped inside the next sync body, so reporting costs no extra request:

```json
{"runtime": {…}, "batch_id": "…", "blocked": [
  {"process": "steam.exe", "reason": "blacklist", "count": 47,
   "first_at": "2026-09-09T14:00:00Z", "last_at": "2026-09-09T14:00:46Z"}]}
```

- **Aggregated** by `(process, reason)`. The enforce loop runs once a second
  and a respawning launcher is killed every time; one row per kill would be
  3,600 rows an hour per process per machine.
- **An entry covers at most an hour.** Only reachable while the agent is
  offline, since a successful sync closes everything open. Without the cap, a
  machine off the network for a week arrives with one row claiming 600,000
  kills across seven days — true and useless.
- **At most 500 entries are held**, and past that the oldest are dropped and
  replaced by a single `guardian.log_overflow` marker carrying how many went.
  A log that quietly loses rows is worse than one that admits it.
- **At most 200 travel per sync**; the remainder goes with the next one. The
  server drops anything past its own limit of 200, so sending more would lose
  data silently.
- **Only a successful kill is recorded.** The log answers "what was blocked",
  and a process the agent could not touch was not blocked.
- **The agent never sends `locked`.** It cannot tell a locked machine from a
  whitelist that allows nothing — both arrive as `mode: "whitelist"` with an
  empty list — so it sends `whitelist` and the server rewrites the reason,
  because only the server knows.
- **`batch_id` makes a retry safe.** The batch stays staged until a `200`, so a
  sync whose response was lost is resent as the same batch under the same id
  and the server, which has already committed it, ignores the repeat. Both
  fields are omitted entirely when there is nothing to report, so the body is
  what a pre-batch server always saw.

Pending entries live in memory only and are lost if the agent restarts.
Writing them to disk every second on every managed machine costs more than the
last minute of a kill log is worth.

### 2. Process Monitor (main loop)
- Checks every 1 second via `time.Ticker`
- Skips when there is no policy, when `mode` is `free`, or when the list is
  empty in blacklist mode. **Whitelist mode with an empty list enforces** —
  that is how the server expresses a locked machine.
- Checks client entries first (`power`), then enforces app rules based on
  mode. After asking the OS to shut down it waits 60 s before checking again,
  rather than re-requesting the shutdown every second.

The in-memory policy is guarded by an `RWMutex` (`policyState`); the data
race the previous two loops shared is gone.

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
| Windows         | `tasklist /FO CSV /NH` — first quoted field     | text between the first pair of quotes |
| Linux / macOS   | `ps aux` — 11th field is command                | `filepath.Base(fields[10])`  |

### Process List/Kill (platform-specific)
| OS              | List Command   | Kill Command             |
|-----------------|----------------|--------------------------|
| Windows         | `tasklist`     | `taskkill /F /IM <name>` |
| Linux / macOS   | `ps aux`       | `pkill -f <name>`        |

### System Process Protection
In whitelist mode, the agent maintains a hardcoded list of system-critical
processes that are never killed (`isSystemProcess` in `main.go`; mirrored by
hand in `tools/whitelist-gui/builtin.go`): Windows kernel/session, core
services, shell, security, input, networking and tooling processes, the agent
itself, and `guardian.exe` (the console).

### Whitelist File
On first run, the agent generates `whitelist.txt` from currently running
processes. This file is loaded once at startup into a `userWhitelist` map and
used alongside the system process list to determine which processes are safe.

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

## Files in the Agent Directory

| File | Written by | Purpose |
|---|---|---|
| `.env` | installer / console; the agent removes `BINDING_TOKEN` from it | configuration |
| `agent_credentials.json` | the agent, at enrollment | this machine's own token (`0600`) |
| `sync.json` | the agent, on every successful sync | last known policy, applied offline |
| `whitelist.txt` | the agent, on first run; edited by the console | locally protected processes |

## Offline Persistence

`sync.json` is loaded before anything else at startup, so the machine is
enforcing its last known policy from the first second whether or not the
server is reachable — or the machine's token still valid. When the server
comes back online, the next sync overwrites it. This is deliberate: if
protection lapsed after some time offline, unplugging the network would be
the simplest way to defeat the product.

## Version

`agentVersion` (`agent.go`) is reported at enrollment and on every sync, and
printed by `-version`. Stamp a release with
`-ldflags "-X main.agentVersion=<version>"`.

## Build

| Script            | Output                                              |
|-------------------|-----------------------------------------------------|
| `agent/build64.ps1` | `dist/agent/bin/agent/procsentinel-agent64.exe`   |
| `agent/build32.ps1` | `dist/agent/bin/agent/procsentinel-agent32.exe`   |

The agent is pure Go (`golang.org/x/sys` only) and cross-compiles from any
host: `GOOS=windows GOARCH=386 go build` produces the 32-bit Windows binary
on Linux. `go test ./...` runs on any platform and needs no server.
