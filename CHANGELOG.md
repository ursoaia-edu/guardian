# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

### Server (rewrite)
- Replace the single-tenant SQLite server with a multi-tenant PostgreSQL 16 backend: any number of customer accounts now share one database, kept apart by row-level security rather than by running a separate server per customer
- Add a cabinet API (`/api/v1/...`) with email/password accounts, session cookies, rooms, per-room application lists, room guest sharing, computer management, binding tokens, and an account activity feed
- Replace the shared `TOKEN`/`ADMIN_TOKEN` bearer secrets with per-principal credentials: a session per signed-in user and a per-machine agent token minted at enrollment (`POST /agent/enroll`, replacing the old `/client/sync` identity parameter)
- Add `guardian-server migrate`, running schema migrations (goose) under a separate owner role; the running service's own database role can read and write but cannot alter the schema
- Remove `server/db.go`, the old `applications`/`server`/`client`/`computers` SQLite tables and in-memory caches, and the `modernc.org/sqlite` dependency
- The agent's own wire format (`/agent/sync`'s `applications`/`mode`/`client` shape) is unchanged on purpose, so agents already in the field keep working
- Tighten CORS to an explicit, credentialed `CABINET_ORIGIN` allow-list instead of a wildcard

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
