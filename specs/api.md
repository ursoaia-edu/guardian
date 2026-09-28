# API Reference

Base URL: `http://<host>:8080`

This is a multi-tenant API. There is no shared client/admin token. Two
credential types exist instead, and the endpoints below are grouped by which
one they require — see `specs/server.md` for the full reasoning behind each.

- **Session** — a cabinet user's login. Sent as the `guardian_session` cookie
  (browser) or `Authorization: Bearer <session-token>` (mobile/API clients).
  Optionally paired with `X-Guardian-Account: <account-id>` to act in an
  account other than the caller's default (see below).
- **Agent token** — one credential per enrolled machine, obtained from
  `POST /agent/enroll`. Sent as `Authorization: Bearer <agent-token>`.
- **Binding token** — carried in the *body* of `POST /agent/enroll` itself,
  not a header. It is a per-account, long-lived token embedded in the
  installer, and it is what names the account a new machine enrolls into.

Responses are built from the types in `server/responses.go`, not from database
rows, so a column added to a table does not appear in the API by itself.
`account_id` is on no response body at all: a client never names an account, in
either direction.

`account_id` is never accepted as a URL, query, or body parameter anywhere in
this API. It is always resolved server-side from the session or the agent
token.

### `X-Guardian-Account`

A session may be able to act in more than one account — the user's own, plus
any account that has shared a room with them as a guest. Omit the header and
the server picks the caller's own account (owner/admin sorts before guest
memberships); to act as a guest in someone else's account, send that
account's id in this header. The value must be one of the ids already
returned by `GET /api/v1/me`'s `accounts` list — anything else, including a
real account id the caller has no membership in, answers `404 Account not
found`. This header can only *select* among memberships the session has
already proven; it cannot be used to reach an account the caller has no
claim on.

---

## Unauthenticated

### `GET /health`

Health check for load balancers and monitoring. It pings PostgreSQL (2-second
timeout) and reports `200` only when the database answers, so an orchestrator
stops routing to a server that can serve nothing but this route. Successful
probes are not written to the request log.

**Response** `200`
```json
{"status": "ok"}
```

**Response** `503` — the database is unreachable
```json
{"status": "degraded", "database": "unreachable"}
```

---

## Auth (`/api/v1/auth`) — no auth required

### `POST /api/v1/auth/register`

Create a user and a brand-new account they own (`role: "owner"`). Does **not**
sign the user in — call `/api/v1/auth/login` afterwards.

**Request**
```json
{"email": "parent@example.com", "password": "at-least-8-chars", "name": "Parent"}
```
`name` is optional; the account is named after it, or after the email if
omitted.

**Response** `201`
```json
{"account_id": "…", "user_id": "…"}
```

**Response** `400` — the email does not parse as a plain address (a bare `@`
is not a check: `parent@` and `Parent <p@example.com>` are both refused), or
the password is under 8 or over 1024 characters.
**Response** `409` — that email is already registered.

### `POST /api/v1/auth/login`

**Request**
```json
{"email": "parent@example.com", "password": "at-least-8-chars"}
```
`"client": "mobile"` may be added to ask for the session token in the response
body — for a client that cannot use a cookie jar and stores the token in the
Keychain/Keystore instead.

**Response** `200` — always sets the `guardian_session` cookie (`HttpOnly`,
`Secure`, `SameSite=Lax`, 30-day expiry). The body carries the token **only**
for `"client": "mobile"`; returning it to a browser would hand script on the
cabinet's origin the very credential `HttpOnly` exists to keep from it.
```json
{"status": "ok"}
```
```json
{"token": "…"}
```

**Response** `401`
```json
{"error": "Invalid email or password"}
```
Returned identically for an unknown email and a wrong password, in
constant-ish time — see `specs/server.md`.

### `POST /api/v1/auth/logout`

Revokes the current session (if any) and clears the cookie.

**Response** `204`

---

## Cabinet (`/api/v1`) — session auth

### `GET /api/v1/me`

