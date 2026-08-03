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

### Server (`server/` — multi-tenant Go package, Postgres-backed)
- `main.go` — `Server` struct (a pgx pool), startup, `migrate` subcommand, graceful shutdown
- `routes.go` — chi router setup, CORS restricted to `CABINET_ORIGIN`, route groups (unauthenticated, cabinet session auth, agent auth)
- `middleware.go` — `SessionAuth` (cabinet: cookie or Bearer session token) and `AgentAuth` (per-agent Bearer token)
- `tenant.go` — `Tenant`/context plumbing, `inAccount` (scopes a transaction via the `app.account_id` GUC) and `inUser` (`app.user_id`, for the reads that must happen before an account is known)
- `auth.go` — argon2id password hashing, session/agent/binding token minting (SHA-256 digests)
- `handlers_auth.go`, `handlers_rooms.go`, `handlers_members.go`, `handlers_computers.go`, `handlers_agent.go`, `events.go` — HTTP handlers grouped by resource; `handlers.go` keeps only `/health` (which pings Postgres)
- `logging.go` — `clientIP` (proxy-aware, the only way an IP is read), slog request logger, panic recoverer
- `maintenance.go` — hourly purge of expired sessions and events older than 180 days (the latter through a `SECURITY DEFINER` function, since RLS blocks an unscoped delete)
- `responses.go` — the API's own response types; handlers never return a sqlc row, and no response carries `account_id`
- `migrate.go` — embeds and runs `db/migrations/*.sql` (goose) against `MIGRATE_DATABASE_URL`
- `db/migrations/` (goose SQL, schema owned by `guardian_owner`), `db/queries/` (sqlc sources), `internal/db/` (generated sqlc code, see `sqlc.yaml`)
- `models.go` — only the agent's wire format (`ClientApplication`, `ClientEntry`, `ClientSyncResponse`, unchanged on purpose) plus `ErrorResponse`
- PostgreSQL row-level security (RLS) is the tenancy boundary, not handler code: `guardian_app`, the role the service connects as, owns no table and has no `BYPASSRLS`
- Two scoping GUCs, both set only in `tenant.go`: a query that sets neither reads nothing, on every table. `app.user_id` (migration `00014`) narrows the pre-account policies on `accounts`/`account_members`/`room_members` to the calling user — `users` and the binding-token lookup remain deliberately un-narrowed, see `specs/server.md`
- `/api/v1` is two route groups: a small guest-reachable one and everything else behind the `ManagerOnly` middleware. `server/authz_test.go` walks the router and fails on any route in neither list — add a route, classify it there
- Two auth paths, no shared secret token anywhere: a session (cookie or Bearer) for the cabinet, a per-agent Bearer token (minted at `/agent/enroll`) for agents; both resolve to a `Tenant{AccountID, ...}` server-side
- Postgres tables: `users`, `accounts`, `account_members`, `sessions`, `rooms`, `room_members`, `applications`, `computers`, `binding_tokens`, `events`
- See `specs/server.md` for the full architecture (RLS policies, roles, the `account_for_agent_token` function, migrations)

### Agent (`agent/`)
- `main.go` (wire types, flags, console entry, process list/kill, whitelist, `sync.json`), `agent.go` (the single run loop shared by console and service mode: config, enrollment, sync, enforcement, `logger` seam), `client.go` (HTTP client, `agent_credentials.json`, `.env` reading and the binding-token wipe), `passport_windows.go`/`passport_other.go` (machine GUID, OS info)
- Enrolls once: `.env`'s `BINDING_TOKEN` (from the cabinet's installer) → `POST /agent/enroll` → per-machine token saved to `agent_credentials.json` (`0600`), then the `BINDING_TOKEN` line is deleted from `.env`. Machine identity is the OS's stable id (Windows `MachineGuid`, `/etc/machine-id`, macOS `IOPlatformUUID`)
- Polls `POST /agent/sync` with the agent token (10s console mode, 20s service mode, `CHECK_INTERVAL`), checks processes every 1s; `sync.json` is loaded before anything else so the last policy is enforced offline and after a revocation (fail-secure)
- Platform-specific process listing/killing: `tasklist`/`taskkill` on Windows, `ps`/`pkill` on Unix
- Windows service support via `service_windows.go`, `shutdown_windows.go`, `main_windows.go`; non-Windows stubs `main_stub.go`, `service_stub.go`
- `.env` keys: `SERVER_ADDRESS`, `BINDING_TOKEN`, `CHECK_INTERVAL` (`TOKEN`/`IDENTITY` are gone). `agentVersion` in `agent.go`, stampable with `-ldflags "-X main.agentVersion=…"`, printed by `-version`
- Pure Go, cross-compiles from Linux (`GOOS=windows GOARCH=386 go build`); `go test ./...` needs no server

