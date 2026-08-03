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
| `tenant.go`              | `Tenant`, request-context plumbing, `inAccount`/`inUser` (RLS scoping) |
| `auth.go`                | argon2id password hashing, session/agent/binding token minting |
| `handlers_auth.go`       | register, login, logout, `/api/v1/me`                          |
| `handlers_rooms.go`      | room CRUD, per-room application lists                          |
| `handlers_members.go`    | room membership (guest grants), `requireManager`, `assertRoomVisible` |
| `handlers_computers.go`  | computer listing and reassignment/blocking                     |
| `handlers_agent.go`      | binding tokens, agent enrollment, agent sync                   |
| `events.go`              | account activity feed                                          |
| `handlers.go`            | `/health` only (pings Postgres, see **Health**)                |
| `responses.go`           | the API's own response types — the wire format, kept separate from the schema |
| `logging.go`             | `clientIP` (proxy-aware address resolution), slog request logger, panic recoverer |
| `maintenance.go`         | the hourly purge of expired sessions and aged-out events        |
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
    pool           *pgxpool.Pool
    trustedProxies int // from TRUSTED_PROXIES; see "Client IP resolution"
}
```

The server holds nothing else — no cache, no mutex, no per-request state.
Every handler reads and writes through the pool, inside a transaction scoped
to one account (`s.inAccount`, see below). `trustedProxies` is configuration
read once at startup, not state.

## Authentication

There are exactly two ways in, and no shared secret anywhere:

### 1. Session auth (the cabinet) — `SessionAuth` middleware

- The caller presents a session token either as the `guardian_session` cookie
  (browser) or as `Authorization: Bearer <token>` (mobile, which cannot use a
  cookie jar comfortably). `POST /api/v1/auth/login` always sets the cookie,
  and returns the same plaintext token in the response body **only** when the
  request asks for it with `"client": "mobile"`. Returning it
  unconditionally would cancel the `HttpOnly` flag it had just set: script on
  the cabinet's origin could read the token straight out of the login
  response and put it somewhere XSS can reach.
  `TestBrowserLoginDoesNotReturnTheTokenInTheBody` is the guard.
- The plaintext is never stored — only its SHA-256 digest, in `sessions`.
  Sessions use a sliding 30-day TTL: any authenticated request touches
  `last_used_at`/`expires_at` forward (at most hourly), so an account in
  active use is never signed out mid-session. The slide is capped at one year
  from the session's creation: unbounded renewal means a session used once a
  week never expires at all, so a token taken from a device that stays in use
  would be good forever and "sign out everywhere" would be the only
  revocation there is.
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
ever set, and it sets it through `scopeTx`, which uses
`set_config('app.account_id', id, true)` — the third argument makes the
setting local to the transaction, so a connection handed back to a pgx pool
can never leak one request's scope into the next request that happens to
reuse it. `s.inUser` is its counterpart for the second GUC (**The second
GUC** below); both are thin wrappers over `s.inTx`. A connection that never calls `inAccount`
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
itself hasn't been determined: registration creating the very first account,
and a session discovering which accounts it may enter. The policies covering
them are **pre-account, not unscoped** — since `00014_user_scope.sql` each one
also tests `app.user_id` (see **The second GUC** below), so it exposes the
calling user's own rows and nothing else:

- **`accounts`** — `accounts_prescope` (`SELECT` where the row is an account
  the calling user can act in: owned by them, or one they hold an
  `account_members` or `room_members` row for) and `accounts_insert`
  (`INSERT` where `owner_user_id = current_user_id()`). The `SELECT` covers
  registration's `INSERT ... RETURNING` and, since `00015`, the account
  *names* `/api/v1/me` needs for an account switcher — those are read before
  any account scope exists. The `WITH CHECK` means no code path can create an
  account owned by somebody else, even by accident.
- **`account_members`** — `account_members_prescope` (`SELECT` where
  `user_id = current_user_id()`). There is **no** pre-scope `INSERT` policy:
  such an insert would let any code path make an arbitrary user the owner
  of an arbitrary account, i.e. privilege escalation written into the schema.
  Registration sets the account scope the instant it has the new account's
  id, and only then inserts the owner membership row — that insert has to
  earn its scope like every other write.
- **`room_members`** (added in `00013_room_member_lookup.sql`) —
  `room_members_prescope` (`SELECT` where `user_id = current_user_id()`), for
  the same reason as `account_members`: a guest's account is discovered from
  their room grants before any account scope exists.
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

### The second GUC: `app.user_id`

Each pre-scope policy was individually justified against the table it sits
on. What those justifications did not cover was what the holes added up to:
`accounts`, `account_members` and `room_members` were each readable with *no*
predicate at all, and `users` has no RLS (it isn't a multi-tenant table — see
**Database Schema** below). Joined in one statement, a connection that had not
scoped itself could read every customer's email, account name, role and
room-sharing graph across the entire fleet — not one account's worth, all of
them. That was demonstrated live against this database, and
`00014_user_scope.sql` closes it.

The mechanism mirrors `app.account_id` exactly:

```sql
CREATE FUNCTION current_user_id() RETURNS UUID
LANGUAGE sql STABLE SET search_path = pg_catalog AS $$
    SELECT NULLIF(current_setting('app.user_id', true), '')::uuid
