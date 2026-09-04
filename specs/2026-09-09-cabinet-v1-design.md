# Guardian Cabinet v1 — Design

**Date:** 2026-09-09
**Status:** Approved in session (scope and the three key decisions)
**Depends on:** `specs/2026-09-05-saas-design.md` — accounts, rooms, the computer pool,
RLS tenancy and the installer archive are prerequisites, all of them already in the server.
**Related:** `specs/server.md`, `specs/api.md`, `specs/agent.md`, `specs/mobile.md`.

## Problem

The multi-tenant server implements the domain the SaaS design described: accounts, rooms,
per-room policy, a computer pool, room guests, and an audit feed of what administrators did.
Three things it does not do, and the product does not work without them:

1. **Nobody can see what actually happened on the machines.** The agent kills a process and
   writes one line to a local log file (`agent/agent.go:413`). The server never hears about
   it. `GET /api/v1/events` answers "who changed the policy", never "what did this policy
   do" — which is the question the customer actually asks.
2. **An account cannot be joined.** `POST /api/v1/account/members` and
   `POST /api/v1/rooms/{roomID}/members` add a user who has *already registered*
   (`server/handlers_account.go:63` says so in as many words). There is no way to invite
   somebody by email, no email confirmation, and no password reset — so a forgotten password
   costs the customer their entire fleet.
3. **Policy is a single static switch.** A room is in one mode until a human changes it.
   The most ordinary requirement in this product's category — "whitelist during lessons,
   free afterwards" — cannot be expressed, and neither can "let this one program run for the
   next half hour".

This document specifies the work that closes those three gaps, plus the smaller fleet-
management omissions found in the same review.

## Scope

**In:** the blocking log and its room tab · email verification, password reset, password
change · email invitations to an account or to a single room · room schedules · temporary
suspension of a rule · application templates · a read-only room role · a command queue ·
bulk computer assignment, computer-list pagination, an online/offline transition event,
agent versioning with a staged, operator-applied update · the Security screen's server
side.

**Explicitly out, decided in review:**

- **Usage tracking** ("what was the user doing", time spent per application). Not wanted.
- **Notifications** — no email alerts, no push. The cabinet is read when it is opened.
- **Tamper detection and self-defence hardening.** The service runs as an administrator and
  the monitored people log in as ordinary users; the threat is out of scope by deployment,
  not by oversight.
- **Client certificates / mTLS.** Confirmed: certificates are not part of this product.
  "Security" in the cabinet is where *secrets* live — binding tokens, sessions, keys.
  See **Security screen** below.

**Out, deferred:** 2FA/TOTP · account deletion and data export · per-email rate limiting ·
Stripe billing · the React SPA itself (no `web/` directory exists yet; this document
specifies the API and the screens it must serve, not the frontend code).

---

## 1. Blocking log

The centre of this document. Everything else is smaller.

### The shape of the data

A kill is not an audit event and must not share a table with one.

`events` records deliberate human acts: nineteen types, all of them rare, kept 180 days,
read as a narrative. Kills are machine output: a misconfigured whitelist on one classroom
produces thousands of rows an hour. Putting them in `events` drowns the audit feed in noise,
forces one retention policy onto two kinds of data with different value, and makes the
"what did the administrator change last month" query scan a table three orders of magnitude
larger than it needs to be.

So: a separate table, its own retention, its own endpoint, its own tab.

```sql
CREATE TABLE process_events (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id  UUID NOT NULL REFERENCES accounts(id)  ON DELETE CASCADE,
    computer_id UUID NOT NULL REFERENCES computers(id) ON DELETE CASCADE,
    room_id     UUID          REFERENCES rooms(id)     ON DELETE SET NULL,
    process     TEXT NOT NULL,
    reason      TEXT NOT NULL CHECK (reason IN ('blacklist', 'whitelist', 'locked', 'overflow')),
    count       INTEGER NOT NULL DEFAULT 1 CHECK (count > 0),
    first_at    TIMESTAMPTZ NOT NULL,
    last_at     TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_process_events_account_time  ON process_events(account_id, created_at DESC);
CREATE INDEX idx_process_events_room_time     ON process_events(room_id, created_at DESC);
CREATE INDEX idx_process_events_computer_time ON process_events(computer_id, created_at DESC);
```

`room_id` is denormalised deliberately: it is the room the machine was in **at the moment of
the kill**, not the room it is in now. Moving a machine between rooms must not rewrite its
history, and the room tab's query must not join through a column that has since changed.

`reason` distinguishes the three ways the agent decides to kill, because "why was this
killed?" is the question the log exists to answer: an explicit blacklist entry, absence from
a whitelist, or the machine being locked (`computers.blocked`, which the wire expresses as
an empty whitelist — see the SaaS design).

An agent never sends `locked`: it cannot tell a lock from an empty whitelist, since both
arrive as `mode: whitelist` with no applications. The server rewrites `whitelist` to
`locked` when the reporting machine is blocked, because only the server knows.

A fourth value, `overflow`, carries the marker the agent writes when its buffer dropped
entries (see **Overflow** below). It is in the CHECK because without it the server would
reject the one row whose whole job is to say that data was lost.

RLS is the same policy every account-scoped table carries:

```sql
ALTER TABLE process_events ENABLE ROW LEVEL SECURITY;
CREATE POLICY process_events_isolation ON process_events
    USING (account_id = current_account_id())
    WITH CHECK (account_id = current_account_id());
```

**Retention: 30 days**, purged by the existing hourly maintenance loop through a
`SECURITY DEFINER` function, exactly as `events` already is (`server/maintenance.go`) —
an unscoped `DELETE` is blocked by RLS, which is the point of RLS.

### The agent side

Three properties, none optional.

**Deduplication.** The enforce loop runs once a second (`agent/agent.go:enforceLoop`). A
process that respawns — a game launcher, a browser restarting itself — produces one kill per
second forever. Sending one row per kill would mean 3,600 rows per hour per process per
machine; a hundred machines makes the table unusable and the tab unreadable.

