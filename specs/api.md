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

**Response** `400` — invalid email, or password under 8 characters.
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
    {"account_id": "…", "role": "owner"},
    {"account_id": "…", "role": "member"}
  ]
}
```
`role` here is `"owner"`, `"admin"`, or `"member"` (a room guest — see
**Roles** in `specs/server.md`). `accounts` lists every account this session
may act in, ordered owner/admin first.

**Response** `401` — not signed in.

### `GET /api/v1/rooms`

Lists rooms in the active account. An `owner`/`admin` sees every room; a
`member` (guest) sees only rooms they were granted.

**Response** `200`
```json
{"rooms": [{"id": "…", "account_id": "…", "name": "Kids room", "mode": "blacklist", "protection_enabled": false, "power_allowed": true, "created_at": "…"}]}
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
{"applications": [{"id": "…", "account_id": "…", "room_id": "…", "name": "steam.exe", "list": "blacklist", "enabled": true, "created_at": "…"}]}
```

### `POST /api/v1/rooms/{roomID}/applications`

**Request**
```json
{"name": "steam.exe", "list": "blacklist"}
```
`list` defaults to `"blacklist"`; must be `"blacklist"` or `"whitelist"`. The
same name may exist in both lists of the same room independently.

**Response** `201` — the created entry. **Response** `400` / `404` as above.

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
    "id": "…", "account_id": "…", "room_id": "…",
    "display_name": "", "machine_guid": "…", "hostname": "FAKE-PC",
    "os_name": "Windows 11", "os_build": "22631", "arch": "amd64",
    "agent_version": "1.4.0", "hardware": {"cpu": "…"}, "runtime": {"uptime_s": 1234},
    "blocked": false, "enrolled_at": "…", "last_seen_at": "…"
  }]
}
```
`token_hash` (the agent's credential digest) is never present in the
response — it is stripped at the struct level, not filtered per-handler.

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

### `POST /api/v1/binding-tokens`

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
unaffected — they carry their own per-machine token by then. There is no
delete-computer endpoint yet to revoke one of those individually —
unenrolling a machine arrives with the cabinet.

**Response** `204`.

### `GET /api/v1/events`

`owner`/`admin` only. The 200 most recent account activity events, most
recent first.

**Response** `200`
```json
{"events": [{"id": "…", "account_id": "…", "room_id": null, "computer_id": "…", "type": "computer.enrolled", "payload": {"hostname": "FAKE-PC", "agent_version": "1.4.0"}, "created_at": "…"}]}
```

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

### `POST /agent/sync`

Agent-token auth. Returns the wire format the single-tenant server spoke —
**unchanged on purpose**, so moving the agent over was a URL and credential
change, not a protocol change.

**Request** (body capped at 64 KiB)
```json
{"runtime": {"uptime_s": 1234, "mode": "service", "agent_version": "3.0.0"}}
```
`runtime` is live telemetry, recorded against the computer's `runtime`
column regardless of the policy computed below. It travels in a body rather
than a query string so it is not written to proxy access logs and not
subject to URL length limits. Telemetry never fails a sync: no body, a body
over the cap, invalid JSON, a non-object, an object over 64 KiB, or anything
containing a NUL is silently replaced with `{}`. `GET` on this path is `405`.

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