$$;
```

`s.inUser(ctx, userID, fn)` (`tenant.go`) is the only place that GUC is set,
and it sets it transaction-locally through the same `scopeTx` helper
`inAccount` uses. Two handlers run inside it — `SessionAuth`'s
`ListAccessibleAccounts` and `handleMe` — and registration sets `app.user_id`
directly on its own transaction the moment the user row exists, before it
creates the account. Anything else that reads these three tables without a
scope now reads **nothing**, which is how every other table in the schema
already behaved. `TestUnscopedConnectionCannotReadTheFleet` and
`TestAccountInsertRequiresTheOwningUsersScope` are the guards.

Two documented exposures deliberately remain, both narrower than what was
closed:

- **`binding_tokens_lookup`** cannot be narrowed by user, because enrollment
  is keyed by a token digest and has no user at all. It exposes token *rows*
  fleet-wide — ids, account ids, lifetimes, digests — but not the ability to
  use one: the digests are SHA-256 of 32 random bytes and enrollment needs
  the plaintext.
- **`users`** has no RLS, because three legitimate paths read a row that is
  not the caller's own: login (before any user id is known), inviting
  somebody to a room by email, and listing a room's members. Narrowing it
  requires `SECURITY DEFINER` lookups for those three — a separate change,
  and a smaller prize than the join that motivated this one, since a `users`
  row on its own carries no account, role or sharing information.

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
- **`admin`** — granted through `POST /api/v1/account/members` by an owner or
  another admin. Functionally identical to `owner` everywhere in the codebase
  today (`ManagerOnly` allow-lists both under a single "may manage this
  account" check); the distinction exists in the schema for future use
  (billing, account deletion) and in one place already: the owner's
  membership row cannot be deleted, so an account always has somebody who can
  be billed. Ownership is not transferable in v1, so there is no way to mint
  a second owner.
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

The API is split into two route groups in `routes.go`, and the split — not a
line inside each handler — is the guard. The account-wide group carries the
`ManagerOnly` middleware (`middleware.go`), so `member` is rejected from
creating or deleting rooms, adding or removing room members, minting or
revoking binding tokens, and listing the events feed.

This was a per-handler `requireManager` call, and it was forgotten twice
(rulings R21 and R22) — both times on a handler written after the guest role
already existed, which is exactly the mistake a per-handler guard invites. As
a group middleware the failure mode inverts: an account-wide route is guarded
by being in the group, and exposing a route to guests is a visible decision.
`TestEveryAPIRouteIsClassified` walks the router and fails on any `/api/v1`
route that appears in neither list, so a new route cannot be added without
someone saying which side it is on; `TestGuestIsRefusedEveryManagerRoute`
drives every account-wide route with a real guest of that account and demands
403.

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

### Email enumeration via add-member (accepted for now, closes in plan 2)

`POST /rooms/{id}/members` (`handleAddRoomMember`) answers 404 for an email
with no Guardian account and 201 for one that has one, so any registered
user can test whether a given address belongs to a Guardian customer by
attempting to add it to a room they manage. This is a known, accepted
tradeoff, not an oversight:

- the endpoint is manager-only and sits behind a registered account — the
  prerequisite for the login oracle R9 closed (any unauthenticated caller,
  no account needed) — so the exposure is materially smaller;
- the 404 is truthful on purpose: the room owner needs to know their
  invitation failed so they can tell the guest to register first, and a
  vaguer "invitation sent either way" answer would silently strand every
  owner who mistyped or misremembered an email;
- the real fix is real email invitations, where "invitation sent" is the
  uniform answer regardless of whether the address already has an account —
  arriving with the cabinet in plan 2, not before.

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

### Telemetry writes are throttled

`TouchComputer` only writes when `last_seen_at` is more than 30 seconds old.
Every agent in the fleet syncs on a timer whether anything changed or not, and
each sync rewrites a `jsonb` column: at a 20-second poll that is three row
versions per machine per minute — all of it WAL and vacuum work — to record a
timestamp nobody reads at that resolution.

### `computers`
`id`, `account_id`, `room_id` (nullable — unassigned means "enforce
nothing"), `display_name`, `machine_guid`, `hostname`, `os_name`, `os_build`,
`arch`, `agent_version`, `hardware`/`runtime` (`jsonb`, each capped at 64 KiB
— see **Rate Limiting and Body Size**), `token_hash`
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

### Re-enrollment rotates a live machine's token

`UpsertComputerByGUID` mints a new agent token whenever a known
`machine_guid` enrolls again, which stops whatever agent was holding the old
one. That is the normal shape of a reinstall. It is also what somebody
holding a leaked binding token can do to a machine that is running perfectly
well: enroll its GUID and take its credential.

Requiring the previous agent token for a re-enrollment was considered and
rejected: the ordinary reinstall — the console stops the service and runs the
installer — has no access to it, so the check would break the common case to
inconvenience an attacker who must already hold an account-wide token. What
is done instead is that the rotation is recorded as its own event type,
`computer.token_rotated`, distinct from `computer.enrolled`, so an owner
watching the feed sees a machine's credential change when nobody was
reinstalling it. The kill switch for the underlying cause is
`DELETE /api/v1/binding-tokens`.

### The seat limit is taken under a lock

`handleEnroll` counts the account's computers and then inserts one. Those are
two statements, and a fleet is installed all at once — an admin runs the
installer across a lab in one sitting — so without a lock held across them
every concurrent enrollment reads the same count, every one decides there is
room, and the account silently ends up over its plan.
`LockAccountComputerLimit` therefore reads `accounts.computer_limit`
`FOR UPDATE` **before** the count, inside the same transaction. The lock is
taken on the account row because that is the thing being rationed, and it
serialises only enrollments of the same account.
`TestConcurrentEnrollmentsCannotExceedThePlanLimit` fires twice as many
simultaneous enrollments as the plan allows and holds the result to exactly
the limit; with the `FOR UPDATE` removed it admits four machines onto a
three-seat plan.

### `binding_tokens`
The token an installer carries, shared by every machine downloaded from one
account's cabinet. `id`, `account_id`, `token_hash` (unique), `expires_at`
(1 year), `revoked_at`. Revoking is a single account-wide `UPDATE`; per-machine
credentials (`computers.token_hash`) are unaffected. There is no
delete-computer endpoint yet to revoke one of those individually —
unenrolling a machine arrives with the cabinet.

### `events`
An append-only activity feed. `id`, `account_id`, `room_id`/`computer_id`
(both nullable, `ON DELETE SET NULL`), `type`, `payload jsonb`, `created_at`.
Indexed `(account_id, created_at DESC)` for the recent-first listing.
Every account-changing handler records one, inside the same transaction as
the change it describes (`recordEvent` takes the caller's `pgx.Tx`), so the
feed cannot disagree with the data it describes — the full list of types is
in `specs/api.md`. Computer events compare the row before and after rather
than trusting the request body: a PATCH that sets `blocked` to the value it
already had is not a blocking, and a feed that says otherwise teaches people
to ignore it.

The feed is paged by `(created_at, id)` — see `ListEventsBefore`. An OFFSET
would repeat or skip rows as events arrive underneath a reader, which for an
activity feed is the normal case rather than the edge one.

There is no retention policy yet: the table grows without bound.

### Migration 00004

There is no `00004_*.sql`. It was retired during development and the number
is permanently skipped rather than reused — goose orders by number, so
renumbering a later migration down to fill the gap would silently reorder
history on any database that had already applied the migrations under their
original numbers. Do not create a new `00004_*.sql`; the next migration after
`00016` is `00017`.

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

### Production role prerequisites

`00001_init.sql` runs `GRANT ... TO guardian_app`, so that role must already
exist the first time `guardian-server migrate` runs. In the dev compose
stack this is invisible: `server/db/init/01-roles.sql` creates `guardian_app`
and the `postgres` image's `POSTGRES_USER` env var creates `guardian_owner`
as a superuser, but **both of those are Docker-image bootstrap behaviour that
only runs when initialising a fresh volume.** A production Postgres instance
has neither. An operator who follows the deploy order above with nothing
else prepared gets `role "guardian_app" does not exist` on the very first
migration.

Before running `guardian-server migrate` against a fresh production
database, a superuser must create both roles by hand, once:

```sql
-- The owner role that runs "guardian-server migrate". It must own the
-- schema (or the whole database) so it can CREATE/ALTER/DROP and so its
-- GRANTs below and inside migrations actually have something to grant from.
CREATE ROLE guardian_owner LOGIN PASSWORD 'CHANGE_ME';
ALTER DATABASE guardian OWNER TO guardian_owner;
-- (or, creating the database at the same time: CREATE DATABASE guardian OWNER guardian_owner;)