The agent aggregates in memory, keyed by `(process, reason)`: the first kill opens an entry
with `first_at`, each subsequent one bumps `count` and `last_at`. Entries accumulate **until
the next successful sync**, which flushes them — so one respawning process is one row per
sync cycle, carrying its count, rather than twenty. That is also the number a human wants to
read: "steam.exe, 47 раз, 14:02–14:20" says more than 47 identical lines.

While the agent is offline the flush does not happen, so a single entry's span is capped at
**one hour**: past that the key rolls over into a new entry. Without the cap a machine off
the network for a week arrives with one row claiming 600,000 kills across seven days, which
is true and useless.

**A bounded buffer.** The agent keeps at most **500 pending entries**. Beyond that the
oldest are dropped and a single `guardian.log_overflow` marker entry is kept, so a machine
offline for a week does not grow its own memory without limit and does not arrive with a
megabyte of backlog. Dropping the oldest rather than refusing the newest is deliberate: the
recent past is what gets looked at.

**Idempotency.** A sync whose response is lost is retried, and the server has already
committed the batch. Without a guard the log double-counts, and a log that inflates its own
numbers is worse than no log. The agent stamps each batch with a `batch_id` (an opaque
token generated when the batch is assembled, reused verbatim on retry); the server stores
the last accepted one on the computer row and silently ignores a repeat:

```sql
ALTER TABLE computers ADD COLUMN last_event_batch TEXT;
```

`TEXT`, not `UUID`: the agent module depends on `golang.org/x/sys` and nothing else, and
pulling in `google/uuid` to generate one identifier is not worth it — its existing
`randomGUID()` (`agent/client.go`) already produces a non-UUID random string. The server
treats the value as opaque and caps it at 64 characters.

The agent generates a new `batch_id` only after a `200`, so the retry path is the same code
as the first attempt.

### The server side

The batch arrives in the existing sync body, already capped at 64 KiB (`routes.go:184`).
Rules, all of them failing soft — **telemetry must never fail a sync**, which is already the
rule for `runtime`:

- At most **200 items** per batch; the rest are dropped, and the drop is logged server-side,
  not returned as an error.
- `process` is sanitised and truncated like every other agent-supplied string
  (`sanitizeText`), max 260 characters (Windows `MAX_PATH`).
- `reason` outside the three permitted values → the item is dropped.
- `count` outside `[1, 100000]` → clamped.
- **Ordering is by the server's `created_at`, never the agent's timestamps.** An agent's
  clock can be wrong by years, and a client-supplied sort key is a client-supplied way to
  sit at the top of somebody's feed forever. `first_at`/`last_at` are stored and displayed
  because they are what the operator wants to read, but they are clamped into
  `[now - 24h, now + 5min]` and never order anything.
- The whole batch is written in the same transaction as the sync's `runtime` update and the
  `last_event_batch` stamp, so a partially-recorded batch cannot exist.

### The endpoint

```
GET /api/v1/rooms/{roomID}/process-events
GET /api/v1/computers/{computerID}/process-events
```

