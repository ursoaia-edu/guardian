# ProcSentinel SaaS — Design

**Date:** 2026-09-05
**Status:** Approved (design review in session)
**Depends on:** `specs/2026-06-12-agent-enrollment-design.md` — per-agent tokens are a
prerequisite, not an option. Without them an agent cannot prove which account it belongs to.
**Related:** `specs/architecture-2026-06-12.md` (weaknesses 1–6), `specs/whitelist-gui.md`.

## Problem

ProcSentinel works, but deploying it requires operating a Linux server: installing a systemd
service, writing `.env` files, minting tokens, exposing a port. That confines the product to
people who already administer servers.

The service removes that barrier: the customer registers on a hosted server, downloads a
ready installer that already carries their credentials, runs it, and the machine appears in
their cabinet. No server, no tokens typed by hand, no configuration.

## Scope

The SaaS is four independent subsystems. This document sets the architecture common to all
four and specifies the first two in full:

1. **Multi-tenancy in the server** — specified here.
2. **Web cabinet and authentication** — specified here.
3. **Personalised installer delivery** — specified here (mechanism decided; build pipeline
   detail belongs to the implementation plan).
4. **Billing** — architecture only; Stripe integration gets its own spec.

**Out of scope for this document:** the Flutter client's migration to accounts (it happens,
but is designed separately), the cabinet's visual redesign, per-room scheduling, activity
reporting beyond the raw event log.

## Stack

Approach chosen: **a single Go service** — the direct evolution of the existing `server/`
package — serving the agent data-plane, the cabinet control-plane, and the compiled React
bundle from one process.

| Layer | Choice | Reason |
|---|---|---|
| Backend | Go, `go-chi/chi` | already the codebase; `routes/handlers/db/models/middleware` layering extends cleanly |
| Database | PostgreSQL, `pgx` + `sqlc`, `goose` migrations | SQLite's single writer becomes a wall exactly when the service starts growing; sqlc gives typed queries without an ORM |
| Frontend | React + TypeScript, SPA | known to the maintainer; the richest ecosystem for admin surfaces (tables, filters, async state) |
| Bundle delivery | `embed.FS` in the Go binary | one artifact to deploy, no separate static host, no CORS in production |
| Sessions | opaque tokens in Postgres | instantly revocable — required for "cut off a delinquent account"; JWT's statelessness buys nothing here |
| Billing | Stripe | webhook endpoint in the same service |
| Deploy | one container + Postgres, Caddy in front | automatic TLS, one VPS |

Rejected: a separate Next.js control-plane (two codebases, two deploy paths, duplicated
models — a permanent tax on a single maintainer) and a managed BaaS such as Supabase or
Clerk (customer data in a third-party cloud, vendor lock, and the Go agent server is still
needed anyway, so the number of systems does not go down).

The cost accepted with this choice: authentication and the Stripe integration are written by
hand — roughly a week of work, paid once.

## Architecture

```
                      ┌──────────────── guardian-saas (Go, one process) ───────┐
browser (React SPA) ──▶│ /                embed.FS → React bundle              │
   session cookie   ──▶│ /api/v1/*        SessionAuth → account from session   │
                       │                                                        │
Windows agent       ──▶│ /agent/sync      AgentAuth   → account from token      │
                       │ /agent/enroll    binding-token gated                   │
Stripe              ──▶│ /webhooks/stripe Stripe signature                      │
                       └────────────────────────────────────────────────────────┘
                                              │
                                          PostgreSQL (RLS)
```

**The invariant everything rests on: `account_id` never arrives in a request.** It is always
derived server-side — from the session for cabinet calls, from the agent token for agent
calls. A client cannot name another account's data because it never names an account at all.

### File layout

Continues the existing package, does not replace it:

| File | Responsibility |
|---|---|
| `routes.go` | three route groups instead of two, plus SPA fallback |
| `middleware.go` | `SessionAuth`, `AgentAuth`; existing `AdminAuth`/`ClientAuth` deleted |
| `tenant.go` | account context, `AccountFromContext(ctx)`, RLS connection wrapper |
| `auth.go` | registration, login, sessions, password reset, invitations |
| `billing.go` | Stripe checkout, webhooks, subscription state |
| `installer.go` | archive assembly and download |
| `db/queries/*.sql` | sqlc sources; every statement filters on `account_id` |

## Domain model