-- The application role the running service connects as. No CREATE, no
-- ALTER, no DROP, no BYPASSRLS — see "The application role owns nothing"
-- above. 00001_init.sql grants it DML on every table once guardian_owner
-- runs the migration.
CREATE ROLE guardian_app LOGIN PASSWORD 'CHANGE_ME';
GRANT CONNECT ON DATABASE guardian TO guardian_app;
GRANT USAGE ON SCHEMA public TO guardian_app;
```

These `CREATE ROLE` statements are deliberately **not** in a migration: a
migration file is committed to the repository, and a role's password must
not be.

**The owner role must stay the same role for every migration, forever.**
`ALTER DEFAULT PRIVILEGES` (in `00001_init.sql`) records the default
privileges *for the role that ran it* — it is not a schema-wide setting.
If a later migration runs under some other role (a different admin's
personal login, a rotated deploy credential under a new name), the tables
*that* migration creates silently get no default grant to `guardian_app` at
all, and the service 500s reading or writing them with no RLS-related
explanation, because the failure is a bare permissions error, not a policy
rejection. `MIGRATE_DATABASE_URL` must name `guardian_owner` — the exact
same role — on every environment and every run, indefinitely.

## Startup Behavior

1. `.env` in the working directory is loaded first, for both paths below
   (real environment variables take precedence)
2. `guardian-server migrate` — separate invocation, exits after applying
   migrations using `MIGRATE_DATABASE_URL`
3. Normal startup: validate `TRUSTED_PROXIES`, open a `pgxpool.Pool` against
   `DATABASE_URL`, ping it (5s timeout), start the HTTP server. A listen
   failure is logged and exits 1 after closing the pool.
4. Graceful shutdown on `SIGINT`/`SIGTERM`, 5-second drain, then the pool is
   closed

## Middleware Stack

Installed once in `setupRoutes`, in this order, on every route:

1. `middleware.RequestID` — a per-request id every later log line carries
2. the client-IP resolver chosen by `TRUSTED_PROXIES` (see **Client IP
   resolution**)
3. `requestLogger` (`logging.go`) — one structured line per request:
   `request_id`, method, path, status, bytes, `duration_ms`, `ip`.
   Successful `/health` probes are skipped so an orchestrator polling every
   two seconds does not bury the lines that matter.
4. `recoverer` (`logging.go`) — a panicking handler becomes a logged 500
   (with the stack, through slog, keyed by request id) rather than a dropped
   connection and a plain-text line on stderr. `http.ErrAbortHandler` is
   re-panicked, as net/http requires.
5. `middleware.Timeout(10s)` — cancels the handler's context, and so every
   query it has in flight, before the server's 15-second write timeout
   closes the connection underneath it.
6. CORS (see **CORS**)

### Client IP resolution

Every place the server needs a client address — the per-IP rate limiter,
the `ip` stored on a session at login, failed-login and bad-token warnings,
the request log — goes through `clientIP(r)` in `logging.go`, which returns
what the client-IP middleware resolved and falls back to the TCP peer.
Nothing else in the package reads `r.RemoteAddr`: behind a proxy that names
the proxy, and every session and every warning would carry the same
address.

Which middleware resolves the address is `TRUSTED_PROXIES`:

- **`0` (default)** — `middleware.ClientIPFromRemoteAddr`: the TCP peer is
  the client and `X-Forwarded-For` is ignored entirely. This is the only
  safe value when nothing sits in front of the server, and the reason it is
  the default: a forwarded header the client itself can write is not
  evidence of anything, and a limiter that believed it would hand every
  request a fresh bucket for the price of a forged header.
- **`N ≥ 1`** — `middleware.ClientIPFromXFFTrustedProxies(N)`: the entry
  `N` hops from the right of `X-Forwarded-For` is the client.
  `dist/server/docker-compose.yml` sets `1` for its Caddy, which strips any
  inbound `X-Forwarded-For` and writes exactly one entry. A CDN or second
  load balancer in front of Caddy means `2`, and declaring it in the
  Caddyfile's `trusted_proxies`.

Getting the count wrong is visible, not exploitable: too low behind a proxy
buckets every client under the proxy's address (rate limiting stops
working, and the logs say so), too high resolves no address at all and every
limiter shares one bucket. `TestSpoofedXFFDoesNotEscapeTheLimitWithoutAProxy`
holds the default to its promise.

### Authentication costs three round trips (accepted)

`SessionAuth` looks the session up, then opens a transaction to read the
caller's accounts under `app.user_id`, then touches the session. Folding that
into one query is possible only with a `SECURITY DEFINER` lookup that steps
around the very policies migration 00014 added, and nothing has measured a
problem worth that. It is written down here so the next person to notice it
knows it was noticed.

## Maintenance

`runMaintenance` (`maintenance.go`) runs hourly alongside the HTTP server and
stops with it. It deletes sessions a day past expiry — already unusable, so
this is housekeeping rather than security — and events older than 180 days.

Event retention cannot be a plain `DELETE` from the application role:
`events` carries an account-scoped RLS policy, so an unscoped delete matches
nothing, and scoping it per account would need exactly the fleet-wide read
that migration `00014` removed. `00016` therefore adds
`purge_old_events(interval)`, a `SECURITY DEFINER` function with a pinned
`search_path` whose only ability is to delete rows older than the interval it
is given and return a count — it cannot read a row out. Same narrow-escape
pattern as `account_for_agent_token`.

It runs in-process rather than as a cron job or a database scheduler because
both are another thing to deploy and another thing to forget; a second server
running it too would simply find nothing to do.

## Health

`GET /health` pings the pool with a 2-second timeout and answers `200
{"status":"ok"}` only when Postgres does. During a database outage it
answers `503 {"status":"degraded","database":"unreachable"}` and logs the
error. Without the ping the route reported `ok` straight through an outage,
so the compose healthcheck, a load balancer, or an uptime monitor would
keep routing to a process that could serve nothing but this one route.

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
| `POST /agent/sync`                               | agent token    |

## CORS

```go
origins := []string{"http://localhost:5173"}
if o := os.Getenv("CABINET_ORIGIN"); o != "" {
    origins = strings.Split(o, ",")
}
```

`CABINET_ORIGIN` has no default. It used to fall back to
`http://localhost:5173`, which is right on a developer's machine and, unset in
production, quietly means a page served from that developer's laptop may act
as any signed-in user of the service. `NewServer` refuses to start without it.