Both are in the guest-reachable route group, both gated on the existing `assertRoomVisible`
(the computer variant resolves the computer's room first). Cursor pagination identical to
`/events`: `limit` (default 50, max 200), opaque `cursor` over `(created_at, id)`.

Filters, all optional and all combinable: `process` (exact match), `reason`, `computer_id`
(on the room route), `since` / `until` (RFC 3339).

Response:

```json
{
  "process_events": [
    {"id": "…", "computer_id": "…", "computer_name": "Класс 2 — ПК 7",
     "room_id": "…", "process": "steam.exe", "reason": "blacklist",
     "count": 47, "first_at": "…", "last_at": "…", "created_at": "…"}
  ],
  "next_cursor": "1757366400000000000_0d9a…"
}
```

`computer_name` is resolved server-side (`display_name`, falling back to `hostname`) because
the alternative is the cabinet issuing an N+1 of lookups to render a list.

### The tab

**Room → Log.** A table: time, machine, process, reason, count. Filters across the top:
machine, process, reason, date range. Default view is the last 24 hours, newest first.
The same table, pre-filtered to one machine, is the log section of the Computer screen.

*Built* (`server/webui/screens/blocklog.js`). The tab is named **Log** rather than Журнал
because the cabinet has no i18n yet and the rest of it is in English; it moves with the
rest when translation lands. The machine filter appears only in the room, where there is
more than one machine to pick between, and the reasons are relabelled for a reader —
`whitelist` shows as *Not on the list*, since "whitelist" as a reason reads like the
opposite of what happened.

---

## 2. Registration, finished

### Email verification

`users.email_verified_at` has existed since `00001_init.sql` and has never been written.
It starts being written.

```sql
CREATE TABLE email_tokens (
    token_hash TEXT PRIMARY KEY,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    purpose    TEXT NOT NULL CHECK (purpose IN ('verify', 'reset')),
    email      TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_email_tokens_user ON email_tokens(user_id);
```

No RLS, for the same reason `sessions` has none: the row has no `account_id` and is reached
before any account scope exists. It is a digest table — the plaintext token exists only in
the email — and it is purged by the maintenance loop once expired.

**What an unverified account cannot do:** mint a binding token, and send an invitation.
Everything else works. Those two are exactly the actions that reach *outside* the account —
one adds machines, the other adds people — and gating them is what stops a typo'd or
someone else's address from becoming a working account. Blocking the whole cabinet instead
would mean a customer who mistypes their address cannot even see what they bought.

### Password reset and change

```
POST /api/v1/auth/password/forgot   {"email": "…"}          → 204, always
POST /api/v1/auth/password/reset    {"token": "…", "password": "…"} → 204
POST /api/v1/account/password       {"current": "…", "new": "…"}    → 204  (session)
```

`forgot` returns `204` whether or not the address exists, and spends the same argon2id work
either way — the registration endpoint already treats account enumeration as a bug to avoid,
and a reset endpoint that leaks it undoes that.

A completed reset **deletes every session of that user**. A password change from inside the
cabinet deletes every session *except the calling one*. Both are the point of having
server-side sessions at all.

Both `forgot` and `reset` are rate-limited per IP at the login endpoint's rate.

### Invitations

```sql
CREATE TABLE invitations (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    room_id    UUID          REFERENCES rooms(id)    ON DELETE CASCADE,
    email      TEXT NOT NULL,
    role       TEXT NOT NULL CHECK (role IN ('admin', 'member', 'viewer')),
    token_hash TEXT NOT NULL UNIQUE,
    invited_by UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    accepted_at TIMESTAMPTZ,
    revoked_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_invitations_account ON invitations(account_id);
```

`room_id IS NULL` means an account-level invitation (`role = 'admin'`); a non-null
`room_id` means a grant to that one room (`role IN ('member','viewer')`). A `CHECK`
enforces the pairing so the two cannot be mixed.

RLS as usual for the manager-facing routes. The **acceptance path is unauthenticated by
necessity** — the invitee may not have an account yet — so it uses the same pattern the
agent token already uses: a `SECURITY DEFINER` function that takes a hash and returns one
UUID and nothing else.

```sql
CREATE FUNCTION account_for_invitation_token(hash TEXT) RETURNS UUID
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, public AS $$
    SELECT i.account_id FROM public.invitations i
    WHERE i.token_hash = hash AND i.accepted_at IS NULL
      AND i.revoked_at IS NULL AND i.expires_at > now()
$$;
```

Flow:

```
manager ─ POST /api/v1/invitations {email, room_id?, role}
        └─▶ row + emailed link  {CABINET_ORIGIN}/invite/<token>

invitee ─ GET  /api/v1/invitations/<token>          unauthenticated
        │      → {"account_name": "…", "room_name": "…", "email": "…", "role": "member"}
        │        (no ids, no secrets — just enough to render "Вас приглашают в …")
        ├─ registers or signs in
        └─ POST /api/v1/invitations/<token>/accept   session required
               → account_members or room_members row, invitation marked accepted
```

Tokens are single-use and live **7 days**. `GET /api/v1/invitations` lists the account's
pending ones, `POST /api/v1/invitations/{id}/resend` sends the link again (rate-limited to
one per minute per invitation, and it mints a fresh token, invalidating the previous link),
and `DELETE /api/v1/invitations/{id}` revokes one. All manager-only.

An invitation to an address that already belongs to a user still goes through the same
flow — accepting is one click for them, and the alternative (silently granting access to an
address the inviter typed) is a way to add a stranger to a room by guessing an email.

Events: `invitation.sent`, `invitation.accepted`, `invitation.revoked`.

### Sending mail

**Provider: SendGrid, over SMTP.** A new `server/internal/mail` package with one interface
and two implementations: SMTP (`net/smtp`, STARTTLS) and a logging one that prints the
message to stdout.

SMTP rather than SendGrid's Web API v3 on purpose: it needs no vendor SDK, no vendor types
in the handler layer, and the day the provider changes it is a URL in a `.env` rather than a
package to rewrite. The Web API buys per-message tracking and templates, neither of which
this product wants — a password reset that is tracked is a password reset with a third
party's pixel in it.

Configuration, all in the server's `.env` (and in `dist/server/server.env`):

| Key | Value |
|---|---|
| `SMTP_URL` | `smtp://apikey:<API-KEY>@smtp.sendgrid.net:587` |
| `MAIL_FROM` | `Guardian <noreply@<domain>>` — the address must be verified in SendGrid |
| `MAIL_TRANSPORT` | unset in production; `log` for development |

**The username is the literal string `apikey`.** Not the account's email, not a name — a
SendGrid-specific detail that costs an hour of "authentication failed" to rediscover, so it
is written down here and in the `.env` template. The API key itself is scoped to **Mail
Send** and nothing else.

Port 587 with STARTTLS. Implicit TLS on 465 also works and is not used: 587 is the one
SendGrid documents first and the one least likely to be blocked outbound by a VPS provider.

**Two operational prerequisites, neither of them code**, both of which decide whether
password resets arrive at all:

1. **Domain authentication** (the CNAME records SendGrid issues for SPF and DKIM). Without
   it mail from a fresh domain lands in spam, and a reset link in a spam folder is
   indistinguishable from a broken product. Single Sender Verification is enough to *send*
   and is not enough to be *delivered*; it is a development shortcut, not a deployment.
2. **A plan that covers the volume.** The free tier is 100 messages a day, which is
   registration confirmations plus resets plus invitations for a small fleet and nothing
   more.

**A known limit, accepted for v1:** SendGrid maintains suppression lists (bounces, spam
reports, unsubscribes). A suppressed address returns success over SMTP and is then silently
dropped — the server cannot tell. Nothing in v1 detects it; the operator diagnoses "письмо
не пришло" in SendGrid's Activity Feed. Closing this properly means the Event Webhook and a
delivery-status column, which is its own piece of work and is not worth it before the first
customer complains.

The server **refuses to start** when `SMTP_URL` is unset, unless `MAIL_TRANSPORT=log` is
set explicitly — the same shape as `CABINET_ORIGIN`, and for the same reason. A service that
boots happily with mail silently disabled produces customers who cannot reset their password
and an operator who finds out from a support ticket. Making the dev path an explicit opt-in
costs one line in a `.env` and removes that failure entirely.

Templates are plain text, in Russian, generated in Go. No HTML mail in v1. Sending happens
**outside the request's transaction and after it commits** — an SMTP round trip to a third
party inside a database transaction holds a Postgres connection hostage for as long as
SendGrid feels like taking, and the 10-second request timeout would abort the invitation
that was already written.

---

## 3. Room schedules

```sql
ALTER TABLE rooms ADD COLUMN timezone TEXT NOT NULL DEFAULT 'UTC';

CREATE TABLE room_schedules (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    room_id    UUID NOT NULL REFERENCES rooms(id)    ON DELETE CASCADE,
    days       SMALLINT NOT NULL CHECK (days > 0 AND days < 128),
    starts_at  TIME NOT NULL,
    ends_at    TIME NOT NULL CHECK (ends_at > starts_at),
    mode       TEXT NOT NULL CHECK (mode IN ('blacklist', 'whitelist', 'free')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_room_schedules_room ON room_schedules(room_id);
```

`days` is a bitmask, Monday = `1<<0` … Sunday = `1<<6`, so "пн–пт" is one row rather than
five. `ends_at > starts_at` forbids intervals crossing midnight in v1: "22:00–02:00" is
written as two rows, and the constraint means no evaluation code has to reason about a
window that belongs to two days at once.

**Evaluation happens in Go, not SQL**, extending the four-step policy logic in
`handlers_agent.go` to six:

1. `computers.blocked` → `whitelist` with an empty list. Unchanged, and still the *only*
   thing that produces a lock.
2. No `room_id` → `free`.
3. `protection_enabled = false` → `free`.
4. **Schedule:** load the room's `timezone`, take the current local weekday and time, and
   find the intervals covering it. The one with the **latest `starts_at`** wins, ties broken
   by the smaller `id` — a total order, so two overlapping intervals can never make two
   machines in the same room disagree. No match → the room's own `mode`.
5. `applications` = the room's enabled entries whose `list` matches the effective mode,
   minus the currently suspended ones (§4).
6. `client` = the room's `power_allowed`, unchanged.

**One behaviour change, deliberate.** Today a room in whitelist mode with no enabled
whitelist entries sends "whitelist, empty" — which the agent reads as a lock. With schedules
that becomes a footgun: a "лекция 08:00–14:00 → whitelist" interval on a room whose list is
still empty would lock a classroom at eight in the morning because somebody saved a calendar.

So the rule becomes uniform: **an effective whitelist that resolves to an empty list is sent
as `free`, not as a lock.** A lock is a deliberate act on one machine (`computers.blocked`),
never an emergent property of a policy or a calendar. `computers.blocked` still sends
"whitelist, empty" and still means exactly what it meant.

The rejected alternative — refusing to save a schedule interval whose mode has no matching
applications — was considered and dropped: it makes the order of setup matter ("you must add
the programs before you may add the hours"), and it does not help the room that is already
misconfigured.

**Tzdata must be embedded.** `server/build.sh` builds `CGO_ENABLED=0` and the Dockerfile
runs the binary on Alpine, which ships no timezone database; `time.LoadLocation` would fail
for every room. `main.go` gains `import _ "time/tzdata"`. This is a one-line change and a
guaranteed production outage if forgotten, so it gets its own test that loads a non-UTC zone.

API: `GET/POST /api/v1/rooms/{roomID}/schedules`,
`PATCH/DELETE /api/v1/rooms/{roomID}/schedules/{scheduleID}`. `timezone` joins the room's
`PATCH` body and is validated against `time.LoadLocation`. Events: `schedule.added`,
`schedule.updated`, `schedule.removed`.

The room screen shows the effective mode **right now** and the next transition
("whitelist до 14:00, затем blacklist"), computed by the same function the sync handler
uses — one implementation, so the screen cannot disagree with the machines.

---

## 4. Temporary suspension

```sql
ALTER TABLE applications ADD COLUMN suspended_until TIMESTAMPTZ;
```

An entry is in force when `enabled AND (suspended_until IS NULL OR suspended_until <= now())`.
Nothing expires it — expiry is a comparison, so there is no job to run and no state that can
be left behind by a missed tick.

```
POST   /api/v1/rooms/{roomID}/applications/{appID}/suspend  {"minutes": 30}
DELETE /api/v1/rooms/{roomID}/applications/{appID}/suspend
```

`minutes` is capped at 1440 — a suspension longer than a day is a policy change, and should
be made as one, visibly.

The UI names it by what it does in each list, because one verb would be a lie in one of
them: on a **blacklist** entry the button is «Разрешить на 30 минут», on a **whitelist**
entry it is «Запретить на 30 минут». Both are the same column. The room screen shows a
countdown next to the suspended entry.

Events: `application.suspended` (payload carries `until`), `application.resumed`.

---

## 5. Application templates

```sql
CREATE TABLE app_templates (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (account_id, name)
);

CREATE TABLE app_template_items (
    template_id UUID NOT NULL REFERENCES app_templates(id) ON DELETE CASCADE,
    account_id  UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    PRIMARY KEY (template_id, name)
);
```

`account_id` is repeated on the items table for the same reason it is repeated on
`applications` and `events`: an RLS policy tests a column on the row, without a join.

**Templates are per-account, seeded at registration** — three starter sets («Игры»,
«Соцсети», «Мессенджеры») inserted in the same transaction that creates the account. The
alternative, a global table of built-ins with a nullable `account_id`, needs its own RLS
policy carving an exception into the one rule that currently has none, and buys only the
ability to edit the built-ins centrally. Per-account copies are also editable by the
customer, which is what a customer will want on the second day.

```
GET    /api/v1/templates
POST   /api/v1/templates                        {"name": "…", "items": ["steam.exe", …]}
PATCH  /api/v1/templates/{templateID}
DELETE /api/v1/templates/{templateID}
POST   /api/v1/rooms/{roomID}/applications/import
       {"template_id": "…", "list": "blacklist"}
       → 200 {"added": 12, "skipped": 3}
```

Import is an upsert: entries already present in that room and list are skipped, not
duplicated or reset. `UNIQUE (room_id, name, list)` already guarantees it at the schema
level; the handler reports the counts so the UI can say "добавлено 12, уже было 3".

Template management is manager-only. Importing into a room is available to a room `member`
(it is an ordinary edit of that room's rules), not to a `viewer`.

Events: `template.created`, `template.updated`, `template.removed`,
`application.template_imported`.

---

## 6. The `viewer` role

```sql
ALTER TABLE room_members DROP CONSTRAINT room_members_role_check;
ALTER TABLE room_members ADD  CONSTRAINT room_members_role_check
    CHECK (role IN ('member', 'viewer'));
```

A `viewer` sees one room the way a `member` does — its machines, their status, the blocking
log — and changes nothing in it.

Enforced the way `ManagerOnly` is enforced, and for the reason that middleware documents at
`server/middleware.go:134`: a per-handler check was forgotten twice. The guest-reachable
route group in `routes.go` splits in two, and a new `WriterOnly` middleware guards the
writing half:

```
/api/v1  (SessionAuth)
├── group: readable by owner, admin, member, viewer
│     GET /me, GET /rooms, GET /rooms/{id}, GET .../applications,
│     GET .../members, GET .../process-events, GET .../schedules,
│     GET /computers, GET /computers/{id}, GET /computers/{id}/process-events
├── group: WriterOnly — owner, admin, member
│     PATCH /rooms/{id}, POST/PATCH/DELETE .../applications, .../suspend,
│     POST .../applications/import, PATCH /computers/{id},
│     POST /computers/{id}/commands
└── group: ManagerOnly — owner, admin
      (unchanged, plus the new account-wide routes below)
```

`server/authz_test.go` walks the router and fails on any route it cannot classify. It gains
a third bucket, so the same property holds for the new role: a route added without a
decision fails the build rather than defaulting to reachable.

`tenant.go` gains `roleViewer = "viewer"`. Both new middlewares are allow-lists, never
"not a viewer" — a fifth role added later inherits nothing until somebody writes it down.

---

## 7. Command queue

`power_allowed` is a policy flag, not a command. "Выключить эту машину сейчас" has no
channel at all today.

```sql
CREATE TABLE commands (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id   UUID NOT NULL REFERENCES accounts(id)  ON DELETE CASCADE,
    computer_id  UUID NOT NULL REFERENCES computers(id) ON DELETE CASCADE,
    type         TEXT NOT NULL CHECK (type IN ('shutdown', 'reboot')),
    params       JSONB NOT NULL DEFAULT '{}'::jsonb,
    status       TEXT NOT NULL DEFAULT 'pending'
                 CHECK (status IN ('pending', 'sent', 'done', 'failed', 'expired')),
    issued_by    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    error        TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    delivered_at TIMESTAMPTZ,
    finished_at  TIMESTAMPTZ,
    expires_at   TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_commands_computer ON commands(computer_id, status);
```

A command is picked up on the next sync — 20 seconds in service mode — and **expires after
one hour** if the machine never comes back. A command with no expiry is a machine that
shuts down when it is next switched on a fortnight later, which is not what anybody pressed
the button for.

Delivery is at-least-once, and the agent must treat it that way: both commands are naturally
idempotent — the machine goes down either way. The status column moves `pending → sent` when
the sync response carries it, and `sent → done|failed` when the agent reports back on a
later sync.

The queue holds only these two, and only momentary actions belong in it. Updating the agent
looked like a third command and is not one: it is a desired state that stays true while a
machine is switched off, and an expiring command cannot express that. See §10.

Both require `power_allowed` on the room, and both are available to a room `member` — a
person who can lock a machine and rewrite its rules is not meaningfully restrained by being
denied its power button.

```
POST /api/v1/computers/{computerID}/commands  {"type": "shutdown"}
GET  /api/v1/computers/{computerID}/commands  — the last 20, with status
```

Events: `command.issued`, `command.finished` (payload carries the outcome).

---

## 8. Wire format changes

The SaaS design kept the agent protocol byte-identical on purpose. This document breaks
that, additively, and the compatibility rule is explicit: **every new field is optional in
both directions.** Go's `encoding/json` ignores unknown fields by default, so a deployed
agent that has never heard of `commands` keeps working against the new server, and the new
server accepts a body from an agent that sends neither `batch_id` nor `blocked`.

**Request** — `POST /agent/sync`, still capped at 64 KiB:

```json
{
  "runtime": {"uptime_s": 1234, "mode": "service", "agent_version": "3.1.0"},
  "batch_id": "b2f1…",
  "blocked": [
    {"process": "steam.exe", "reason": "blacklist", "count": 47,
     "first_at": "2026-09-09T14:02:11Z", "last_at": "2026-09-09T14:20:53Z"}
  ],
  "command_results": [
    {"id": "…", "status": "done"},
    {"id": "…", "status": "failed", "error": "access denied"}
  ]
}
```

**Response** — `200`:

```json
{
  "applications": [{"name": "steam.exe", "mode": "blacklist"}],
  "mode": "blacklist",
  "client": [{"name": "power", "status": true}],
  "commands": [{"id": "…", "type": "shutdown", "params": {}}],
  "agent": {"target_version": "3.1.0", "sha256": "9f2c…"}
}
```

The `agent` block is the automatic-update channel and is specified in full in §10; it is
omitted entirely when the machine is already on the target version or fails any of the four
gates described there. The request's `update_result` (also §10) reports the outcome of the
previous one.

`sync.json` on the agent keeps caching the policy exactly as it does now. Pending blocked
entries are **not** persisted across a restart: they live in memory only. A crash losing the
last minute of a kill log is acceptable; writing the log to disk every second on every
managed machine is not.

---

## 9. Fleet management

**Bulk assignment.** `PATCH /api/v1/computers` with
`{"ids": ["…"], "room_id": "…"|null, "blocked": true|false}` — at most 200 ids, applied in
one transaction, one event per computer so the audit feed stays per-machine. Manager-only,
like the single-computer move it generalises.

**Pagination for `/api/v1/computers`.** `limit` (default 50, max 200) and an opaque
`cursor`, ordered by `COALESCE(NULLIF(display_name, ''), hostname) ASC, id ASC` — the order
the list is displayed in, because a page that is sorted differently from its screen is not a
page. Existing filters (`room_id`, `blocked`) are unchanged and compose with it.

**Online/offline as an event.** `last_seen_at` is written on every sync but the *transition*
is invisible, so it can be neither read in the feed nor filtered on.

```sql
ALTER TABLE computers ADD COLUMN offline_since TIMESTAMPTZ;
```

A sweep on its own **60-second** ticker (the hourly maintenance loop is far too coarse for
this) sets `offline_since` and records `computer.offline` for every machine whose
`last_seen_at` is older than **three minutes** and whose `offline_since` is still null; the
sync handler clears it and records `computer.online` when a machine comes back. Three
minutes is nine missed syncs in service mode and eighteen in console mode — far enough past
a dropped packet or a slow boot that an ordinary reboot does not generate a pair of events.

The list's online indicator keeps deriving from `last_seen_at` directly — the column is for
the transition, not for the badge.

---

## 10. Agent versioning and automatic updates

Decided in review: the cabinet shows a banner when an update exists, a person presses it,
and the machines update themselves from there. The server keeps a real registry of versions
rather than a version number in an environment variable.

### The constraint that shapes everything

**A running Windows service cannot overwrite its own executable.** The file is locked for
as long as the process holds it. `tools/whitelist-gui/update.go:performUpdate` already
solves this, and it can only do so because the console is a *separate* process: it stops the
service, copies the new file over the old one, and starts the service again.

So the agent cannot update itself in place, and any design that pretends otherwise fails on
the first machine. It needs a second process.

**The second process is the agent's own binary, run from a copy, in a different mode.** On
deciding to update, the agent copies itself to
`%ProgramData%\ProcSentinel\update\updater.exe`, launches it detached with
`--apply-update <staged-exe> <target-path>`, and returns to its loop; the copy is not the
locked file, so it can do to the installed binary exactly what the console does. Reusing the
agent binary rather than shipping a separate updater means one more artifact to build, sign
and keep in the archive — which is one more thing to get out of sync — for no gain.

The update procedure itself is `performUpdate`'s, step for step: back up the current
executable to a timestamped directory under `%ProgramData%\ProcSentinel\backup`, stop the
service, copy, start, and on a failed start leave the service stopped with the backup
named in the error rather than run a half-written binary.

`agent/update_windows.go` therefore carries a copy of that logic, and it joins
`tools/whitelist-gui/builtin.go` on the short list of things this repo keeps in sync by
hand — the console and the agent are separate Go modules, so there is nowhere shared to put
it. `CLAUDE.md` records the pairing.

**An architecture change is never automatic.** `performUpdate` already refuses a 32↔64-bit
switch because the service's `ImagePath` would point at a filename that no longer exists.
The agent refuses it for the same reason and reports the update as failed with that
explanation; switching architecture stays a job for the console.

### The registry

```sql
CREATE TABLE agent_releases (
    version      TEXT NOT NULL,
    arch         TEXT NOT NULL CHECK (arch IN ('386', 'amd64')),
    sha256       TEXT NOT NULL,
    size_bytes   BIGINT NOT NULL,
    signed       BOOLEAN NOT NULL DEFAULT false,
    rollout_pct  SMALLINT NOT NULL DEFAULT 0 CHECK (rollout_pct BETWEEN 0 AND 100),
    withdrawn_at TIMESTAMPTZ,
    notes        TEXT NOT NULL DEFAULT '',
    published_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (version, arch)
);
```

The binary itself is stored on disk next to the server, not in Postgres — a 25 MB bytea per
release read out on every download is a waste of a database.

**This is product data, not customer data**, so it carries no `account_id` and no RLS
policy; `guardian_app` gets `SELECT` and nothing else. Writes happen through a new
`guardian-server release` subcommand, alongside the existing `migrate` one:

```sh
./guardian-server release add   --version 3.1.0 --arch amd64 --file ./procsentinel-agent64.exe
./guardian-server release rollout --version 3.1.0 --pct 10
./guardian-server release withdraw --version 3.1.0
```

A CLI rather than an API endpoint because the product has no platform-administrator role and
inventing one to publish a binary would add an authentication surface whose compromise is
remote code execution on every customer machine. Requiring a shell on the server is the
correct amount of privilege for the action.

### Who is offered an update, and who presses the button

Decided in review: **the cabinet shows that an update exists and a person applies it.**
The fleet does not update itself behind the operator's back.

That single decision removes two mechanisms the earlier draft carried — a nightly
maintenance window and an `agent_update` command — and replaces them with something
simpler and, on the failure that actually happens, better.

**Applying an update sets a desired state, not a queue of commands.** Pressing the button
writes the target version onto the selected machines:

```sql
ALTER TABLE computers ADD COLUMN desired_agent_version TEXT;
```

A machine below its desired version is offered the update on its next sync, whenever that
is — tonight, or on Monday when the classroom is switched back on. A queue of
`agent_update` commands cannot do this: §7 expires a command after an hour, precisely so
that a `shutdown` does not fire a fortnight late, and a class that is powered off at
17:00 would have every one of its updates expire unapplied. Wanting a machine on version
3.1.0 is a state that stays true while the machine is off; "shut down" is not.

`agent_update` therefore disappears from the `commands` table's type list. The queue keeps
the two power commands, which are the ones that genuinely are momentary.

**Two gates decide whether a machine is even offered:**

1. **The release is rolled out to it.** `hash(computer_id || version) % 100 < rollout_pct`,
   evaluated server-side and deterministic — a machine in the 10% wave stays in it, so
   raising the percentage only ever adds machines. `rollout_pct` now means *who is told
   about this version*, which is a clearer thing than it meant when updates were silent:
   the operator publishes at `0`, raises it to `10`, and only those customers see the
   banner at all.
2. **The machine is not pinned.** `computers.pinned_agent_version TEXT` — a machine that
   must not move. Cleared to rejoin the fleet.

**Unattended updating is an opt-in, and it is off.**
`accounts.agent_auto_update BOOLEAN NOT NULL DEFAULT false` sets the desired version
automatically as soon as a release is rolled out to the account, with no banner and nobody
pressing anything. It exists because the alternative failure is real — a customer who never
opens the cabinet is a customer whose fleet never updates — and it is off by default because
the review asked for the button. A customer who wants to stop being asked switches it on
once.

### The banner

```
GET  /api/v1/agent-updates
POST /api/v1/agent-updates/apply
```

Manager-only, both: a `viewer` or a room `member` must not be shown a control they cannot
use, and the update is account-wide in effect even when applied to one room.

`GET` is what the cabinet polls on every screen, and it answers in one round trip:

```json
{
  "version": "3.1.0",
  "notes": "…",
  "behind": 14,
  "by_room": [{"room_id": "…", "name": "Класс 2", "behind": 9}],
  "unassigned": 2,
  "in_progress": {"pending": 3, "failed": 1}
}
```

`version` absent means no banner. `behind` counts machines below the offered version that
pass both gates. `in_progress` is what turns the banner into a progress strip after the
press: it counts machines whose `desired_agent_version` is the target and whose installed
version is not yet, plus the ones that reported a failure — so the operator sees "3 ещё
обновляются, 1 не смогла" without opening anything.

`POST .../apply` takes a scope and nothing else:

```json
{"scope": "all"}                    | {"room_id": "…"} | {"computer_ids": ["…"]}
```

and returns `{"queued": 14}` — the number of machines whose `desired_agent_version` it set.
Machines already on the version, pinned machines and machines outside the wave are skipped
and counted separately in the response, because "я нажал, а обновилось 9 из 14" needs an
answer on the same screen.

Events: `agent.update_requested` (payload: version, scope, how many machines), then
`agent.update_succeeded` / `agent.update_failed` per machine.

### The wire

The `agent` block from §8:

```json
"agent": {"target_version": "3.1.0", "sha256": "9f2c…"}
```

Present only when the machine's `desired_agent_version` is set and differs from what it
reports running. The agent begins the update as soon as it sees the block — the moment was
already chosen, by a person, when they pressed the button.

**The agent is never given a URL.** It downloads from a fixed path on the `SERVER_ADDRESS`
it is already configured with — `GET /agent/download?version=3.1.0&arch=amd64`, agent-token
auth, the same credential as sync. A free-form URL in the sync response would mean anyone
who can write a row in `agent_releases` can point the entire fleet at a host they control:
that is a remote-code-execution primitive, and no operational convenience is worth handing
one out. With a fixed path the worst a bad row can do is serve a bad build from our own
server, which is what `sha256` and the rollout percentage are for.

**Verification before the swap, in this order:** the download's SHA-256 must equal the one
in the sync response; when `signed` is true the staged file must pass Authenticode
validation (`WinVerifyTrust`); and the staged binary must answer `--version` with the
expected string. Any failure discards the staged file, records the reason, and leaves the
installed agent untouched. The third check is cheap and catches the case the first two
cannot: a correctly-hashed, correctly-signed binary that simply does not run on this
machine.

The result comes back on the next sync:

```json
"update_result": {"from": "3.0.0", "to": "3.1.0", "status": "done"}
```

or `"status": "failed"` with an `error`. A success clears `desired_agent_version`; a failure
clears it too and records the reason, so a machine that cannot take a build is not caught in
a loop of downloading it every twenty seconds. Pressing the button again is a deliberate
retry.

### Stopping a bad release

The point of the registry. `agent_releases` accumulates outcomes:

```sql
ALTER TABLE agent_releases ADD COLUMN success_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE agent_releases ADD COLUMN failure_count INTEGER NOT NULL DEFAULT 0;
```

When a release has **at least 10 reported outcomes and more than 20% of them are failures**,
the server sets `withdrawn_at` itself: the banner disappears, no further machine is offered
it, and any `desired_agent_version` still pointing at it is cleared. Automatic, because a
bad build discovered by the twentieth machine should not reach the two hundredth while the
operator reads about it.

A withdrawn release is not un-installed: machines already running it keep running it.
Rolling back is publishing the older version again with a higher `rollout_pct` — the server
offers a `target_version` lower than the installed one only when the release row is marked
`allow_downgrade`, so an ordinary rollout can never move a machine backwards by accident.

The **Computers** screen gains an `agent_version` column and a filter, which it needed
anyway, and the account settings screen shows how many machines are on each version and how
many failed.

### The endpoints this needs

`accounts.agent_auto_update` is an account setting, and the account has no settings endpoint
at all today — `PATCH /api/v1/account` does not exist. It arrives here:

```
GET   /api/v1/account          — name, plan, computer limit, agent_auto_update
PATCH /api/v1/account          — name, agent_auto_update
```

Manager-only, and the natural home for the account-level fields that later screens
(billing, above all) will need.

The rest:

```
GET   /api/v1/agent-releases           — versions, rollout %, install counts (manager)
PATCH /api/v1/computers/{id}           — gains pinned_agent_version (manager)
GET   /agent/download?version=&arch=   — agent-token auth, streams the binary
```

`GET /agent/download` is the one route in the agent group besides sync and enroll. It is
rate-limited per agent token, streams from `AGENT_RELEASE_DIR` on disk, and refuses a
version that is withdrawn or that this machine is not currently being offered — a valid
agent token is permission to fetch *the build the server chose for this machine*, not to
enumerate the release directory.


## 11. Security screen

Confirmed in review: **no certificates**. This screen holds the account's secrets and the
means of revoking them.

**Sessions.** `sessions` needs an identifier the cabinet can name without handling a token
digest:

```sql
ALTER TABLE sessions ADD COLUMN id UUID NOT NULL UNIQUE DEFAULT gen_random_uuid();
```

```
GET    /api/v1/account/sessions            — this user's sessions: created, last used, ip, user agent
DELETE /api/v1/account/sessions/{sessionID}
DELETE /api/v1/account/sessions            — all except the calling one
```

Session auth, not manager-only: these are the *caller's own* sessions. The current one is
flagged `"current": true` so the UI can label it rather than let somebody log themselves out
by accident.

**Binding tokens.** Two gaps: there is no way to list them, and no way to tell whether one
has been used.

```sql
ALTER TABLE binding_tokens ADD COLUMN use_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE binding_tokens ADD COLUMN last_used_at TIMESTAMPTZ;
```

Incremented in the enrollment transaction. `GET /api/v1/binding-tokens` lists the active
ones with their creation time, expiry, use count and last use — so "этим токеном добавлено
14 машин, последняя вчера" is answerable, which is the whole reason a reusable
account-wide token needs a screen at all. The plaintext token is shown once, at creation,
and never again.

**Sign-in history.** The Security screen's third block is the `events` feed filtered to the
security types. Two new ones are recorded: `auth.login` (payload: ip, user agent) and
`auth.password_changed`. Failed logins are **not** recorded as events — a table anyone can
write to by guessing an email is a denial-of-service on the audit feed; they stay in the
structured log, where the rate limiter already puts them.

---

## Errors and degradation

- **Sync telemetry never fails a sync.** A malformed `blocked` array, an over-long process
  name, a bad `reason`, a duplicate `batch_id` — every one of these drops data and returns
  `200`. The policy the machine enforces must not depend on the log it is shipping.
- **Mail is down.** The row is already committed when the send is attempted (see **Sending
  mail**), so the endpoint returns `503` naming mail as the cause and the invitation stays
  **pending** — visible in the list, resendable with
  `POST /api/v1/invitations/{id}/resend`. Rolling it back instead would mean a manager who
  hit send during a SendGrid incident has nothing to show for it and no way to retry except
  to type the address again. A password-reset token behaves the same way: it stays valid,
  and the user simply asks for another.
- **A command outlives its machine.** `expires_at` closes it after an hour; the sweep that
  writes `computer.offline` also marks such commands `expired`.
- **Schedule with an unloadable timezone.** Validated on write. If a room somehow holds an
  invalid zone at read time, evaluation falls back to the room's static `mode` and logs an
  error — a room that keeps its old policy is recoverable, a room that panics the sync
  handler is a fleet-wide outage.

## Testing

The existing floor applies, and every new endpoint is subject to it:

- **`server/isolation_test.go` gains a row per new account-scoped endpoint.** Mandatory, as
  the repo's rules already say: account A with a valid session reaches for account B's
  object and gets `404`.
- **`server/authz_test.go` gains its third bucket** (read / writer / manager) and keeps
  failing the build on an unclassified route.
- **Schedule evaluation is table-driven and unit-tested**, including a non-UTC zone, a DST
  boundary, an overlap resolved by the latest `starts_at`, and the empty-whitelist case that
  must resolve to `free` and not to a lock.
- **Agent tests** (`agent/agent_test.go`, `httptest`, no Windows needed) cover the
  aggregation window, the 500-entry cap and its overflow marker, and batch idempotency: a
  sync whose response is dropped must not double-count.
- **Update gating is table-driven and unit-tested on the server**: the wave hash is stable
  for a given `(computer_id, version)` across runs, raising `rollout_pct` only ever adds
  machines, a pinned machine is never selected, a withdrawn release is never offered, and
  the automatic-withdrawal threshold fires at the documented counts and not before.
- **The agent's verification chain is tested without Windows**: a wrong SHA-256, a staged
  binary that reports the wrong `--version`, and an architecture mismatch each leave the
  installed agent untouched and produce a `failed` result with the reason. The swap itself
  (`agent/update_windows.go`) stays hand-tested, like the console's, and says so.
- **`server/fakeagent`** learns to send a blocked batch, execute a command, and report an
  update result, so the whole loop is exercisable without a Windows machine.
- Postgres-backed handler tests as usual — RLS cannot be verified against a mock.

## Implementation order

Each step ends with the suite green; none of them requires the next one to be useful.

1. **Blocking log.** Migration, sync-body extension, agent aggregation, the two endpoints,
   the room tab. The largest single item and the one the product is missing most.
2. **Mail package, verification, password reset and change.** Nothing depends on it, and
   everything after it can be tested with real invitations.
3. **Invitations**, on top of 2.
4. **`viewer` role and the route-group split.** Before the new writing routes multiply, so
   they are classified as they are written rather than retrofitted.
5. **Schedules and temporary suspension** — both touch the same policy-evaluation function,
   so they land together.
6. **Templates.**
7. **Command queue, then agent versioning and automatic updates** — the second half of the
   wire change. Split into three landings that are each useful alone: the `commands` table
   and the two power commands; then the `agent_releases` registry, the `release` subcommand,
   the download endpoint and version reporting in the cabinet; then the agent's own updater
   mode, the banner and its apply endpoint, and the automatic withdrawal of a failing
   release. The last of these
   is the only part that can break a customer's machine, so it lands last and behind
   `rollout_pct = 0` — published, offered to nobody, raised by hand once the operator has
   watched it work on one machine.
8. **Fleet management**: bulk assignment, pagination, the offline sweep.
9. **Security screen's server side**: session listing, binding-token counters, the two auth
   events.

## Documentation

Per the repo's standing rule, each step updates `specs/api.md` (new routes and the wire
format), `specs/server.md` (schema, RLS, middleware, maintenance), `specs/agent.md`
(aggregation, commands, the `.env` surface if it changes) and `CLAUDE.md` in the same commit
as the code.

Step 2 additionally adds `SMTP_URL`, `MAIL_FROM` and `MAIL_TRANSPORT` to
`dist/server/server.env` and to the environment block of
`dist/server/docker-compose.yml`, alongside the existing `DATABASE_URL` /
`CABINET_ORIGIN` / `TRUSTED_PROXIES`; step 7 adds `AGENT_RELEASE_DIR` (where the published
agent binaries live on disk) the same way. A key that exists only in Go and never in the
template is a key the next deployment forgets.

## Open questions

- ~~**SMTP provider.**~~ Decided in review: **SendGrid over SMTP**. See **Sending mail**.
  What remains is operational, not architectural — domain authentication must be set up
  before step 2 is deployed, or the mail will send and not arrive.
- ~~**Does `agent_update` ship in v1?**~~ Decided in review: the versioning registry ships,
  and the update is applied from a banner in the cabinet rather than by a command or on a
  timer. §10.
- ~~**Is a nightly update window the right gate?**~~ Dropped with the automatic rollout: the
  moment is chosen by whoever presses the button.
- **Should `agent_auto_update` exist at all?** §10 keeps it as an opt-in, default off, for
  the customer who never opens the cabinet and whose fleet would otherwise never move. It is
  the one piece of §10 that the review did not ask for, and it is the easiest thing in this
  document to cut if it reads as scope rather than as insurance.
