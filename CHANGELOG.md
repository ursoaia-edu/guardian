# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

### Server (rewrite)
- Replace the single-tenant SQLite server with a multi-tenant PostgreSQL 16 backend: any number of customer accounts now share one database, kept apart by row-level security rather than by running a separate server per customer
- Add a cabinet API (`/api/v1/...`) with email/password accounts, session cookies, rooms, per-room application lists, room guest sharing, computer management, binding tokens, and an account activity feed
- Replace the shared `TOKEN`/`ADMIN_TOKEN` bearer secrets with per-principal credentials: a session per signed-in user and a per-machine agent token minted at enrollment (`POST /agent/enroll`, replacing the old `/client/sync` identity parameter)
- Add `guardian-server migrate`, running schema migrations (goose) under a separate owner role; the running service's own database role can read and write but cannot alter the schema
- Remove `server/db.go`, the old `applications`/`server`/`client`/`computers` SQLite tables and in-memory caches, and the `modernc.org/sqlite` dependency
- The agent's own wire format (`/agent/sync`'s `applications`/`mode`/`client` shape) is unchanged on purpose, so moving the agent over was a URL and credential change rather than a protocol change. **No previously deployed client works against this server**, and both have since been rewritten in this release (see Agent and Mobile below): the route moved from `/client/sync` to `/agent/sync` and the shared `TOKEN` to a per-machine one, and the Flutter app's `/manage/*` calls with `ADMIN_TOKEN` have no endpoints left to reach. Upgrading means reinstalling the agent from an installer carrying a binding token, and signing in on the phone
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
- Guard the account-wide API with a `ManagerOnly` route-group middleware instead of a `requireManager` line each handler had to remember — the omission that two rulings had already fixed twice. A test walks the router and fails on any route nobody has classified as guest-reachable or manager-only
- Build responses from types in `server/responses.go` rather than returning database rows: the schema is no longer the wire contract, and `account_id` is gone from every response body (it was never accepted in a request either)
- Add the endpoints the model already implied but nothing exposed: `PATCH` an application to switch a rule off without deleting it (the `enabled` column could never be cleared), `GET` and `DELETE` a computer (unenrolling revokes that one machine's agent token — the per-machine counterpart to the account-wide token kill switch), and `/api/v1/account/members` to grant and revoke the `admin` role, which had existed in the schema with no way to give it to anybody
- Record an event for every account-changing operation rather than enrollment alone, each in the same transaction as the change it describes; computer events compare the row before and after, so a PATCH that changes nothing records nothing
- Page `/api/v1/events` by `(created_at, id)` with an opaque cursor instead of returning a fixed 200 rows
- Require `CABINET_ORIGIN` instead of defaulting to `http://localhost:5173`: unset in production, that default let a page on a developer's machine act as any signed-in user
- Cap session renewal at a year from creation (a sliding TTL alone never expires) and purge expired sessions and events older than 180 days hourly; event retention runs through a `SECURITY DEFINER` function because RLS blocks an unscoped delete
- Throttle the telemetry write on `/agent/sync` to once per 30 seconds per machine — it was rewriting a jsonb column on every poll of every agent
- Validate the email as an address rather than looking for an `@`, and bound the password before it reaches argon2id
- Record a re-enrollment as `computer.token_rotated` rather than `computer.enrolled`: it takes a live machine's credential, which is a reinstall when you meant it and a leaked binding token when you did not
- `/api/v1/me` lists each account once, with its name: the union behind it returned one row per path, so an admin who was also a guest of one of that account's rooms saw it twice and no switcher could label either

### Deployment
- `dist/server/docker-compose.yml` adds Caddy (automatic TLS for `GUARDIAN_DOMAIN`, the one trusted proxy hop) and stops publishing the server's port directly; the server has a compose healthcheck
- `dist/server/Dockerfile` runs as a non-root user and ships CA certificates; `server/build.sh` builds a static Linux binary (`CGO_ENABLED=0`) so it actually starts on Alpine
- `guardian-server.service` runs as an unprivileged `guardian` user under systemd sandboxing; `install.sh` creates that user, keeps an existing `.env` on re-runs, and restricts `.env` to `root:guardian 0640`
- Add CI (`.github/workflows/`): `server.yml` (gofmt, vet, sqlc drift, the full suite against Postgres 16, the static release build), `agent.yml` (vet, tests, and the two Windows cross-builds it ships), and `mobile.yml` (format, analyze, test)

### Agent (rewrite for the multi-tenant server)
- Enroll once with the cabinet's `BINDING_TOKEN` (`POST /agent/enroll`), save a per-machine token to `agent_credentials.json`, and delete the binding token from `.env`; sync with `POST /agent/sync`. `TOKEN` and `IDENTITY` are gone, and with them the hardcoded fallback token
- Machine identity is the OS's stable id (Windows `MachineGuid`, `/etc/machine-id`, macOS `IOPlatformUUID`), so a reinstall re-enrolls the same computer instead of adding a duplicate
- One run loop for console and service mode instead of two diverging copies; the shared policy is mutex-guarded (the documented data race is gone); enrollment refusals (token revoked, plan full, no connection) are three distinct messages; a revoked token keeps the last policy in force
- Add `-version`; `agentVersion` is stampable with `-ldflags`
- Add unit tests (`agent/agent_test.go`) — enrollment, the binding-token wipe, revocation, credentials for another server — that run on any platform
- Console (`tools/whitelist-gui`) and the PowerShell installers collect `BINDING_TOKEN` instead of `TOKEN`/`IDENTITY`; the console reports "enrolled" from `agent_credentials.json` and does not require a token to reinstall over an enrolled agent

### Mobile (rewrite)
- Sign in as a person instead of pasting a shared `ADMIN_TOKEN`: `POST /api/v1/auth/login` with `"client": "mobile"`, the session token in `shared_preferences`, and a sign-out that ends the session server-side. Registration is in the app, so a phone can be the first client an account ever has
- Follow the domain to rooms: **Rules** and **Room** are scoped to a room chosen in the app bar, **Computers** manages the account's pool (assign to a room, lock, rename, unenrol, mint an installer token), and Settings switches between the accounts a user can act in (`X-Guardian-Account`)
- Surface failures instead of swallowing them: every call raises `ApiException` carrying the server's own message, where the previous client returned `false` or an empty list and made an outage look like an empty account
- Add tests (`mobile/test/`): the API contract against a mock HTTP client, and a login-screen widget test. Neither needs an Android SDK or a server
- Add `specs/mobile.md`

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