`AllowCredentials: true` with an explicit origin list — never a wildcard. A
wildcard origin cannot legally coexist with credentialed (cookie) requests;
browsers refuse the combination, and allowing it would have meant any site
could act as the signed-in cabinet user.

`AllowedHeaders` includes `X-Guardian-Account`. A browser will not send a
header its preflight did not allow, so omitting it breaks the guest flow
entirely — and does it in the browser, where no server-side test would see
it. `TestPreflightAllowsTheAccountHeader` covers exactly that.

## Rate Limiting and Body Size

Every route carries a body cap. The three unauthenticated routes —
`POST /api/v1/auth/register`, `POST /api/v1/auth/login`, and
`POST /agent/enroll` — additionally carry a per-IP rate limit that nothing
else in `routes.go` needs, because they are the only routes a caller reaches
with no session and no agent token:

- **`http.MaxBytesReader`** (`limitBody` in `routes.go`), capping the request
  body at `maxRequestBodyBytes` (1 MiB). It is installed once, in the global
  middleware stack, so it covers **every** route rather than the three
  unauthenticated ones it was originally written for: holding a session is not
  a licence to post a gigabyte, and a cap applied per route is a cap that gets
  forgotten on the next one. A body over it fails in the handler's existing
  JSON decode and is reported as a 400, not spent as memory or turned into a
  500.
