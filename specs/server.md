# Server Architecture Specification

## Overview

The Guardian server is a multi-tenant Go REST API backed by PostgreSQL 16. Any
number of customer accounts share one database; row-level security (RLS) is
what keeps one account's data invisible to another, not application-level
filtering. There is no per-deployment SQLite file and no shared secret token —
every account authenticates its own users (via a session) and its own agents
(via a per-machine token minted at enrollment).

## File Structure

| File                    | Purpose                                                        |
|-------------------------|-----------------------------------------------------------------|
| `main.go`                | `Server` struct (a pgx pool), startup, `migrate` subcommand, graceful shutdown |
| `routes.go`              | chi router setup: CORS restricted to `CABINET_ORIGIN`, route groups |
| `middleware.go`          | `SessionAuth` (cabinet) and `AgentAuth` (agents)                |
| `tenant.go`              | `Tenant`, request-context plumbing, `inAccount` (RLS scoping)   |
| `auth.go`                | argon2id password hashing, session/agent/binding token minting |
| `handlers_auth.go`       | register, login, logout, `/api/v1/me`                          |
| `handlers_rooms.go`      | room CRUD, per-room application lists                          |
| `handlers_members.go`    | room membership (guest grants), `requireManager`, `assertRoomVisible` |
| `handlers_computers.go`  | computer listing and reassignment/blocking                     |
| `handlers_agent.go`      | binding tokens, agent enrollment, agent sync                   |
| `events.go`              | account activity feed                                          |
| `handlers.go`            | `/health` only                                                 |
| `migrate.go`             | embeds and runs `db/migrations/*.sql` via goose                |
| `models.go`              | the agent's wire format (`ClientApplication`, `ClientEntry`, `ClientSyncResponse`) plus `ErrorResponse` |
| `helpers.go`             | JSON writer, `.env` loader, small utilities                    |
| `db/migrations/`         | goose SQL migrations — schema owned by `guardian_owner`        |
| `db/queries/`            | sqlc query sources                                              |
| `internal/db/`           | generated sqlc code (see `sqlc.yaml`)                           |
| `fakeagent/`             | a CLI that enrolls and syncs like the Windows agent, for exercising the API without a real machine |

## Core Struct

```go
type Server struct {
    pool *pgxpool.Pool
}
```

The server holds nothing else — no cache, no mutex, no per-request state.
Every handler reads and writes through the pool, inside a transaction scoped
to one account (`s.inAccount`, see below).

## Authentication

There are exactly two ways in, and no shared secret anywhere:

### 1. Session auth (the cabinet) — `SessionAuth` middleware

- The caller presents a session token either as the `guardian_session` cookie
  (browser) or as `Authorization: Bearer <token>` (mobile, which cannot use a
  cookie jar comfortably). `POST /api/v1/auth/login` hands out the same
  plaintext token both ways: as the cookie and in the response body.
- The plaintext is never stored — only its SHA-256 digest, in `sessions`.
  Sessions use a sliding 30-day TTL: any authenticated request touches
  `last_used_at`/`expires_at` forward, so an account in active use is never
  signed out mid-session.
- `SessionAuth` resolves the session to a user, then to the accounts that
  user can act in (`ListAccessibleAccounts`, see **Roles** below), and puts a
  `Tenant{AccountID, UserID, Role}` in the request context. `account_id`
  comes from nowhere else in the codebase — no handler ever reads it from a
  path, query string, or body.

#### `X-Guardian-Account`

A user can belong to more than one account: they own their own, and may also
be a guest in a room shared by someone else's. Picking `accounts[0]` (owner
sorts first) would silently strand a guest in their own empty account and
they would never see the room they were given. So the caller names which of
*their already-proven* memberships to act in:

```
X-Guardian-Account: <account-id>
```

This is deliberately not a violation of "account_id never comes from a
request". The constraint exists to stop a caller reaching an account they
have no claim on. The header cannot do that: `SessionAuth` has already loaded
the full list of accounts this session's user may enter, and the header only
*selects* among that list. A value that doesn't match one of them is a 404,
exactly like a foreign resource — whether an account with that id even
exists is not something a non-member is entitled to learn. Omitting the
header keeps the pre-9.0 default of `accounts[0]`.

### 2. Agent auth — `AgentAuth` middleware

- The agent presents `Authorization: Bearer <agent-token>`, a token unique to
  that machine, handed to it by `POST /agent/enroll` and never seen by the
  cabinet. There is no shared `TOKEN` env var to leak from an installer.