```
Account  ─── created at registration; unit of billing and isolation
├── Account members     owner, admins
├── Computers (pool)    each in 0 or 1 room
└── Rooms
    ├── the room's computers
    ├── policy: mode (blacklist/whitelist) + application list + protection on/off + power
    └── room guests
```

Three rules the model depends on:

1. **A computer belongs to the account, not to a room.** Room assignment is a nullable field.
   Moving a machine between rooms reinstalls nothing: on its next sync the agent receives a
   different policy, and that is the whole operation.
2. **Policy lives on the room.** Today mode and the application list are global — the `server`
   table is a single row guarded by `CHECK (id = 1)`. Each room now carries its own. There is
   deliberately no account→room inheritance: it would add a second place to look and make
   "why is this program blocked?" twice as hard to answer.
3. **A computer outside a room enforces nothing.** It syncs, reports itself, and is listed,
   but blocks nothing. A machine that was just added must not start killing processes before
   somebody deliberately placed it somewhere. Blocking a computer (`computers.blocked`) is a
   separate thing from policy and applies whether or not it is in a room — it is a lock on the
   machine, not a rule about programs.

   **On the wire, a lock is whitelist mode with an empty list.** The agent's sync response has no
   field that says "locked", and inventing one would break every agent already deployed; but
   "whitelist, allowing nothing" already means exactly that in the vocabulary the agent speaks —
   only the system processes it protects unconditionally survive. The alternative reading, that a
   blocked machine simply receives no policy, has to be named to be rejected: it would mean pressing
   **Block this computer** in the cabinet switches protection off on it, which is the opposite of
   the button. A locked machine's own room policy is irrelevant while the lock is on; unblocking
   restores it on the next sync.

### Roles

| Role | Scope | Capabilities |
|---|---|---|
| Owner | account | everything, plus billing and account deletion. One per account, not transferable in v1 |
| Admin | account | all rooms, the whole computer pool, invitations. No billing |
| Room member | granted rooms only | rules and computers inside those rooms. Cannot see the pool or other rooms |

Invitations are sent by email. An invitee without an account registers and lands directly in
the room. **A room invitation grants no visibility of the rest of the account** — that is what
makes sharing an environment safe.

### Computer identity and naming

`machine_guid` (the Windows machine GUID) is the stable identity, not the hostname. It gives
"same machine, renamed" instead of a duplicate row.

`display_name` is set by the user and is what the interface shows; when empty the UI falls
back to `hostname`, so no machine is ever nameless. It is asked for at the one moment the
user certainly knows which machine is in front of them — in the first-computer wizard, as the
machine appears ("DESKTOP-8H3K connected — what should we call it?"). No uniqueness
constraint: two machines called "Masha's laptop" is the user's problem, not grounds to reject
input. Search matches both `display_name` and `hostname`.

### Machine passport

Split by lifecycle:

**At enrollment** (once, refreshed when hardware changes): machine GUID, hostname, Windows
version/edition/build, architecture, CPU model and core count, RAM, disks (model, capacity),
GPU, MAC addresses, locale and timezone, BIOS/board serial.

**On every sync** (cheap, overwritten): last contact, uptime, currently logged-in Windows
user, free disk space, agent version, run mode (service or console), privileges (SYSTEM or
user), public IP (observed by the server).

## Data schema

```sql
users           (id, email UNIQUE, password_hash, name, email_verified_at, created_at)
accounts        (id, name, owner_user_id, plan, computer_limit, stripe_customer_id,
                 subscription_status, grace_until, created_at)
account_members (account_id, user_id, role, created_at)          -- PK(account_id, user_id)

rooms           (id, account_id, name, mode, protection_enabled, power_allowed, created_at)
room_members    (room_id, user_id, role, created_at)             -- PK(room_id, user_id)

applications    (id, account_id, room_id, name, list, enabled, created_at)
                -- UNIQUE(room_id, name, list); list = 'blacklist' | 'whitelist'

computers       (id, account_id, room_id NULL, display_name, machine_guid,
                 hostname, os_name, os_build, arch, agent_version,
                 hardware JSONB, runtime JSONB,
                 token_hash, blocked, enrolled_at, last_seen_at)
                -- UNIQUE(account_id, machine_guid)

binding_tokens  (id, account_id, token_hash, created_at, expires_at, revoked_at)
sessions        (token_hash PK, user_id, created_at, expires_at, last_used_at, ip, user_agent)
invitations     (id, account_id, room_id NULL, email, role, token_hash, expires_at, accepted_at)
events          (id, account_id, room_id, computer_id, type, payload JSONB, created_at)
```

