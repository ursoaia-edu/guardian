# Server Architecture Specification

## Overview

The ProcSentinel server is a Go REST API for managing applications, computer status, and client commands. It uses SQLite for persistence, in-memory caches for fast reads, and chi for HTTP routing.

## File Structure

| File            | Purpose                                        |
|-----------------|-------------------------------------------------|
| `main.go`       | Server struct, startup, graceful shutdown       |
| `models.go`     | Request/response structs                        |
| `db.go`         | Database init, schema, migrations, cache loading, queries |
| `handlers.go`   | HTTP handler functions                          |
| `routes.go`     | Chi router setup, middleware wiring             |
| `middleware.go`  | Auth middleware (constant-time token compare)   |
| `helpers.go`    | Utilities: JSON writer, env loader, IP detect   |

## Core Struct

```go
type Server struct {
    mu           sync.RWMutex
    db           *sql.DB
    appsCache    map[string]Application
    enabledCache bool
    modeCache    string
    clientCache  map[string]bool
}
```

All state access is serialized through `sync.RWMutex`. Reads acquire `RLock`, writes acquire full `Lock`.

## Database Schema

**SQLite file:** `./guardian.db`

### `applications`
| Column     | Type     | Notes                          |
|------------|----------|--------------------------------|
| id         | INTEGER  | PRIMARY KEY AUTOINCREMENT      |
| name       | TEXT     | NOT NULL                       |
| enabled    | BOOLEAN  | DEFAULT 1                      |
| mode       | TEXT     | NOT NULL, DEFAULT 'blacklist'  |
| created_at | DATETIME | DEFAULT CURRENT_TIMESTAMP      |

Constraint: `UNIQUE(name, mode)` — same app name can exist in both blacklist and whitelist modes.
Index: `idx_apps_name_mode` on `(name, mode)`

Mode values: `blacklist` (kill matching processes) or `whitelist` (kill everything except matching processes).

### `server`
| Column     | Type     | Notes                            |
|------------|----------|----------------------------------|
| id         | INTEGER  | PRIMARY KEY, CHECK (id = 1)      |
| enabled    | BOOLEAN  | DEFAULT 0                        |
| mode       | TEXT     | NOT NULL, DEFAULT 'blacklist'    |
| updated_at | DATETIME | DEFAULT CURRENT_TIMESTAMP        |

Single-row table (id always 1). Controls whether the server is "active" and which mode (blacklist/whitelist) is in effect.

### `client`
| Column     | Type     | Notes                          |
|------------|----------|--------------------------------|
| id         | INTEGER  | PRIMARY KEY AUTOINCREMENT      |
| name       | TEXT     | UNIQUE NOT NULL                |
| status     | BOOLEAN  | DEFAULT 1                      |
| created_at | DATETIME | DEFAULT CURRENT_TIMESTAMP      |
| updated_at | DATETIME | DEFAULT CURRENT_TIMESTAMP      |

Index: `idx_client_name` on `(name)`
Seeded with `('power', 1)` on init.

### `computers`
| Column   | Type     | Notes                          |
|----------|----------|--------------------------------|
| identity | INTEGER  | PRIMARY KEY                    |
| blocked  | BOOLEAN  | DEFAULT 0                      |
| datetime | DATETIME | DEFAULT CURRENT_TIMESTAMP      |

Index: `idx_computers_identity` on `(identity)`

## Database Migration

On startup, the server checks for an old `idx_apps_name` index (from the previous `UNIQUE(name)` constraint). If found, it recreates the `applications` table with the new `UNIQUE(name, mode)` constraint in a transaction.

## Caching Strategy

On startup, the server loads all data from SQLite into in-memory structures:
- `appsCache` — full `Application` structs keyed by `"name:mode"` (id, name, enabled, mode)
- `enabledCache` — server enabled/disabled flag
- `modeCache` — server mode (blacklist/whitelist)
- `clientCache` — client entry name-to-status map

**Write-through:** Every mutation updates both the cache and the database within the same lock scope. The client sync endpoint serves directly from the cache without hitting SQLite.

## Startup Behavior

1. Load `.env` file if present
2. Open SQLite database (`./guardian.db`)
3. Create tables if not exist (idempotent)
4. Run migrations if needed
5. Load all data into caches
6. Start HTTP server with graceful shutdown support

The server preserves its enabled/disabled state across restarts (reads from database). The database defaults to `enabled = 0` on first run.

## Graceful Shutdown

The server listens for `SIGINT`/`SIGTERM` and shuts down with a 5-second timeout, allowing in-flight requests to complete and ensuring the database connection is closed cleanly.

## Authentication

Two tiers of Bearer token auth implemented as chi middleware with constant-time comparison:

| Middleware    | Env Var       | Fallback Token                         | Used By               |
|---------------|---------------|----------------------------------------|----------------------|
| `ClientAuth`  | `TOKEN`       | `mILp9n6shk3G9SGSaS2nmP6YlLHwsP1Z`   | Agent/client endpoints|
| `AdminAuth`   | `ADMIN_TOKEN` | `mILp9n6shk3G9SGSaS2nmP6YlLHwsP1Z`   | Management endpoints  |

