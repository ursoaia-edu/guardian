# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

### Server (rewrite)
- Replace the single-tenant SQLite server with a multi-tenant PostgreSQL 16 backend: any number of customer accounts now share one database, kept apart by row-level security rather than by running a separate server per customer
- Add a cabinet API (`/api/v1/...`) with email/password accounts, session cookies, rooms, per-room application lists, room guest sharing, computer management, binding tokens, and an account activity feed
- Replace the shared `TOKEN`/`ADMIN_TOKEN` bearer secrets with per-principal credentials: a session per signed-in user and a per-machine agent token minted at enrollment (`POST /agent/enroll`, replacing the old `/client/sync` identity parameter)
- Add `guardian-server migrate`, running schema migrations (goose) under a separate owner role; the running service's own database role can read and write but cannot alter the schema
- Remove `server/db.go`, the old `applications`/`server`/`client`/`computers` SQLite tables and in-memory caches, and the `modernc.org/sqlite` dependency
- The agent's own wire format (`/agent/sync`'s `applications`/`mode`/`client` shape) is unchanged on purpose, so the plan 3 agent rewrite is a URL and credential change rather than a protocol change — **no client that ships today works against this server.** The route moved from `/client/sync` to `/agent/sync` and the shared `TOKEN` to a per-machine one, and the current `agent/main.go` still calls the old route with the old credential, so every deployed agent 404s on every poll; the Flutter app's five calls all target `/manage/*` with `ADMIN_TOKEN`, which no longer exists, so it is entirely non-functional against this server. Both are expected — the agent is plan 3 and the Flutter migration is separate — but neither currently works, unlike a plain reading of "agents already in the field keep working" would suggest
- Tighten CORS to an explicit, credentialed `CABINET_ORIGIN` allow-list instead of a wildcard
- Resolve the client IP from `TRUSTED_PROXIES` (default `0`: the TCP peer, `X-Forwarded-For` ignored) instead of unconditionally trusting one forwarded hop — without a proxy in front, the old behaviour let any client pick its own rate-limit bucket; sessions and failed-login logs now record that resolved IP rather than the proxy's
- Add per-request structured logging with request ids, a slog-backed panic recoverer, and a 10-second handler timeout
- `GET /health` pings Postgres and answers `503` during a database outage instead of `200`
- `guardian-server migrate` reads `.env` from its working directory like the service does
- `/agent/sync` is now `POST` with telemetry in a JSON body (64 KiB cap) instead of a `runtime` query parameter, so telemetry is not written to proxy access logs and cannot grow a computer's row without bound; `hardware` at enrollment is capped the same way
- Close the composed pre-scope exposure with a second GUC, `app.user_id` (migration `00014`): the policies on `accounts`, `account_members` and `room_members` that had to run before an account scope exists now key on the calling user, so a query that scopes itself to neither reads nothing instead of reading every customer's account name, membership and room-sharing graph. Creating an account also now requires the owning user's scope
- Take the plan's seat limit under a row lock at enrollment: concurrent installs across a lab could each read the same seat count and all enroll, putting an account over its plan with no error anywhere
- Login returns the session token in the response body only for `"client": "mobile"`; a browser gets the `HttpOnly` cookie alone, which returning the token unconditionally had cancelled
- Cap every request body at 1 MiB, not just the three unauthenticated routes, and allow `X-Guardian-Account` in CORS preflight (without it the guest flow fails in the browser only)

### Deployment
- `dist/server/docker-compose.yml` adds Caddy (automatic TLS for `GUARDIAN_DOMAIN`, the one trusted proxy hop) and stops publishing the server's port directly; the server has a compose healthcheck
- `dist/server/Dockerfile` runs as a non-root user and ships CA certificates; `server/build.sh` builds a static Linux binary (`CGO_ENABLED=0`) so it actually starts on Alpine
- `guardian-server.service` runs as an unprivileged `guardian` user under systemd sandboxing; `install.sh` creates that user, keeps an existing `.env` on re-runs, and restricts `.env` to `root:guardian 0640`
- Add `.github/workflows/server.yml`: gofmt, vet, sqlc drift check, the full server suite against Postgres 16, and the static release build

### Agent (rewrite for the multi-tenant server)
- Enroll once with the cabinet's `BINDING_TOKEN` (`POST /agent/enroll`), save a per-machine token to `agent_credentials.json`, and delete the binding token from `.env`; sync with `POST /agent/sync`. `TOKEN` and `IDENTITY` are gone, and with them the hardcoded fallback token
- Machine identity is the OS's stable id (Windows `MachineGuid`, `/etc/machine-id`, macOS `IOPlatformUUID`), so a reinstall re-enrolls the same computer instead of adding a duplicate
- One run loop for console and service mode instead of two diverging copies; the shared policy is mutex-guarded (the documented data race is gone); enrollment refusals (token revoked, plan full, no connection) are three distinct messages; a revoked token keeps the last policy in force
- Add `-version`; `agentVersion` is stampable with `-ldflags`
- Add unit tests (`agent/agent_test.go`) — enrollment, the binding-token wipe, revocation, credentials for another server — that run on any platform
- Console (`tools/whitelist-gui`) and the PowerShell installers collect `BINDING_TOKEN` instead of `TOKEN`/`IDENTITY`; the console reports "enrolled" from `agent_credentials.json` and does not require a token to reinstall over an enrolled agent

### Guardian Console (new)
- Windows GUI (`tools/whitelist-gui`) for managing the agent on a single machine
- Install / Update agent / Uninstall, plus start, stop and restart of the ProcSentinelAgent service
- Diagnostics folding service state, executable, `.env`, `sync.json` freshness and `whitelist.txt` into one reported state
- Editor for the agent's local `whitelist.txt`, showing which running processes the agent would close
- Tail of the agent's Windows event log
- Ships as a single 32-bit `Guardian.exe` that runs on both 32- and 64-bit Windows and installs the matching agent
- Uninstall preserves `whitelist.txt` and `.env` by default, and refuses to delete anything that is not an agent folder

### Agent
- Add `guardian.exe` to the protected-process list, so the console is not closed by the agent in whitelist mode

### Docs
- Add `specs/whitelist-gui.md`

## [2.1.0] - 2026-04-04

### Mobile (Guardian)
- Add shield button to Dashboard, System, and Computers app bars to toggle server active/inactive status
- Sync server enabled state across all screens via shared ValueNotifier
- Use IndexedStack for tab navigation to prevent screen rebuilds on tab switch
- Parallelize data fetches with Future.wait for faster loading
- Add dropdown menu with "Remove All" option on Dashboard
- Fix application update not sending mode (affected apps in both blacklist and whitelist)
- Sort applications: disabled first, then alphabetically by name
- Remove active/inactive status bar and switch from Dashboard
- Move snackbar notifications to bottom with 1-second duration

### Server
- No changes

### Agent
- No changes