Columns are split deliberately: fields the UI filters or sorts on (`agent_version`, `os_name`,
`arch`) are real columns; the rest of the passport lives in `hardware`/`runtime` JSONB, where
adding a field costs no migration.

`account_id` is repeated on tables that could reach it through a parent (`applications`,
`events`). That is not accidental denormalisation: an RLS policy has to test the column on the
row itself, without a join.

### Migration from the current schema

| Today | Becomes |
|---|---|
| `applications` (one global list) | `applications` with `room_id` |
| `server` (single row: enabled, mode) | `rooms.protection_enabled`, `rooms.mode` |
| `client` (name/bool pairs, magic `power`) | `rooms.power_allowed` |
| `computers.identity INTEGER`, self-assigned | `computers.id` + `machine_guid`, server-assigned |

The `client` table is dropped entirely. It was a generic name/bool bag with magic names
(architecture review, weakness 5); its only real inhabitant becomes an ordinary column.

`events` is new and is not a luxury: without it the Overview screen has nothing to show and
"why was this process killed yesterday?" has no answer (weakness 2 — the server stores state
but remembers nothing).

### Tenant isolation

Two layers, because one is not enough.

**Layer 1 — discipline.** Every query filters on `account_id`, derived from the session or
the agent token. Every sqlc query file carries it.

**Layer 2 — PostgreSQL row-level security.** Each transaction issues
`SET LOCAL app.account_id = ...` before any statement; RLS policies on every account-scoped
table make other accounts' rows physically unreachable, even when the SQL is wrong.

The application connects under a role that **cannot** bypass RLS. Migrations run under a
separate role that can. Cost: a wrapper around the pgx pool, roughly 50 lines. Benefit: the
single class of bug that could end the business becomes impossible rather than unlikely.

RLS is the second line of defence, not the first — see the isolation tests below.

## Authentication

One `sessions` table, one opaque token, two delivery forms:

| Client | Obtains | Stores | Sends |
|---|---|---|---|
| Web (React) | `POST /api/v1/auth/login` → `Set-Cookie` | HttpOnly cookie, invisible to JS | automatically |
| Mobile (Flutter) | same endpoint → token in the JSON body | secure storage (Keychain / Keystore) | `Authorization: Bearer` |

`SessionAuth` accepts both forms, resolves the same `sessions` row, and puts `account_id` and
`user_id` into the request context. One verification path, one revocation path: delete the
row and both clients are logged out.

Sessions live 30 days with sliding renewal. Anything shorter and a parent is thrown out of
the app weekly and deletes it; because revocation is server-side, the long lifetime is not a
security trade.

Passwords are argon2id. Login is rate-limited per email and per IP, as are password reset and
enrollment.

### Two existing security defects that must not survive the move

1. **`middleware.go:35-41` — a hardcoded fallback token.** Both auth tiers fall back to the
   literal `mILp9n6shk3G9SGSaS2nmP6YlLHwsP1Z` when the environment variable is unset. In a
   multi-tenant service that is instant, total compromise of every customer. Both tiers are
   deleted outright.
2. **`main.go:19-26` — process-global caches.** `appsCache`, `enabledCache`, `modeCache` and
   `clientCache` hold one tenant's worth of state for the whole process; multi-tenant, that is
   literally "customer A sees customer B's settings". They are removed and reads go to
   Postgres, as the architecture review already recommended (weakness 4). At this request rate
   the difference is unmeasurable.

Also: `routes.go` currently sets `AllowedOrigins: ["*"]`. With cookie sessions that is a hole;
it becomes the cabinet's exact origin with `AllowCredentials: true`.

## Screens

**Public** — Landing (`docs/index.html`, gains a sign-in button) · Registration (email,
password, email confirmation) · Sign in · Password reset · Invitation (follow the emailed
link, sign in or register, land in the room).

**Onboarding** — First-computer wizard: download the archive → run it → the machine appears
and is named. It is the point of the whole product, so it is the first screen after
registration, not an item hidden in settings.

**Main**

- **Overview** — rooms as tiles: machine count, how many online, whether protection is on,
  what happened in the last 24 hours.
