# Agent Enrollment & Identity — Design

**Date:** 2026-06-12
**Status:** Approved (design review in session)
**Depends on:** security-hardening plan executed first
(`dev-docs/plans/2026-06-12-security-hardening.md` — test harness, token validation,
rate limiting, default-deny).
**Context:** internet-exposed personal server. See `dev-docs/architecture-2026-06-12.md` (M2).

## Problem

All agents share one `TOKEN` and self-declare an integer identity (`/client/sync?identity=N`).
Anyone holding the shared token can impersonate any computer; there is no per-machine
revocation; the computers list fills with spoofable, unnamed entries ("Computer #3"). Audit
findings SEC-09, SEC-12, UX-07.

## Solution overview

A computer joins the fleet through a **one-time enrollment code**:

1. Parent taps **Add computer** in the app → server mints a short-lived code → app displays it.
2. During agent install, the installer prompts for the code and writes it to the agent `.env`.
3. On first start the agent calls `POST /enroll` with the code → receives a **server-assigned
   identity** and a **permanent per-agent token** → persists them locally → the code is consumed.
4. All subsequent `/client/sync` calls authenticate with the per-agent token; the server
   derives identity from the token. The shared client `TOKEN` tier is **removed**.
5. Revocation = deleting the computer in the app; its token dies with the row.

## Data model (server, SQLite)

`computers` becomes the agent registry. Existing columns are kept; `identity` remains
`INTEGER PRIMARY KEY` but is now **server-assigned** (SQLite rowid auto-assignment —
`AUTOINCREMENT` not required). Migration is `ALTER TABLE ... ADD COLUMN`, guarded by a
`PRAGMA table_info` check, consistent with existing migration style:

```sql
ALTER TABLE computers ADD COLUMN name          TEXT NOT NULL DEFAULT '';
ALTER TABLE computers ADD COLUMN hostname      TEXT NOT NULL DEFAULT '';
ALTER TABLE computers ADD COLUMN os            TEXT NOT NULL DEFAULT '';
ALTER TABLE computers ADD COLUMN agent_version TEXT NOT NULL DEFAULT '';
ALTER TABLE computers ADD COLUMN token_hash    TEXT NOT NULL DEFAULT '';
ALTER TABLE computers ADD COLUMN enrolled_at   DATETIME;

CREATE UNIQUE INDEX IF NOT EXISTS idx_computers_token_hash
  ON computers(token_hash) WHERE token_hash != '';

CREATE TABLE IF NOT EXISTS enroll_codes (
  code       TEXT PRIMARY KEY,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at DATETIME NOT NULL,
  used       BOOLEAN  NOT NULL DEFAULT 0
);
```

- **Per-agent token:** 32 random bytes, hex-encoded (64 chars). The server stores only its
  SHA-256 hex digest in `token_hash`; the agent keeps the plaintext. A leaked database does
  not leak usable agent credentials.
- **Enrollment code:** 8 characters from `ABCDEFGHJKMNPQRSTVWXYZ23456789` (no `0/O/1/I/L`
  ambiguity; ~39 bits). TTL **15 minutes**, single-use. Persisted in the DB so a server
  restart mid-install doesn't strand the user. Expired/used codes are deleted opportunistically
  when new codes are minted.
- `datetime` (last seen) keeps its existing name and update-on-sync behavior.

## API

### `POST /enroll` — unauthenticated, code-gated

Rate-limited to **10 req/min/IP** (stricter than the global limit). Request:

```json
{"code": "ABCD2345", "hostname": "DESKTOP-KIDS", "os": "windows", "agent_version": "2.1.0"}
```

- Code is consumed atomically:
  `UPDATE enroll_codes SET used=1 WHERE code=? AND used=0 AND expires_at > CURRENT_TIMESTAMP`
  — exactly one affected row proceeds; anything else returns the error below.
- On success the server inserts a `computers` row (identity auto-assigned, `blocked = 0` —
  enrollment expresses parent intent, so the machine starts unblocked; non-enrolled agents
  can no longer sync at all, which supersedes the default-deny rule from hardening Task 3)
  and responds `201`:

```json
{"identity": 7, "token": "<64 hex chars>"}
```

- Unknown, used, or expired code → `403 {"error": "invalid or expired enrollment code"}` —
  one message for all three cases, no oracle.
- Code matching is case-insensitive (normalize to upper case server-side).

### `POST /manage/enroll_codes` — admin

Mints a code, responds `201 {"code": "ABCD2345", "expires_at": "<RFC3339>"}`. Multiple codes
may be live at once (enrolling several machines in one sitting).

### `GET /client/sync` — changed

- Auth: `Authorization: Bearer <per-agent token>`. Middleware hashes the token, looks up
  `token_hash` → identity (in-memory hash→identity cache on the hot path, invalidated on
  enroll/revoke), and puts the identity in the request context. **The `?identity=` query
  parameter and the shared client `TOKEN` tier are removed.**