- **A per-IP rate limit** (`perIP` in `routes.go`, built on
  `github.com/go-chi/httprate`): `loginRateLimit` and `registerRateLimit`
  (10/minute) protect the argon2id work `handleLogin` deliberately spends on
  every attempt, hit or miss, to close the timing oracle described under
  **Key Design Decisions** below — without a limiter that same correctness
  fix is a memory-amplification denial of service, needing no authentication.
  `enrollRateLimit` (20/minute) is higher on purpose: an installer run across
  a fleet of machines behind one NAT — a school, an office — is legitimate
  traffic, not abuse.

`POST /agent/sync` carries its own, much smaller cap (`maxAgentSyncBodyBytes`,
64 KiB): its body is telemetry that lands in `computers.runtime`, so the cap
bounds the row as much as the request. Both telemetry columns (`hardware` at
enrollment, `runtime` on sync) additionally refuse any object over
`maxTelemetryObjectBytes` (64 KiB) inside `sanitizeJSONObject`, storing `{}`
instead — a failure of telemetry is never a failure of the sync.

The client IP the limiter keys on is whatever the client-IP middleware
selected by `TRUSTED_PROXIES` resolved — see **Client IP resolution** under
the middleware stack above. The count must match the deployment: `0` for a
bare install, `1` behind the compose file's Caddy.