- **Rooms** — list, create, rename, delete.
- **Room** — the main working screen, in tabs:
  - *Computers* — the room's machines, status, quick actions (block, power off)
  - *Rules* — blacklist/whitelist mode, application list, protection master switch
  - *Power* — whether shutdown is permitted, forced commands
  - *Members* — who has been let into this room
- **Computers** — the whole account pool: filters (room, online/offline, agent version),
  assignment to a room, bulk operations.
- **Computer** — passport, status, event history, rename, move, unenroll.
- **Install agent** — download the archive, instructions, what to do when antivirus complains.

**Settings** — Profile · Security (active sessions and devices, revoke) · Account members
(admins, invitations, revocation) · Billing (plan, computer limit, card, invoices).

The cabinet **polls** — every 5–10 seconds on the active screen. WebSocket/SSE are not in v1:
ten seconds of latency in a product whose commands take twenty to arrive anyway changes
nothing, and the infrastructure cost is real.

## Agent flows

### Enrollment

```
cabinet            server                          customer machine
   │                  │                                  │
   ├─ Download ──────▶│ streams the reference archive    │
   │                  │ with agent.env substituted ─────▶│ extract, run Guardian.exe
   │                  │                                  │
   │                  │◀─ POST /agent/enroll ────────────┤ binding token + passport
   │                  │   creates the computer row,      │
   │                  │   returns the agent token ──────▶│ stored locally
   │                  │                                  │
   │◀─ machine listed │◀─ GET /agent/sync (Bearer) ──────┤ every 20s
```

`account_id` comes from the binding token, never from the request body.

**The binding token is account-scoped and reusable**, with a TTL, revocation from the
cabinet, and the plan's computer limit as its ceiling. A single-use token would be safer but
would force a separate download per machine — precisely the friction the service exists to
remove.

Two properties without which this flow fails in practice:

- **Reinstallation must not create duplicates.** An agent arriving with a known `machine_guid`
  updates the existing row and receives a fresh agent token, killing the old one. Reinstalling
  Windows, swapping a disk, "I removed it and installed it again" are ordinary events, and
  none of them may leave a junk row behind.
- **The plan limit is checked at enrollment**, returning `402` with a comprehensible message
  in the cabinet ("your plan covers 5 computers; this is the sixth") rather than a silent
  refusal the customer has to debug alone.

### Sync

`GET /agent/sync` with the agent token. The server resolves the machine by `token_hash`, and
from it the account and the room; it returns the room's policy (or an empty one when the
machine is in no room) and updates `last_seen_at` and `runtime`.

## Installer

**Delivered as a ZIP, not a single executable.** The decisive property: `Guardian.exe` stays
byte-identical for every customer, so it can be signed once, served from cache, and never
post-processed. The personalised part is a plain text file.

```
Guardian-<account>.zip
├── Guardian.exe                        signed once, identical for everyone
├── agent.env                           the only generated file: SERVER_ADDRESS + binding token
├── bin/agent/procsentinel-agent32.exe
├── bin/agent/procsentinel-agent64.exe
└── README.txt                          three steps
```

This is close to what `dist/agent/` already contains, and `findAgentExe()`
(`tools/whitelist-gui/install.go:58`) already looks for the agent next to itself, so the
console barely changes. The server keeps one pre-built reference archive and substitutes a
single small file on download — orders of magnitude cheaper than rebuilding a binary per
request, and it needs no Go toolchain in the container.

Rejected: rebuilding per download (a Go toolchain on the server and seconds of CPU per 25 MB
artifact), patching a placeholder inside the PE (breaks Authenticode), and appending the
token to the certificate table (works, and is what Dropbox and Chrome do, but depends on
padding checks that Microsoft shipped a switch to tighten in MS13-098 — off by default, yet
not a foundation worth building on when a text file will do).

Three things the archive form requires:

1. **Running straight from the archive is the most common user error.** Explorer presents a
   ZIP as a folder; the user double-clicks `Guardian.exe` inside it, Windows extracts *only
   that file* to a temporary directory, and neither `agent.env` nor the agent binaries are
   found. The console must detect this at startup — its own path under `%TEMP%\Temp1_*`, or
   missing siblings — and say "extract the whole archive first" instead of failing with a
   technical error.