- Resolving the token to an account happens in two steps, and the split is
  deliberate (see `AgentAuth` in `middleware.go` for the full reasoning):
  1. `account_for_agent_token(hash)` — a `SECURITY DEFINER` SQL function
     (migration `00010_agent_lookup.sql`) that returns a bare account UUID
     for a token hash, or SQL `NULL` for an unknown one. This is the *only*
     lookup in the whole request path that runs before any account scope
     exists.
  2. Everything else — reading the actual `computers` row, and every query
     the handler goes on to make — runs inside `s.inAccount`, scoped and
     RLS-checked like any other request.
- `AgentAuth` puts `Tenant{AccountID}` (no user, no role) and the resolved
  `db.Computer` in the request context.

## Row-Level Security

RLS, not handler code, is the tenancy boundary. Every multi-tenant table has
`ENABLE ROW LEVEL SECURITY` and a policy of the shape:

```sql
USING (account_id = current_account_id()) WITH CHECK (account_id = current_account_id())
```

`current_account_id()` reads the `app.account_id` Postgres GUC:

```sql
CREATE FUNCTION current_account_id() RETURNS UUID
LANGUAGE sql STABLE SET search_path = pg_catalog AS $$
    SELECT NULLIF(current_setting('app.account_id', true), '')::uuid
$$;
```

`s.inAccount(ctx, accountID, fn)` (`tenant.go`) is the only place that GUC is
ever set, and it sets it with `set_config('app.account_id', id, true)` — the
third argument makes the setting local to the transaction, so a connection
handed back to a pgx pool can never leak one request's scope into the next
request that happens to reuse it. A connection that never calls `inAccount`
sees nothing at all in any scoped table: `current_setting(..., true)` returns
NULL when unset, and `NULL = anything` is never true in SQL.

### The application role owns nothing

`guardian_app` — the role in `DATABASE_URL`, the one the running service
actually connects as — does **not** own any table and does **not** have
`BYPASSRLS`. It is granted `SELECT, INSERT, UPDATE, DELETE` on the schema (via
`ALTER DEFAULT PRIVILEGES`, set once in `00001_init.sql` so every table every
later migration creates inherits the grant automatically) and nothing more:
no `CREATE`, no `ALTER`, no `DROP`. This is what makes RLS an actual barrier
rather than a suggestion — a role with `BYPASSRLS` (superusers, and table
owners by default) ignores every policy above unconditionally, so the whole
scheme depends on the connection the server holds not being one.

### The deliberate pre-scope exceptions

Two situations legitimately have no account scope yet, because the scope
itself hasn't been determined:

- **`accounts`** — `accounts_prescope` (unscoped `SELECT`) and
  `accounts_insert` (unscoped `INSERT`). Registration has to create the very
  first row for a brand-new account before any scope can be set on it, and a
  session has to be able to discover which accounts it may enter.
- **`account_members`** — `account_members_prescope` (unscoped `SELECT`
  only). There is **no** unscoped `INSERT` policy: an unscoped insert into
  `account_members` would let any code path make an arbitrary user the owner
  of an arbitrary account, i.e. privilege escalation written into the schema.
  Registration sets the account scope the instant it has the new account's
  id, and only then inserts the owner membership row — that insert has to
  earn its scope like every other write.
- **`room_members`** (added in `00013_room_member_lookup.sql`) —
  `room_members_prescope` (unscoped `SELECT` only), for the same reason as
  `accounts`: a guest's account is discovered from their room grants before
  any scope exists.
- **`binding_tokens`** — `binding_tokens_lookup` (unscoped `SELECT` only).
  Enrollment names the account by the plaintext binding token in the request
  body, not by anything the caller could assert directly, so the lookup by
  hash has to run before a scope exists. Be precise about how big this hole
  is: an unscoped connection can enumerate every account's token *rows* —
  ids, account ids, lifetimes, digests — across every customer. What it
  cannot do is *use* any of them: the digests are SHA-256 of 32 random
  bytes, and enrollment needs the plaintext. The account-wide revoke is a
  plain `UPDATE`, which stays confined by the ordinary isolation policy —
  only the `SELECT` is unscoped.

Every one of these is a deliberate, narrow, and reasoned-about hole — not an
oversight. Adding a table to this list is a decision that needs the same
justification as the four above, not a default.

### `account_for_agent_token`: why a function instead of a policy

