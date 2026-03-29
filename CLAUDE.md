# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

ProcSentinel is a process monitoring and control system with three components:
- **Server** (Go) — REST API backend using SQLite, runs as a Linux systemd service
- **Agent** (Go) — Client-side process monitor/killer, runs as a Windows service (also supports Linux/macOS)
- **Mobile** (Flutter/Dart) — Android management app called "Guardian"
- **Guardian Console** (Go + lxn/walk) — Windows GUI in `tools/whitelist-gui/` for installing, diagnosing and updating the agent, and editing its local `whitelist.txt`

## Build Commands

### Server
```sh
cd server && ./build.sh        # Builds binary and creates ../release/procsentinel-server.tar.gz
```

### Agent (Windows, requires PowerShell)
```powershell
./agent/build64.ps1            # 64-bit build → dist/agent/bin/agent/procsentinel-agent64.exe
./agent/build32.ps1            # 32-bit build → dist/agent/bin/agent/procsentinel-agent32.exe
```

### Mobile
```sh
cd mobile && ./build.sh        # flutter build apk → ../release/guardian.apk
```

### Guardian Console (Windows, requires PowerShell)
```powershell
./tools/whitelist-gui/build.ps1   # 32-bit universal build → dist/agent/Guardian.exe
./tools/whitelist-gui/test.ps1    # tests, including the window smoke test
```
Built as 32-bit on purpose so one binary runs on both 32- and 64-bit Windows;
it detects the OS architecture at run time and installs the matching agent.
See `specs/whitelist-gui.md`.

### Flutter icon generation
```sh
cd mobile && flutter pub run flutter_launcher_icons
```

## Architecture

### Server (`server/` — multi-file Go package, ~1000 lines total)
- `main.go` — Server struct, startup, graceful shutdown
- `routes.go` — chi router setup with CORS, route groups (unauthenticated, client auth, admin auth)
- `handlers.go` — all HTTP handler methods
- `db.go` — SQLite database init, migrations, CRUD operations, cache loading
- `models.go` — request/response structs (Application, Computer, ClientEntry, etc.)
- `middleware.go` — Bearer token auth middleware (ClientAuth, AdminAuth)
- `helpers.go` — utility functions (JSON writing, env file loading, IP detection)
- `Server` struct holds DB connection and in-memory caches (`appsCache`, `enabledCache`, `modeCache`, `clientCache`) with a `sync.RWMutex`
- Uses `go-chi/chi` router and `modernc.org/sqlite` (pure Go, no CGO required)
- Two auth tiers via Bearer tokens: `TOKEN` (client/agent endpoints) and `ADMIN_TOKEN` (management endpoints)
- SQLite tables: `applications`, `server`, `client`, `computers`

### Agent (`agent/main.go` + platform-specific files)
- Polls server via `/client/sync` (10s console mode, 20s service mode configurable via `CHECK_INTERVAL`), checks processes every 1s
- Platform-specific process listing/killing: `tasklist`/`taskkill` on Windows, `ps`/`pkill` on Unix
- Windows service support via `service_windows.go`, `shutdown_windows.go`, `main_windows.go`
- Non-Windows stubs: `main_stub.go`, `service_stub.go`
- Special commands: `force_poweroff`, `force_shutdown`

### Mobile (`mobile/lib/`)
- 4 screens: `HomeScreen` (blocked apps), `SystemScreen`, `ComputersScreen`, `SettingsScreen`
- `SettingsService` handles all HTTP calls and local persistence via `shared_preferences`

### Landing page (`docs/`)
- Static marketing landing page — single self-contained `docs/index.html`
- Styled with Tailwind via CDN (config inlined in a `<script>`), fonts from Google Fonts (Space Grotesk / Inter / JetBrains Mono)
- Dark "terminal" aesthetic with an animated "fleet console" hero (vanilla JS, respects `prefers-reduced-motion`)
- Brand assets in `docs/assets/` (also used by `README.md`); served directly via GitHub Pages from the `docs/` folder
- Preview locally: `cd docs && python3 -m http.server 8099` → http://localhost:8099

### API Endpoints
- `/client/sync` — agent fetches apps, mode, and client entries (TOKEN auth)
- `/manage/applications` — CRUD for blocked apps (ADMIN_TOKEN auth)
- `/manage/applications/reset` — clear all applications (ADMIN_TOKEN auth)
- `/status` — get/set server enabled state and mode (ADMIN_TOKEN auth)
- `/info` — server info (ADMIN_TOKEN auth)
- `/client` — get/set client entries like power (ADMIN_TOKEN auth)
- `/manage/computers` — computer management (ADMIN_TOKEN auth)
- `/manage/computers/reset` — unblock all computers (ADMIN_TOKEN auth)
- `/manage/computers/block_all` — block all computers (ADMIN_TOKEN auth)
- `/health` — health check (unauthenticated)

## Key Dependencies

- **Server Go:** `go-chi/chi` (router), `go-chi/cors`, `modernc.org/sqlite` (pure Go SQLite)
- **Agent Go:** `golang.org/x/sys` (Windows APIs)
- **Flutter:** `http`, `shared_preferences`

## Deployment

### Dist structure
```
dist/
├── server/          # Server binary, .env template, systemd service, install.sh, Docker files
├── guardian.apk     # Mobile app
└── agent/           # Agent .env template, Install/Uninstall .bat files, PowerShell scripts, binaries,
                     # and Guardian.exe (the console)
```

Server installs to `/usr/local/bin/procsentinel/` as a systemd service. Agent and server both read `.env` files for configuration (`SERVER_ADDRESS`, `TOKEN`, `ADMIN_TOKEN`).

## Rules

- **Always update specs on code changes:** After any code change, update the corresponding files in `specs/` (`server.md`, `agent.md`, `api.md`) and this `CLAUDE.md` to keep documentation in sync. This includes API changes, schema changes, config changes, file structure changes, and build output paths.

## Notes

- The only automated tests are for Guardian Console (`tools/whitelist-gui`); the server, agent and mobile app have none
- `tools/whitelist-gui/builtin.go` mirrors the hardcoded protected-process list in `agent/main.go` and must be kept in sync by hand
- `tools/mkico` is a separate module (it needs `golang.org/x/image` only to build the console's icon)
- Server and agent have separate `go.mod` files (modules `server` and `agent`)
- Server uses pure Go SQLite (`modernc.org/sqlite`) — CGO is NOT required for server builds
- Agent builds require `CGO_ENABLED=1`