- Query parameters `hostname`, `os`, `version` (URL-encoded) refresh the registry row on
  every sync, along with last-seen. Response body is unchanged.

### `PUT /manage/computers` — extended

Gains optional `name` (≤ 64 chars, trimmed). Partial-update semantics via pointer fields,
same pattern as hardening Task 8: `{"identity": 7, "name": "Kids PC"}` renames without
touching `blocked`, and vice versa.

### `DELETE /manage/computers` — new (admin)

Body `{"identity": 7}`. Deletes the row → the agent's token hash no longer resolves → its
next sync gets `401`. This **is** revocation. (`/manage/computers/reset` keeps meaning
"unblock all" — unchanged.)

## Agent behavior

- **Credentials file:** `agent_credentials.json` next to the existing state files, mode 0600:
  `{"identity": 7, "token": "<hex>"}`.
- **Startup:** if the credentials file exists → normal sync loop. Otherwise read
  `ENROLL_CODE` from `.env`; if present, call `/enroll`, persist credentials, log success.
  If absent → log "not enrolled — set ENROLL_CODE" and retry every 60s (service must not
  crash-loop).
- **Enrollment failure** (403): log the server's message and retry every 60s — the parent may
  mint a fresh code and update `.env` without reinstalling.
- **After successful enrollment** the code in `.env` is dead weight (single-use); the agent
  does not need to remove it. The installer template documents this.
- **On 401 during sync (revoked):** the agent keeps enforcing its **last-synced** rules
  (fail-secure — deleting a computer must not free it) and logs every failure. Re-enrollment
  requires deleting `agent_credentials.json` and setting a fresh `ENROLL_CODE`.
- **Installer** (`Install.bat` / PowerShell): prompts "Enrollment code (from the Guardian
  app):" and writes `ENROLL_CODE=<value>` into the agent `.env`. `TOKEN=` disappears from the
  template.
- Transport note: enrollment sends the token in the response body — the server docs and `.env`
  template state that an internet-exposed server **must** run with TLS (hardening Task 6);
  plain HTTP remains possible for LAN-only testing.

## Mobile app

- **Computers screen — "Add computer"** action (app bar): calls `POST /manage/enroll_codes`,
  then shows a dialog with the code in large monospace type, a countdown to `expires_at`
  (parsed with `time_utils.parseServerTimestamp`), a copy button, and the instruction
  "Enter this code when installing the agent."
- **Computer cards:** title shows `name`, falling back to `hostname`, falling back to
  `Computer #<identity>`; subtitle keeps online/last-seen and gains an OS + agent-version
  line. Rename via an edit icon → text dialog → `PUT` with `name` only.
- **Delete/revoke:** per-card menu action with a confirmation dialog ("Remove and revoke
  this computer? Its agent will stop syncing."), calling the new `DELETE`.

## Migration — hard cutover

Justified by the deployment (a handful of machines, one admin):

- Server migration adds columns/tables; existing rows survive with empty `token_hash` — they
  can never authenticate again and just show as offline legacy entries until deleted in the app.
- Existing agents start receiving `401` after the server upgrade; each is re-enrolled by
  re-running the installer with a fresh code (one minute per machine).
- No shared-token compatibility window. CHANGELOG documents the procedure:
  upgrade server → mint code per machine → re-run agent installer.

## Error handling summary

| Case | Behavior |
|---|---|
| Enroll with bad/expired/used code | `403`, generic message; agent logs and retries every 60s |
| Enroll endpoint abuse | 10/min/IP rate limit; codes are 39-bit, 15-min TTL, single-use |
| Sync with unknown/revoked token | `401`; agent keeps last rules (fail-secure), logs |
| Rename with name > 64 chars | `400` with message |
| Delete unknown identity | `404` |
| Server restart between code mint and agent enroll | Code persisted in DB — still works |

## Testing

- **Server (httptest, on the M1 harness):** enroll happy path; expired code; second use of a
  code; case-insensitive code; sync derives identity from token (no query param); revoked
  token → 401; rename partial update leaves `blocked` intact; metadata refresh on sync;
  enroll rate limit.
- **Agent (unit):** credentials persist/load round-trip; enrollment response handling;
  not-enrolled retry path does not exit.
- **Mobile:** `flutter analyze`; unit test for countdown formatting; manual checklist for the
  add/rename/revoke flows (no widget-test infra, consistent with existing plans).

## Out of scope

- Automatic migration of legacy agents (hard cutover instead).
- SSE/real-time presence (roadmap M5).
- Scoped or per-user admin tokens (parking lot).
- Agent-initiated re-enrollment UX beyond "delete credentials + new code".