**Response** `200`
```json
{
  "user_id": "…",
  "account_id": "…",
  "role": "owner",
  "accounts": [
    {"account_id": "…", "name": "Parent", "role": "owner"},
    {"account_id": "…", "name": "The Smiths", "role": "member"}
  ]
}
```
`role` here is `"owner"`, `"admin"`, or `"member"` (a room guest — see
**Roles** in `specs/server.md`). `accounts` lists every account this session
may act in, ordered owner/admin first, each exactly once and carrying the
name an account switcher needs. A user reachable through two paths — an
admin who is also a guest of one of that account's rooms — is listed once
under the stronger role.

**Response** `401` — not signed in.

### `GET /api/v1/rooms`

Lists rooms in the active account. An `owner`/`admin` sees every room; a
`member` (guest) sees only rooms they were granted.

**Response** `200`
```json
{"rooms": [{"id": "…", "name": "Kids room", "mode": "blacklist", "protection_enabled": false, "power_allowed": true, "created_at": "…"}]}
```

### `POST /api/v1/rooms`

`owner`/`admin` only.

**Request**
```json
{"name": "Kids room"}
```
Created with `mode: "blacklist"`, `protection_enabled: false`,
`power_allowed: true`; use `PATCH` to change any of those.

**Response** `201` — the created room (same shape as above).
**Response** `400` — empty name.
**Response** `403` — caller is a guest (`member`).

### `GET /api/v1/rooms/{roomID}`

**Response** `200` — the room. **Response** `404` — not in this account, or a
guest not granted this room. The two cases are indistinguishable by design.

### `PATCH /api/v1/rooms/{roomID}`

**Request** (all fields optional)
```json
{"name": "New name", "mode": "whitelist", "protection_enabled": true, "power_allowed": false}
```
`mode`, if present, must be `"blacklist"` or `"whitelist"`. `name`, if
present, cannot be empty/whitespace-only.

**Response** `200` — the updated room. **Response** `400` / `404` as above.

### `DELETE /api/v1/rooms/{roomID}`

`owner`/`admin` only. Any computer in the room is unassigned (`room_id` set
to `NULL`), not deleted.

**Response** `204`. **Response** `403` / `404` as above.

### `GET /api/v1/rooms/{roomID}/applications`

Lists that room's blacklist/whitelist entries.

**Response** `200`
```json
{"applications": [{"id": "…", "room_id": "…", "name": "steam.exe", "list": "blacklist", "enabled": true, "created_at": "…"}]}
```

### `POST /api/v1/rooms/{roomID}/applications`

**Request**
```json
{"name": "steam.exe", "list": "blacklist"}
```
`list` defaults to `"blacklist"`; must be `"blacklist"` or `"whitelist"`. The
same name may exist in both lists of the same room independently.

**Response** `201` — the created entry. **Response** `400` / `404` as above.

### `PATCH /api/v1/rooms/{roomID}/applications/{appID}`

Switches a rule off without deleting it; `/agent/sync` stops sending a
disabled entry, and switching it back on restores it unchanged.

**Request**
```json
{"enabled": false}
```

**Response** `200` — the updated application. **Response** `400` — no
`enabled` in the body. **Response** `404` — room not visible, or no such
application in that room.

### `DELETE /api/v1/rooms/{roomID}/applications/{appID}`

**Response** `204`. **Response** `404` — room not visible, or no such
application in that room.

### `GET /api/v1/rooms/{roomID}/members`

Lists the guests granted this room.

**Response** `200`
```json
{"members": [{"id": "…", "email": "guest@example.com", "name": "Grandma"}]}
```

### `POST /api/v1/rooms/{roomID}/members`

`owner`/`admin` only. Grants an existing Guardian user guest access to this
one room — it does not invite by email; the user must already have an
account.

**Request**
```json
{"email": "guest@example.com"}
```

**Response** `201`. **Response** `404` — room not found, or no Guardian user
with that email yet. **Response** `403` — caller is a guest.

### `DELETE /api/v1/rooms/{roomID}/members/{userID}`

`owner`/`admin` only. Revokes that guest's access to this room (their session
elsewhere is unaffected; the next request that touches this account simply
no longer finds it in `X-Guardian-Account`'s allowed list).

**Response** `204`. **Response** `403` / `404` as above.

### `GET /api/v1/computers`

An `owner`/`admin` sees every computer in the account; a `member` sees only
computers currently in a room they were granted.