**Per-email keying is not implemented in this plan.** The design spec asks
for login to be rate-limited "per email and per IP". `httprate` supports a
custom key function that could read the email from the body, but doing so
would mean consuming the body before `maxBody`'s `MaxBytesReader` wraps it,
which is the wrong order. Per-IP is the whole of the limiter for plan 1;
per-target (email) keying arrives with the cabinet in a later plan.

## Configuration

Loaded from `.env` in the working directory; real environment variables take
precedence.

| Variable                | Purpose                                                     |
|--------------------------|--------------------------------------------------------------|
| `DATABASE_URL`            | `guardian_app` connection string, used by the running service |
| `MIGRATE_DATABASE_URL`    | `guardian_owner` connection string, used only by `guardian-server migrate` |
| `CABINET_ORIGIN`          | comma-separated origins allowed to send credentialed requests. **Required** — startup fails without it |
| `SERVER_ADDRESS`          | listen address (full URL or `host:port`), default `0.0.0.0:8080` |
| `TRUSTED_PROXIES`         | reverse-proxy hops in front of the server; `0` (default) ignores `X-Forwarded-For`, `1` behind Caddy/nginx. A non-integer or negative value is a startup error |

## HTTP Server Settings

| Setting                    | Value      |
|----------------------------|------------|
| Read timeout               | 15 seconds |
| Write timeout              | 15 seconds |
| Idle timeout               | 60 seconds |
| Handler (context) timeout  | 10 seconds (`middleware.Timeout`, below the write timeout on purpose) |
| Health-check DB ping       | 2 seconds  |