### Mobile (`mobile/`)
- Signs in as a person (`POST /api/v1/auth/login` with `"client": "mobile"`, which is what returns the token in the body) and sends `Authorization: Bearer <session token>` plus `X-Guardian-Account`. The shared `ADMIN_TOKEN` field is gone
- `LoginScreen` (sign in / register) gates the app; `_Root` in `main.dart` listens to `SettingsService` so signing in or out swaps the screen
- 4 tabs: `HomeScreen` (**Rules** — one room's applications), `SystemScreen` (**Room** — protection, mode, power, rename/delete/create), `ComputersScreen` (pool, room assignment, lock, unenrol, mint an installer token), `SettingsScreen` (server address, account switcher, sign out)
- `SettingsService` is the whole API client plus `shared_preferences` state (server address, session token, selected account and room). Failures raise `ApiException` carrying the server's message — the old client returned `false`/`[]`, so an outage looked like an empty account
- `flutter test` needs no Android SDK and no server: API contract tests drive the service against `MockClient`, plus a login-screen widget test. See `specs/mobile.md`

### Landing page (`docs/`)
- Static marketing landing page — single self-contained `docs/index.html`
- Styled with Tailwind via CDN (config inlined in a `<script>`), fonts from Google Fonts (Space Grotesk / Inter / JetBrains Mono)
- Dark "terminal" aesthetic with an animated "fleet console" hero (vanilla JS, respects `prefers-reduced-motion`)
- Brand assets in `docs/assets/` (also used by `README.md`); served directly via GitHub Pages from the `docs/` folder
- Preview locally: `cd docs && python3 -m http.server 8099` → http://localhost:8099

### API Endpoints
- `POST /agent/enroll` — agent enrolls with a binding token, gets its own per-machine agent token (unauthenticated; the token in the body is the gate)
- `POST /agent/sync` — agent posts `{"runtime": {...}}` telemetry (64 KiB cap) and fetches its room's applications, mode, and client entries (agent token auth)
- `POST /api/v1/auth/register`, `/login`, `/logout` — cabinet account creation and session auth. Login always sets the `HttpOnly` cookie and returns the token in the body only for `"client": "mobile"`
- `GET /api/v1/me` — caller's identity and every account they can act in (session auth)
- `/api/v1/rooms`, `/api/v1/rooms/{roomID}` — room CRUD (session auth)
- `/api/v1/rooms/{roomID}/applications`, `/api/v1/rooms/{roomID}/applications/{appID}` — per-room blacklist/whitelist entries; PATCH toggles `enabled` (session auth)
- `/api/v1/rooms/{roomID}/members` — room guest grants (session auth)
- `/api/v1/computers`, `/api/v1/computers/{computerID}` — computer listing, reading, reassignment/blocking, and DELETE to unenrol (revokes that machine's agent token) (session auth)
- `/api/v1/account/members`, `/api/v1/account/members/{userID}` — the account's own owner/admins (session auth, manager only)
- `/api/v1/binding-tokens` — mint/revoke the installer's enrollment token (session auth)
- `GET /api/v1/events` — account activity, paged by an opaque `cursor` (session auth, manager only)
- `GET /health` — health check (unauthenticated; `503` when Postgres is unreachable)
- Session requests may set `X-Guardian-Account: <account-id>` to act as a different one of the caller's own proven memberships (e.g. a guest room grant) — see `specs/api.md`
- Full request/response reference: `specs/api.md`

## Key Dependencies

- **Server Go:** `go-chi/chi` (router), `go-chi/cors`, `jackc/pgx/v5` (PostgreSQL driver/pool), `pressly/goose/v3` (migrations)
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

Server installs to `/usr/local/bin/guardian/` as a systemd service running as the unprivileged `guardian` user (sandboxed unit; `install.sh` creates the user and never overwrites an existing `.env`). Both agent and server read `.env` files for configuration; the agent's is `SERVER_ADDRESS`/`TOKEN`, the server's is `DATABASE_URL`/`MIGRATE_DATABASE_URL`/`CABINET_ORIGIN`/`SERVER_ADDRESS`/`TRUSTED_PROXIES` (see `dist/server/server.env` and `specs/server.md`). `CABINET_ORIGIN` is required — the server refuses to start without it, since the old `http://localhost:5173` default silently allowed a developer's machine to act as any signed-in user in production. `TRUSTED_PROXIES` is the number of reverse-proxy hops in front of the server — `0` (default, X-Forwarded-For ignored) bare, `1` behind Caddy/nginx. `dist/server/docker-compose.yml` runs Postgres + migrate + server + Caddy (TLS for `GUARDIAN_DOMAIN`) and sets `TRUSTED_PROXIES=1` itself; the server is not published outside the compose network. `server/build.sh` produces a static Linux binary (`CGO_ENABLED=0`; `GOARCH=amd64 ./build.sh` when cross-building) because the Dockerfile runs it on Alpine. Deploy order for the server: run `./guardian-server migrate` once (it reads `MIGRATE_DATABASE_URL` from `.env` in its working directory, like the service), then start the service — the running service's own role cannot alter the schema.

## Rules

- **Always update specs on code changes:** After any code change, update the corresponding files in `specs/` (`server.md`, `agent.md`, `api.md`, `mobile.md`, `whitelist-gui.md`) and this `CLAUDE.md` to keep documentation in sync. This includes API changes, schema changes, config changes, file structure changes, and build output paths.
- **The agent's `.env` keys are shared with the console:** `agent/agent.go` reads them, `tools/whitelist-gui/envfile.go` (`knownEnvOrder`) writes them, and `dist/agent/agent.env` templates them — change all three together.

## Notes

- Server tests need a live Postgres: `docker compose -f server/docker-compose.dev.yml up -d`,
  then `TEST_DATABASE_URL` and `TEST_APP_DATABASE_URL` as in `specs/plans/2026-09-05-saas-multitenant-core.md`
- `server/fakeagent` enrolls and syncs like the Windows agent, for exercising the API without Windows
- `server/isolation_test.go` is mandatory: every new account-scoped endpoint gets a row in its table
- Tests that read an account-scoped table back must scope the read, or use `observe(t)` (a pool on the owner role, not subject to RLS) when the point is to observe the database independently of the app's scoping — this now includes `accounts` and `account_members`
- CI: `.github/workflows/server.yml` runs gofmt, `go vet`, `sqlc diff` (committed `internal/db/` must match `db/queries/`), the full suite against a Postgres 16 service, and the static release build on every push touching `server/`
- Agent tests (`agent/agent_test.go`) run against an `httptest` server on any platform; mobile tests (`mobile/test/`) run with `flutter test` and need no Android SDK. CI covers server, agent and mobile (`.github/workflows/`); the Windows console is still tested only by hand (`tools/whitelist-gui/test.ps1`)
- `tools/whitelist-gui/builtin.go` mirrors the hardcoded protected-process list in `agent/main.go` and must be kept in sync by hand
- `tools/mkico` is a separate module (it needs `golang.org/x/image` only to build the console's icon)
- Server and agent have separate `go.mod` files (modules `server` and `agent`)
- Server uses PostgreSQL via pgx; CGO is not required
- Agent builds require `CGO_ENABLED=1`