Header format: `Authorization: Bearer <token>`

## API Endpoints

### Unauthenticated

#### `GET /health`
Returns `{"status": "ok"}`. No auth required (suitable for load balancer probes).

### Client Endpoints (TOKEN auth)

#### `GET /client/sync`
Returns the full state an agent needs: applications filtered by current mode, server mode, and client entries.

**Query params:**
- `identity` (optional) — integer computer ID. When provided, the server:
  1. Checks if that computer is blocked
  2. Updates the computer's `datetime` (heartbeat)

**Response:**
```json
{
  "applications": [{"name": "firefox", "mode": "blacklist"}],
  "mode": "blacklist",
  "client": [{"name": "power", "status": true}]
}
```

**Logic:**
- If server is disabled OR computer has `blocked: false` → `applications` and `client` arrays are empty, mode is `"free"`
- If active → returns only enabled applications whose `mode` matches the server's current mode
- `mode` — the server's current mode, or `"free"` if disabled/unblocked
- `client` — client entries (e.g. power status) for the agent to act on

### Management Endpoints (ADMIN_TOKEN auth)

#### `GET /manage/applications`
Returns all applications with full details (id, name, enabled, mode). Always returns the complete list regardless of server enabled state.

#### `POST /manage/applications`
Add a new application. Body: `{"name": "app.exe", "mode": "blacklist"}`. Mode defaults to `blacklist` if omitted. Returns 201 on success.

#### `PUT /manage/applications`
Update an application's enabled state. Body: `{"name": "app.exe", "enabled": true|false|0|1, "mode": "whitelist"}`. The `enabled` field accepts both boolean and numeric types. Mode is optional — used to find the specific entry.

#### `DELETE /manage/applications`
Remove an application. Body: `{"name": "app.exe", "mode": "blacklist"}`. If mode is omitted, removes all entries with that name.

#### `DELETE /manage/applications/reset`
Remove all applications.

#### `GET /status`
Returns `{"enabled": bool, "mode": "blacklist|whitelist"}`.

#### `PUT /status`
Set server enabled/disabled and/or mode. Body: `{"enabled": true, "mode": "whitelist"}`. Mode is optional — only updated if provided.

#### `GET /info`
Returns server IP (auto-detected), port (`8080`), version (`1.0.0`), status, and mode.

#### `GET /client`
Returns all client entries as `{"entries": [{"name": "power", "status": true}]}`.

#### `PUT /client`
Create or update a client entry. Body: `{"name": "power", "status": false}`.

#### `GET /manage/computers`
Returns all computers with identity, blocked status, last-seen datetime, and current server time.

#### `PUT /manage/computers`
Update a computer's blocked status. Body: `{"identity": 1, "blocked": true}`. Uses `INSERT OR REPLACE`.

#### `DELETE /manage/computers/reset`
Unblock all computers (set `blocked = 0`).

#### `PUT /manage/computers/block_all`
Block all computers (set `blocked = 1`).

## Configuration

Loaded from `.env` file in working directory. Environment variables take precedence over `.env` values.

| Variable         | Purpose                                      | Default                     |
|------------------|----------------------------------------------|-----------------------------|
| `SERVER_ADDRESS` | Listen address (full URL or `host:port`)     | `0.0.0.0:8080`             |
| `TOKEN`          | Client/agent auth token                      | hardcoded fallback          |
| `ADMIN_TOKEN`    | Management auth token                        | hardcoded fallback          |

## HTTP Server Settings

| Setting        | Value      |
|----------------|------------|
| Read timeout   | 15 seconds |
| Write timeout  | 15 seconds |
| Idle timeout   | 60 seconds |

## Deployment

- Installs to `/usr/local/bin/procsentinel/` as a systemd service
- Database file created in working directory (`./guardian.db`)
- Listens on port 8080 by default
- No CGO required (`modernc.org/sqlite` is pure Go)

## Key Dependencies

| Package                  | Purpose                        |
|--------------------------|--------------------------------|
| `modernc.org/sqlite`     | Pure-Go SQLite driver          |
| `github.com/go-chi/chi`  | HTTP router with method routing|
| `github.com/go-chi/cors` | CORS middleware                |
| `log/slog` (stdlib)      | Structured JSON logging        |

## Key Design Decisions

1. **Multi-file layout** — split by concern: models, db, handlers, routes, middleware, helpers
2. **Write-through cache** — mutations update both cache and DB under lock; reads served from cache
3. **Dual auth tiers** — agents use a simpler token; management uses an admin token
4. **Constant-time token comparison** — prevents timing side-channel attacks
5. **Unauthenticated health check** — for load balancer and monitoring probes
6. **CORS enabled** — allows cross-origin requests from web and mobile clients
7. **Graceful shutdown** — clean connection drain on SIGINT/SIGTERM
8. **Computer heartbeat** — agents report identity on each poll, server tracks last-seen time
9. **Mode-filtered sync** — `/client/sync` only returns apps matching the server's current mode
10. **Computer-gated sync** — unblocked computers (`blocked: false`) receive empty applications and client arrays
11. **UNIQUE(name, mode)** — same app can exist in both blacklist and whitelist lists independently