## Deployment

`server/build.sh` builds a **static** Linux binary (`CGO_ENABLED=0
GOOS=linux`, `-trimpath -ldflags="-s -w"`) into `dist/server/`. Static is
not cosmetic: `dist/server/Dockerfile` runs the binary on Alpine, whose musl
libc cannot load a glibc-linked executable, and the failure there is a bare
"not found" pointing nowhere. `GOARCH` defaults to the building machine —
`GOARCH=amd64 ./build.sh` when building on an arm64 laptop for an x86-64
host.

Two supported shapes, both under `dist/server/`:

- **Docker** (`docker-compose.yml`): `postgres` → `migrate` (runs once, must
  succeed) → `server` → `caddy`. Caddy terminates TLS with an automatic
  Let's Encrypt certificate for `GUARDIAN_DOMAIN` and is the single proxy
  hop the compose file's `TRUSTED_PROXIES=1` refers to; the server's port
  is `expose`d to the compose network only, never published, so nothing
  can reach it without going through Caddy. The image runs as a non-root
  user and carries CA certificates for the first outbound HTTPS call. The
  server has a compose healthcheck against `/health`.
- **systemd** (`install.sh` + `guardian-server.service`): installs to
  `/usr/local/bin/guardian/`, runs as the unprivileged `guardian` system
  user (created by the script) under a sandboxed unit — `ProtectSystem=strict`,
  `NoNewPrivileges`, an empty capability set, and the rest; the server
  writes nothing to disk, so it needs no writable path. `.env` is written
  once (`server.env` is never copied over an existing `.env` on a re-run)
  and restricted to `root:guardian 0640`. `migrate` runs from the install
  directory before the service is restarted. Nothing sits in front of the
  server in this shape unless the operator adds it, so `TRUSTED_PROXIES`
  stays `0` until they do.

## Key Dependencies

| Package                     | Purpose                                   |
|-------------------------------|--------------------------------------------|
| `github.com/jackc/pgx/v5`      | PostgreSQL driver and connection pool     |
| `github.com/pressly/goose/v3`  | SQL migrations                            |
| `github.com/go-chi/chi/v5`     | HTTP router                               |
| `github.com/go-chi/cors`       | CORS middleware                           |
| `github.com/go-chi/httprate`   | per-IP rate limiting on register/login/enroll |
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

**A test that reads an account-scoped table back must scope the read**, or
use `observe(t)` — a pool on the owner role, which owns the tables and is not
subject to RLS — when the point is to observe what is actually in the
database independently of the application's scoping. Since `00014` this is
true of `accounts` and `account_members` too; they were the last two tables
where an unscoped read-back in a test happened to work.

`.github/workflows/server.yml` runs on every push and pull request touching
`server/`: gofmt, `go vet`, `sqlc diff` (the committed `internal/db/` must
match `db/queries/` — a query edited without regenerating fails the build),
the full suite against a Postgres 16 service with both roles created the
way `docker-compose.dev.yml` does, and the same static release build
`build.sh` produces. A test nobody runs protects nothing.

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
9. **Responses are written, not derived.** Handlers return the types in
   `responses.go`, never a sqlc row. A column added to a table does not
   appear in the API by itself, and keeping one out is not a `json:"-"` tag
   on generated code — the two tags that exist, `computers.token_hash` and
   `users.password_hash`, were both added after a review caught the leak.
   `account_id` appears on no response at all.
10. **The two GUCs are the only way to see anything.** `app.account_id` scopes
   every multi-tenant table; `app.user_id` narrows the handful of policies
   that must run before an account is known. A query that sets neither reads
   nothing, on every table, without exception.
11. **`X-Forwarded-For` is believed only when configuration says a proxy
   wrote it.** `TRUSTED_PROXIES` defaults to `0`; the compose deploy sets
   `1` for its own Caddy. Trusting a forwarded hop that nothing vouches for
   turns every per-IP limit into a suggestion.
12. **The process runs unprivileged and writes nothing.** Non-root in the
    container, a sandboxed system user under systemd; the only writable
    thing it needs is a Postgres connection.