The obvious alternative to the function described under **Agent auth** would
be an unscoped `SELECT` policy on `computers`, mirroring
`binding_tokens_lookup`. That was rejected: a `computers` row carries far
more than a token digest — hostname, OS build, hardware and live runtime
telemetry for every machine on every account. An unscoped read policy on it
would let any unauthenticated connection enumerate that for the entire
fleet, not just look up one account id. `account_for_agent_token` is the
narrow alternative: it runs `SECURITY DEFINER` (as the table owner, so it is
not itself subject to RLS) but its return type is one bare `uuid` — the
answer to "whose token is this?" and nothing else. Its `search_path` is
pinned to `pg_catalog, public` so it cannot be redirected by a session that
has tampered with its own search path — a `SECURITY DEFINER` function that
resolves unqualified names through the caller's search path is a
privilege-escalation primitive, so this is pinned deliberately.

## Roles

There are two roles at the account level and one at the room level:

- **`owner`** — the user who registered the account. Set once, at
  registration, and stored in `account_members`.
- **`admin`** — added by an owner or another admin. Functionally identical to
  `owner` everywhere in the codebase today (`requireManager` allow-lists both
  under a single "may manage this account" check); the distinction exists in
  the schema for future use (billing, account deletion) even though no
  handler currently treats them differently.
- **`member`** (room guest) — not a row in `account_members` at all. A user
  becomes a guest of an account by being granted access to one specific room
  (`room_members`, whose own `role` column is `CHECK (role IN ('member'))` —
  there is only one room-level role). `ListAccessibleAccounts` (in
  `db/queries/room_members.sql`) unions `account_members` with a derived
  `'member'` row per account reachable through a room grant, so a guest shows
  up in `/api/v1/me`'s `accounts` list with `role: "member"` even though they
  have no `account_members` row for that account at all. Where a user is
  both e.g. an `admin` of their own account and a `member`-guest of someone
  else's, the two are entirely separate `Tenant`s selected by
  `X-Guardian-Account` — there is no "strongest role wins" across accounts,
  only within the ranking of the `UNION` for a single account id.

### What a guest can reach

`requireManager` (`handlers_members.go`) rejects `member` from every
account-wide write: creating or deleting rooms, adding or removing room
members, minting or revoking binding tokens, and listing the events feed.
Within the rooms they *have* been granted, a guest can:

- see only those rooms (`ListRoomsForMember`) and only those rooms'
  computers (`ListComputersForMember`), not the account's full lists;
- open, but not delete, an individual granted room (`assertRoomVisible`
  gates `GetRoom`/`PatchRoom`/room-applications endpoints on `IsRoomMember`
  for a `member` caller — an ungranted room and a foreign room are both a
  404, indistinguishable from outside);
- move a computer *within* their granted rooms, but not pull it out of every
  room altogether (`SetRoom && RoomID == nil` is rejected for a `member` —
  unassigning a computer is an account-wide act, not something a guest does
  to a machine that happens to sit in their room right now) and not move it
  into a room they were not granted (`assertRoomVisible`, not the weaker
  `GetRoom`, gates the target room on a computer PATCH).

## Database Schema

PostgreSQL 16. Every table below except `users` and `sessions` carries an
`account_id UUID NOT NULL REFERENCES accounts(id)` and an RLS policy as
described above.

### `users`
Email/password identity, independent of any account. `email` is globally
unique. Passwords are argon2id (`$argon2id$v=19$m=65536,t=1,p=4$<salt>$<key>`
— cost parameters travel with the hash, so raising them later needs no
migration of stored passwords).

### `accounts`
`id`, `name`, `owner_user_id`, `plan` (default `'free'`), `computer_limit`
(default `3` — enforced at enrollment, see `handleEnroll`), Stripe fields,
`subscription_status`, `grace_until`.

### `account_members`
`(account_id, user_id)` primary key, `role CHECK (role IN ('owner', 'admin'))`.

### `sessions`
`token_hash` (SHA-256 of the plaintext) primary key, `user_id`, `expires_at`,
`last_used_at` (advanced on every authenticated request), `ip`, `user_agent`.

### `rooms`
`id`, `account_id`, `name`, `mode CHECK (mode IN ('blacklist','whitelist'))`,
`protection_enabled` (default `false` — a room enforces nothing until this is
turned on), `power_allowed` (default `true`).

### `room_members`
`(room_id, user_id)` primary key, `account_id` (denormalized for the RLS
policy and the composite FK), `role CHECK (role IN ('member'))`.

