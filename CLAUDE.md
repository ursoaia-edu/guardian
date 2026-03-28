# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

ProcSentinel is a process monitoring and control system with three components:
- **Server** (Go) — REST API backend using SQLite, runs as a Linux systemd service
- **Agent** (Go) — Client-side process monitor/killer, runs as a Windows service (also supports Linux/macOS)
- **Mobile** (Flutter/Dart) — Android management app called "Guardian"

## Build Commands

### Server
```sh
cd server && ./build.sh        # Builds binary and creates ../release/procsentinel-server.tar.gz
```

### Agent (Windows, requires PowerShell)
```powershell
./agent/build64.ps1            # 64-bit build → dist/bin/agent/procsentinel-agent64.exe
./agent/build32.ps1            # 32-bit build → dist/bin/agent/procsentinel-agent32.exe
```

### Mobile
```sh
cd mobile && ./build.sh        # flutter build apk → ../release/guardian.apk
```

### Flutter icon generation
```sh
cd mobile && flutter pub run flutter_launcher_icons
```

## Architecture

### Server (`server/main.go` — single-file monolith)
- All logic in one ~1200-line file: HTTP handlers, SQLite schema, caching, auth
- `Server` struct holds DB connection, in-memory caches (`blockedAppsCache`, `enabledCache`, `systemCache`), and a `sync.RWMutex`
- Two auth tiers via Bearer tokens: `TOKEN` (client/agent endpoints) and `ADMIN_TOKEN` (management endpoints)
- Server auto-disables itself on startup (safety measure)
- SQLite tables: `blocked_applications`, `server_status`, `system`, `computers`

### Agent (`agent/main.go` + platform-specific files)
- Polls server every 10s for blocked app list, checks processes every 1s
- Platform-specific process listing/killing: `tasklist`/`taskkill` on Windows, `ps`/`pkill` on Unix
- Windows service support via `service_windows.go`, `shutdown_windows.go`, `main_windows.go`
- Non-Windows stubs: `main_stub.go`, `service_stub.go`
- Special commands: `force_poweroff`, `force_shutdown`

### Mobile (`mobile/lib/`)
- 4 screens: `HomeScreen` (blocked apps), `SystemScreen`, `ComputersScreen`, `SettingsScreen`
- `SettingsService` handles all HTTP calls and local persistence via `shared_preferences`

### API Structure
- `/client/applications` — agent fetches its blocked list (TOKEN auth)
- `/manage/applications` — CRUD for blocked apps (ADMIN_TOKEN auth)
- `/status` — get/set server enabled state
- `/system` — get/set system entries (e.g., power)
- `/manage/computers` — computer management
- `/health` — health check
- `/info` — server info

## Key Dependencies

- **Go:** `github.com/mattn/go-sqlite3` (CGO required), `golang.org/x/sys` (Windows APIs)
- **Flutter:** `http`, `shared_preferences`

## Deployment

Server installs to `/usr/local/bin/procsentinel/` as a systemd service. Agent and server both read `.env` files for configuration (`SERVER_ADDRESS`, `TOKEN`, `ADMIN_TOKEN`).

## Notes

- No automated tests exist yet
- Agent builds require `CGO_ENABLED=1`
- The Go module is `procsentinel` (root `go.mod`), but server and agent have separate `go.mod` files