**Response** `200`
```json
{
  "computers": [{
    "id": "…", "room_id": "…",
    "display_name": "", "machine_guid": "…", "hostname": "FAKE-PC",
    "os_name": "Windows 11", "os_build": "22631", "arch": "amd64",
    "agent_version": "1.4.0", "hardware": {"cpu": "…"}, "runtime": {"uptime_s": 1234},
    "blocked": false, "enrolled_at": "…", "last_seen_at": "…"
  }]
}
```
`token_hash` (the agent's credential digest) is never present in the
response — it is stripped at the struct level, not filtered per-handler.

### `GET /api/v1/computers/{computerID}`

One machine. A `member` (guest) may read only machines currently sitting in a
room they were granted.

**Response** `200` — the computer, in the shape above. **Response** `404` —
not in this account, or not visible to this guest.

### `PATCH /api/v1/computers/{computerID}`

**Request** (all fields optional; only present keys are applied)
```json
{"display_name": "Living room PC", "room_id": "…", "blocked": true}
```
`room_id: null` explicitly (as opposed to the key being absent) unassigns the
computer from every room. A `member` (guest) may move a computer between
rooms they were granted, but may not unassign it from every room, and may
not move it into a room they were not granted — both are a `404`/`403`
depending on which guard trips.

**Response** `200` — the updated computer. **Response** `404` — not in this
account, or (for a guest) not currently visible to them.

### `DELETE /api/v1/computers/{computerID}`

`owner`/`admin` only. Unenrols the machine. The row *is* the credential, so
this revokes that one machine's agent token and nothing else — the
per-machine counterpart to the account-wide binding-token kill switch.

The machine does not thereby go free: its next sync is a `401`, and the agent
treats a `401` as "keep enforcing the last known policy" (see
`specs/agent.md`). Bringing it back means running the installer again.

**Response** `204`. **Response** `404` — not in this account.

### `GET /api/v1/account/members`

`owner`/`admin` only. The account's own members — the owner and any admins.
Distinct from `/rooms/{roomID}/members`, which grants one room to a guest.

**Response** `200`
```json
{"members": [{"id": "…", "email": "parent@example.com", "name": "Parent", "role": "owner", "added_at": "…"}]}
```
Owner first, then admins by email.

### `POST /api/v1/account/members`

`owner`/`admin` only. Grants an existing Guardian user the `admin` role on
this account: every room, the whole computer pool, invitations — everything
except that ownership itself is not transferable in v1, so there is no role
parameter and no way to mint a second owner.

**Request**
```json
{"email": "partner@example.com"}
```

**Response** `201`. **Response** `404` — no Guardian user with that email
yet. **Response** `409` — already a member of this account.

### `DELETE /api/v1/account/members/{userID}`

`owner`/`admin` only. Revokes an admin. The owner row cannot be removed —
an account with no owner has nobody who can be billed or delete it — and the
attempt is a `404` like any other member that is not there.

**Response** `204`. **Response** `404` — not an admin of this account.

### `POST /api/v1/binding-tokens`

**`403` until the caller's email address is confirmed** — see
`POST /api/v1/auth/verify`. This and inviting somebody are the two actions that
reach outside the account: one adds machines, the other adds people. Everything
else in the cabinet works unconfirmed, because blocking it all would mean a
customer who mistypes their address cannot see what they bought.

`owner`/`admin` only. Mints a new installer token for this account, valid
1 year.

**Response** `201`
```json
{"token": "…"}
```
This is the only time the plaintext is ever returned — only its SHA-256
digest is stored.

### `DELETE /api/v1/binding-tokens`

`owner`/`admin` only. Revokes **every** binding token this account has ever
minted (all rows marked `revoked_at`). Machines already enrolled are
unaffected — they carry their own per-machine token by then; revoking one of
those individually is `DELETE /api/v1/computers/{computerID}`.

**Response** `204`.

### `GET /api/v1/installer`

**`403` until the caller's email address is confirmed**, like
`POST /api/v1/binding-tokens`. `owner`/`admin` only.

Mints a new binding token for this account (valid 1 year, recorded as a
`binding_token.created` event with `"source": "installer"`) and returns the
server's reference installer archive with `agent.env` replaced by one carrying
it:

```
SERVER_ADDRESS=<AGENT_SERVER_ADDRESS, or the first CABINET_ORIGIN>
BINDING_TOKEN=<the new token>
CHECK_INTERVAL=30
```

Lines end in `\r\n` — the file is read on Windows, and the agent trims them.

**Response** `200`, `Content-Type: application/zip`,
`Content-Disposition: attachment; filename="Guardian-<account-name>.zip"` (the
name reduced to ASCII letters, digits and dashes; plain `Guardian.zip` when
nothing is left, e.g. a Cyrillic account name),
`Cache-Control: no-store` (the archive carries a live credential).

**`503`** `{"error": "No installer is available from this server yet"}` when
the server has no usable reference archive (`INSTALLER_ARCHIVE` unset, missing,
over 256 MiB, or holding no agent executable) or no address to write into
`agent.env`. The archive is checked **before** a token is minted, so a failed
download leaves no credential behind. See **Installer** in `specs/server.md`.

### `GET /api/v1/events`

`owner`/`admin` only. Account activity, most recent first.

**Query params:**
- `limit` — page size, default 50, capped at 200.
- `cursor` — from a previous response's `next_cursor`. Opaque: it names a
  position by `(created_at, id)`, not an offset, so rows arriving while
  somebody pages through the feed cannot make it repeat or skip.

**Response** `200`
```json
{
  "events": [{"id": "…", "room_id": null, "computer_id": "…", "type": "computer.enrolled", "payload": {"hostname": "FAKE-PC", "agent_version": "3.0.0"}, "created_at": "…"}],
  "next_cursor": "1757366400000000000_0d9a…"
}
```
`next_cursor` is present only when the page was full; its absence means the
end of the feed.

Events older than 180 days are purged hourly (`specs/server.md`,
**Maintenance**).

Recorded types: `room.created`, `room.updated`, `room.deleted`,
`application.added`, `application.updated`, `application.removed`,
`computer.enrolled`, `computer.token_rotated` (a known machine enrolled
again, taking a new credential — see `specs/server.md`), `computer.blocked`,
`computer.unblocked`,
`computer.assigned`, `computer.renamed`, `computer.removed`,
`room_member.granted`, `room_member.revoked`, `account_member.added`,
`account_member.removed`, `binding_token.created`, `binding_token.revoked`.
Every one is written in the same transaction as the change it describes, so
the feed cannot disagree with the data.

---

### `GET /api/v1/rooms/{roomID}/process-events`
### `GET /api/v1/computers/{computerID}/process-events`

Session auth, **guest-reachable**: the blocking log for a room somebody was
deliberately given is exactly what they were given it for. The room endpoint
is gated by the same room-visibility check as the rest of `/rooms/{roomID}/…`;
the computer endpoint reads the machine first, so a guest cannot read the
history of an unassigned machine or one in a room they were not granted.
Either way "not yours" and "not there" are both `404`.

This is the blocking log — what a policy actually closed — and it is a
different feed from `/events` above, which records what administrators did.
Rows are purged after 30 days rather than 180 (`specs/server.md`,
**Maintenance**).

The two endpoints exist separately because a machine in no room has `room_id`
NULL on every row, and the room query can never match it. Its history is read
on the computer screen.

**Query params** (all optional, and they compose):

| Param | Meaning |
|---|---|
| `process` | Exact process name. |
| `reason` | `blacklist`, `whitelist`, `locked` or `overflow`. Anything else is ignored. |
| `computer_id` | Room endpoint only — narrows to one machine in that room. |
| `since`, `until` | RFC3339, compared against `created_at`. |
| `limit` | Page size, default 50, capped at 200. |
| `cursor` | From a previous response's `next_cursor`. |

A malformed filter is **ignored**, not rejected: this is a log viewer, and a
mistyped filter that returns the unfiltered feed is friendlier than a `400`
with no rows. The cursor is the exception — it is the server's own opaque
token, so a broken one is `400 Invalid cursor`, because it means the client is
out of step.

**Response** `200`
```json
{
  "process_events": [{
    "id": "…", "computer_id": "…", "computer_name": "LAB-A-01", "room_id": "…",
    "process": "steam.exe", "reason": "blacklist", "count": 47,
    "first_at": "2026-09-09T14:00:00Z", "last_at": "2026-09-09T14:00:46Z",
    "created_at": "2026-09-09T14:01:03Z"
  }],
  "next_cursor": "1757366400000000000_0d9a…"
}
```

`computer_name` is resolved server-side (the machine's display name, or its
hostname when it has none); the alternative is the cabinet issuing an N+1 of
lookups to render one page.

Ordered by `created_at DESC`, never by the agent's own `first_at`/`last_at` —
an agent's clock can be wrong by years, and those two are shown, not trusted.
`next_cursor` is present only when the page was full; its absence means the end
of the feed.

---

## Agent

### `POST /agent/enroll`

No header credential; the binding token in the body is this route's gate.

**Request**
```json
{
  "binding_token": "…",
  "machine_guid": "a machine-stable identifier",
  "hostname": "FAKE-PC",
  "os_name": "Windows 11",
  "os_build": "22631",
  "arch": "amd64",
  "agent_version": "3.0.0",
  "hardware": {"cpu_count": 8, "go_os": "windows"}
}
```
Re-enrolling the same `machine_guid` on the same account updates that
machine's row (and mints it a new agent token) rather than creating a second
one, and does not count against the account's computer limit. `hardware`
over 64 KiB is stored as `{}`.

**Response** `201`
```json
{"agent_token": "…"}
```
**Response** `401` — binding token unknown, expired, or revoked.
**Response** `402` — account is at its `computer_limit` and this is a new
machine.

### `POST /api/v1/auth/verify`

Unauthenticated, rate-limited per IP at the login endpoint's rate. The token in
the body is the gate, exactly as the binding token is for enrolment: the link
is opened by somebody who is very often not signed in, and often in a different
browser from the one they registered in.

**Request** `{"token": "…"}` → `204`

A link lives **48 hours** and works **once**. Unknown, spent and expired are one
answer — `400 {"error": "This confirmation link is not valid or has already
been used"}` — because distinguishing them tells somebody holding a stolen link
which kind of wrong it is.

The address is confirmed only if it still matches the one the link was sent to,
so a link sent to an old address cannot confirm a new one.

### `POST /api/v1/auth/password/forgot`

Unauthenticated, rate-limited per IP at the login endpoint's rate.

**Request** `{"email": "…"}` → **`204`, always**

The same status and the same empty body whether or not the address has an
account, and the miss path spends the same argon2id work the hit path does.
This endpoint would otherwise be an account-enumeration oracle — by status, or
by timing — undoing the care the registration endpoint already takes. Mail is
sent only when there is somebody to send it to.

A reset link lives **one hour** and works **once**. Minting supersedes: asking
twice invalidates the first link, so a mailbox never holds two working ones.

### `POST /api/v1/auth/password/reset`

Unauthenticated, rate-limited at the same rate.

**Request** `{"token": "…", "password": "…"}` → `204`

`400` for a token that is unknown, spent or expired — one answer for all three
— and `400` for a password under 8 or over the maximum.

**Completing a reset deletes every session that user has**, on every device.
This is the point of having server-side sessions at all: a reset is the one
moment somebody might be taking an account back from whoever has been in it.

### `POST /api/v1/account/password`

Session auth, guest-reachable — a guest changes their own password like anybody
else; the route touches the caller's own user row and no account-wide state.

**Request** `{"current": "…", "new": "…"}` → `204`

`403` when `current` is wrong: the session is fine, the claim about the current
password is not, and a `401` would bounce the cabinet to the sign-in screen
over a mistyped field.

**It deletes every session except the calling one.** Signing somebody out of
the browser they are standing in front of, as a consequence of their own
deliberate act, is a bug that reads as one.

### `POST /api/v1/account/verify/resend`

Session auth, guest-reachable, rate-limited. Sends the link again to the
caller's own address; `204` whether or not it was needed, so a re-clicker
learns nothing and the cabinet needs no second code path. An address that is
already confirmed is a no-op.

It lives under `/account/` rather than beside `/auth/verify` on purpose:
`TestEveryAPIRouteIsClassified` skips everything under `/api/v1/auth/` ("no
session yet, so no role to check"), and a session-authenticated route there
would be invisible to the one test whose job is to catch an unclassified route.

### `POST /agent/sync`

Agent-token auth. Returns the wire format the single-tenant server spoke —
**unchanged on purpose**, so moving the agent over was a URL and credential
change, not a protocol change.

**Request** (body capped at 64 KiB)
```json
{
  "runtime": {"uptime_s": 1234, "mode": "service", "agent_version": "3.0.0"},
  "batch_id": "5f3c9a…",
  "blocked": [
    {"process": "steam.exe", "reason": "blacklist", "count": 47,
     "first_at": "2026-09-09T14:00:00Z", "last_at": "2026-09-09T14:00:46Z"}
  ]
}
```
`runtime` is live telemetry, recorded against the computer's `runtime`
column regardless of the policy computed below. It travels in a body rather
than a query string so it is not written to proxy access logs and not
subject to URL length limits. Telemetry never fails a sync: no body, a body
over the cap, invalid JSON, a non-object, an object over 64 KiB, or anything
containing a NUL is silently replaced with `{}`. `GET` on this path is `405`.

`batch_id` and `blocked` are optional and travel together — the blocking log
(`process_events`). Both are absent when the agent has killed nothing, so the
body is exactly what a pre-batch server always saw.

| Field | Rule |
|---|---|
| `batch_id` | Opaque, at most 64 characters. A batch whose id matches the computer's `last_event_batch` is **ignored**: a sync whose response was lost is retried under the same id, and the server has already committed it. An id over the cap means the batch cannot be deduplicated, so it is dropped rather than stored. |
| `blocked[]` | At most 200 items per sync; the rest are dropped and logged. The agent holds the remainder and sends it next time. |
| `process` | Required, trimmed, truncated at 260 characters. An empty one drops the item. |
| `reason` | `blacklist`, `whitelist` or `overflow`. Anything else drops the item — including **`locked`, which an agent may never claim**: it cannot tell a locked machine from a whitelist that allows nothing, since both reach it as `mode: "whitelist"` with an empty list. The server rewrites `whitelist` to `locked` itself when the reporting computer is `blocked`, because only the server knows. |
| `count` | Must be ≥ 1, clamped at 100,000. A machine claiming more between two syncs is broken, and storing the claim verbatim only spreads the breakage into the UI. |
| `first_at`, `last_at` | Clamped into `[now − 24h, now + 5min]`; anything outside, or absent, becomes the server's `now`. They are stored and shown because they are what an operator wants to read, but they never order the feed — `created_at` does, because an agent's clock can be wrong by years. `last_at` earlier than `first_at` is pulled up to it. |

The same rule covers all of it: **the batch never fails a sync.** Every
malformation above drops data and still answers `200`, because a machine's
policy must never depend on the log it is shipping.

`overflow` carries the agent's own admission that its buffer dropped entries:
`process` is `guardian.log_overflow` and `count` is how many went.

**Response** `200`
```json
{
  "applications": [{"name": "steam.exe", "mode": "blacklist"}],
  "mode": "blacklist",
  "client": [{"name": "power", "status": true}]
}
```

**Policy logic**, evaluated in this order:
1. Computer is `blocked: true` → `mode: "whitelist"`, empty `applications`
   and `client`. (Locked, not unmanaged — see `specs/server.md`.)
2. Computer has no `room_id` → `mode: "free"`, empty `applications` and
   `client`. A freshly enrolled machine enforces nothing until it is placed
   in a room.
3. Room exists but `protection_enabled: false` → same as above.
4. Otherwise → `mode` is the room's `mode`; `applications` is every enabled
   entry in that room whose `list` matches the room's `mode`; `client` is a
   single `power` entry reflecting the room's `power_allowed`.

---

## Error Responses

All endpoints return errors in this format:

```json
{"error": "Description of the error"}
```

Common HTTP status codes:
- `400` — invalid request (bad JSON, missing/invalid fields)
- `401` — not signed in, or an invalid/expired credential
- `402` — account is at its computer limit (enrollment only)
- `403` — signed in, but the account role does not permit this action
- `404` — resource not found, not in the caller's account, or (for a guest)
  not granted to them — these are indistinguishable by design
- `409` — conflict (registration with an already-used email)
- `500` — internal server error