### `applications`
Per-room blacklist/whitelist entries. `id`, `account_id`, `room_id`, `name`,
`list CHECK (list IN ('blacklist','whitelist'))`, `enabled`.
`UNIQUE (room_id, name, list)` — the same name can exist in both lists of the
same room independently, exactly as the single-tenant server allowed per
(name, mode).

### `computers`
`id`, `account_id`, `room_id` (nullable — unassigned means "enforce
nothing"), `display_name`, `machine_guid`, `hostname`, `os_name`, `os_build`,
`arch`, `agent_version`, `hardware`/`runtime` (`jsonb`), `token_hash`
(the agent's credential digest — tagged `json:"-"` in the generated struct so
it can never leak through a handler that returns a `Computer` verbatim),
`blocked`, `enrolled_at`, `last_seen_at`. `UNIQUE (account_id, machine_guid)`
so a reinstall on the same machine re-enrolls the same row instead of
consuming a new seat. A composite FK (`account_id, room_id) REFERENCES
rooms (account_id, id)`, added in `00012_room_account_consistency.sql`,
enforces at the database level that a computer can never point at a room
belonging to a different account — RLS alone cannot express this, because a
policy on `computers` only ever tests that row's own `account_id`, never the
`account_id` of the room it references.

### `binding_tokens`
The token an installer carries, shared by every machine downloaded from one
account's cabinet. `id`, `account_id`, `token_hash` (unique), `expires_at`
(1 year), `revoked_at`. Revoking is a single account-wide `UPDATE`; per-machine
credentials (`computers.token_hash`) are unaffected and are revoked one at a
time by deleting the computer.

### `events`
An append-only activity feed. `id`, `account_id`, `room_id`/`computer_id`
(both nullable, `ON DELETE SET NULL`), `type`, `payload jsonb`, `created_at`.
Indexed `(account_id, created_at DESC)` for the recent-first listing.
Currently only `computer.enrolled` is recorded (`handleEnroll`); more event
types are expected to be added the same way, inside the same transaction as
the change they describe (`recordEvent` takes the caller's `pgx.Tx`).

### Migration 00004

There is no `00004_*.sql`. It was retired during development and the number
is permanently skipped rather than reused — goose orders by number, so
renumbering a later migration down to fill the gap would silently reorder
history on any database that had already applied the migrations under their
original numbers. Do not create a new `00004_*.sql`; the next migration after
`00013` is `00014`.

## Migrations

`go run . migrate` (packaged as `guardian-server migrate`) applies every
pending file under `db/migrations/` with goose, using `MIGRATE_DATABASE_URL`.
That DSN must be the **owner** role (`guardian_owner` in the dev
compose file) — the one that actually owns the tables and may run `CREATE`/
`ALTER`/`DROP`. The service's own connection role, `guardian_app`
(`DATABASE_URL`), is deliberately unable to alter the schema at all: the only
DDL-capable path into this database is a human (or a deploy pipeline)
running the `migrate` subcommand once, before the service using
`DATABASE_URL` is (re)started. See `server/build.sh` for the deploy-order
comment.

## Startup Behavior

1. `go run . migrate` — separate invocation, exits after applying migrations
2. Normal startup: load `.env` if present, open a `pgxpool.Pool` against
   `DATABASE_URL`, ping it (5s timeout), start the HTTP server
3. Graceful shutdown on `SIGINT`/`SIGTERM`, 5-second drain, then the pool is
   closed

## API Endpoints

See `specs/api.md` for the full request/response reference. Summary, exactly
as wired in `routes.go`:

| Route                                          | Auth           |
|-------------------------------------------------|----------------|
| `GET /health`                                    | none           |
| `POST /agent/enroll`                             | binding token (in body) |
| `POST /api/v1/auth/register`                     | none           |
| `POST /api/v1/auth/login`                        | none           |
| `POST /api/v1/auth/logout`                       | session        |
| `GET /api/v1/me`                                 | session        |
| `GET, POST /api/v1/rooms`                        | session        |
| `GET, PATCH, DELETE /api/v1/rooms/{roomID}`      | session        |
| `GET, POST /api/v1/rooms/{roomID}/applications`  | session        |
| `DELETE /api/v1/rooms/{roomID}/applications/{appID}` | session    |
| `GET, POST /api/v1/rooms/{roomID}/members`       | session        |
| `DELETE /api/v1/rooms/{roomID}/members/{userID}` | session        |
| `GET /api/v1/computers`                          | session        |
| `PATCH /api/v1/computers/{computerID}`           | session        |
| `POST, DELETE /api/v1/binding-tokens`            | session        |
| `GET /api/v1/events`                             | session        |
| `GET /agent/sync`                                | agent token    |

## CORS

```go
origins := []string{"http://localhost:5173"}
if o := os.Getenv("CABINET_ORIGIN"); o != "" {
    origins = strings.Split(o, ",")
}
```

`AllowCredentials: true` with an explicit origin list — never a wildcard. A
wildcard origin cannot legally coexist with credentialed (cookie) requests;
browsers refuse the combination, and allowing it would have meant any site
could act as the signed-in cabinet user.

## Configuration

Loaded from `.env` in the working directory; real environment variables take
precedence.

| Variable                | Purpose                                                     |
|--------------------------|--------------------------------------------------------------|
| `DATABASE_URL`            | `guardian_app` connection string, used by the running service |
| `MIGRATE_DATABASE_URL`    | `guardian_owner` connection string, used only by `guardian-server migrate` |
| `CABINET_ORIGIN`          | comma-separated origins allowed to send credentialed requests |
| `SERVER_ADDRESS`          | listen address (full URL or `host:port`), default `0.0.0.0:8080` |

## HTTP Server Settings

| Setting        | Value      |
|----------------|------------|
| Read timeout   | 15 seconds |
| Write timeout  | 15 seconds |
| Idle timeout   | 60 seconds |

## Key Dependencies

| Package                     | Purpose                                   |
|-------------------------------|--------------------------------------------|
| `github.com/jackc/pgx/v5`      | PostgreSQL driver and connection pool     |
| `github.com/pressly/goose/v3`  | SQL migrations                            |
| `github.com/go-chi/chi/v5`     | HTTP router                               |
| `github.com/go-chi/cors`       | CORS middleware                           |
| `golang.org/x/crypto`          | argon2id password hashing                 |
| `github.com/google/uuid`       | account/user/room/computer identifiers    |
| `log/slog` (stdlib)            | structured JSON logging                   |

sqlc (`sqlc.yaml`) generates `internal/db/` from `db/queries/*.sql` against
the schema in `db/migrations/*.sql`; the generated code is committed, not
built on the fly.

## Testing

Every test in `server/*_test.go` runs against a live Postgres — there is no
mock or in-memory substitute for RLS, because RLS is exactly the thing being
tested. `TEST_DATABASE_URL` (owner role, used to set up fixtures) and
`TEST_APP_DATABASE_URL` (the `guardian_app` role, used to exercise the actual
request path) must both be exported or the suite skips silently. See
`server/docker-compose.dev.yml` for a local Postgres 16.

`isolation_test.go` is the load-bearing test: every account-scoped table gets
a row asserting that a second account's connection cannot read or write it.
A new table without a corresponding row there is an unreviewed tenancy claim.

## Key Design Decisions

1. **RLS is the tenancy boundary, not handler code.** Every handler still
   scopes its own queries via `inAccount`, but a bug that forgot to would
   fail closed (empty result / no-op write) rather than leaking another
   account's rows, because the connection itself cannot see them.
2. **The application role cannot alter its own schema.** Migrations run
   under a separate owner role, invoked explicitly and out of band from
   normal service startup.
3. **No shared secret token anywhere.** Every session and every agent
   credential is minted per-principal (`newToken`, 32 random bytes, SHA-256
   digest stored) and can be revoked without affecting anyone else's.
4. **Constant-time-equivalent login.** Login always spends the same argon2
   work whether the email exists or not (`dummyPasswordHash`), so a login
   response cannot be used to enumerate registered emails by timing.
5. **`account_id` is resolved server-side, once, per request.** It is never
   accepted as a path, query, or body parameter; `X-Guardian-Account`
   *selects* among already-proven memberships rather than asserting one.
6. **A room enforces nothing until explicitly turned on** (`protection_enabled`)
   and an unassigned computer enforces nothing at all — a freshly enrolled
   machine, or a freshly created room, defaults to inert rather than locked.
7. **A blocked computer is locked, not unmanaged.** `/agent/sync` for a
   blocked machine answers `whitelist` mode with an empty allow-list, not
   `free` — "blocked" and "no policy at all" must never be the same wire
   response.
8. **CORS is an explicit allow-list with credentials, never a wildcard.**