2. **The binding token must not remain on disk after installation.** It is reusable and
   account-wide: in the wrong hands it adds a machine to the fleet and exposes the room's
   policy. Once enrollment succeeds the agent lives on its own per-machine token, and the
   binding token is wiped from the installed `.env`. Its copy in the downloaded folder is
   fine — the user deletes that folder — but it has no business in `System32\ProcSentinel`.
3. **Mark-of-the-Web still applies.** A ZIP downloaded from the internet is marked, and the
   mark is inherited by extracted files, so SmartScreen does not go away. The archive does not
   replace code signing; it only means one artifact needs signing instead of thousands.

Manual editability is a deliberate feature, not a side effect: when a customer's installation
misbehaves, "open agent.env and check what it says" is a one-minute phone call, which a token
baked into a binary makes impossible. The same file also opens the door to a self-hosted
variant delivered by the same archive.

**Certificates are not shipped.** The server sits behind Caddy with a normal Let's Encrypt
certificate and the agent trusts the Windows certificate store. No pinned CA (it breaks every
agent on the day the CA changes) and no per-account client certificates (a private CA plus
rotation and revocation is a large piece of infrastructure for a marginal gain over a
revocable token).

Code signing for `Guardian.exe` is desirable and unresolved. The product is a service that
kills other processes and defends itself from being stopped — exactly the behavioural profile
antivirus heuristics react to — so an unsigned, freshly downloaded executable draws both
SmartScreen's "unknown publisher" and a raised chance of quarantine. An EV certificate costs
roughly $300–500 a year and requires entity verification, so it is a decision to make early
but it does not block the architecture.

## Billing lifecycle

Stripe, with the webhook endpoint in the same service. The plan sets `computer_limit`, which
is enforced at enrollment.

**When a subscription lapses:** 14 days of grace with a banner in the cabinet, after which
agents stop enforcing policy but stay enrolled, and data is retained a further 30 days.
Paying restores everything by itself, with nothing to reinstall. Keeping someone's computers
locked down over an unpaid invoice is a bad idea both reputationally and legally.

## Errors and degradation

**The agent loses connectivity.** It keeps applying the last known policy indefinitely. This
is deliberate: if protection expires after N hours offline, the simplest way to defeat the
product is to unplug the network — which makes it useless against precisely the person it is
written for. The agent already caches policy on disk in `sync.json`.

**Enrollment fails.** Three distinct cases, three distinct messages in the console rather than
one generic "error": token revoked, plan limit reached, no connection. The customer fixes the
first two in the cabinet and the third at home.

**The cabinet.** `401` → sign-in (session revoked or expired), `402` → billing, `403` → "you
do not have access to this room". Polling backs off exponentially on errors, so a tab left
open overnight against a downed server does not hammer it with thousands of requests.

**The server.** Rate limits on login, password reset and enrollment, per IP and per target.
Migrations run under a role that bypasses RLS; the application runs under one that cannot.

## Testing

Today only the console (`tools/whitelist-gui`) has tests; the server, agent and mobile app
have none. Tolerable for a personal server, not for a service holding other people's data.
The floor:

- **Tenant isolation tests, as their own mandatory suite.** For every endpoint: account A,
  holding a valid session, reaches for account B's object and gets `404`. It is a cheap
  parameterised test and it catches exactly the class of bug that ends the business. RLS is
  the second line; this test is the first.
- **Handler tests against a real PostgreSQL**, not mocks — RLS policies cannot be verified
  against a mock even in principle.
- **A fake agent as a test utility** — enrolls, syncs, reports. It doubles as a manual tool:
  exercise the cabinet without booting a Windows machine.
- **React:** Vitest for logic, Playwright for one end-to-end path — register → download the
  archive → fake agent enrolls → machine appears in the room.

## Prerequisites and order

1. Per-agent tokens (`specs/2026-06-12-agent-enrollment-design.md`). Everything else assumes
   an agent can prove which account it belongs to.
2. PostgreSQL migration and the RLS wrapper.
3. Accounts, sessions, and the deletion of both existing auth tiers.
4. Rooms and the move of policy from global to per-room.
5. Cabinet: onboarding first, then rooms and computers.
6. Installer delivery.
7. Billing.

## Open questions

- Code signing: buy an EV certificate, or ship unsigned in v1 and accept the SmartScreen
  friction?
- The Flutter client's migration to accounts — designed separately, but it must land before
  the shared `ADMIN_TOKEN` can be removed from circulation.
