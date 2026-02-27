# ProcSentinel Architecture Review — 2026-06-12

Companion to `specs/audit-2026-06-12.md` (security/UX findings). This document assesses the
architecture itself and sets the direction and roadmap. Deployment context: **internet-exposed
server, personal use** — one admin, a handful of mostly-Windows machines. That context drives
every recommendation: security and operability outrank scale and features.

## Current architecture

```
┌──────────────┐   GET /client/sync (poll 20-30s)   ┌──────────────────┐
│ Agent (Go)   │ ──────────────────────────────────▶ │ Server (Go)      │
│ Windows svc  │   shared TOKEN, ?identity=N         │ chi + SQLite     │
│ kill loop 1s │ ◀────────────────────────────────── │ in-mem caches    │
└──────────────┘   apps + mode + client entries      └──────────────────┘
                                                         ▲
                   ADMIN_TOKEN, REST CRUD                │
┌──────────────┐   3 independent 10s polling loops      │
│ Mobile       │ ────────────────────────────────────────┘
│ (Flutter)    │
└──────────────┘
```

### What is sound — keep it

- **Single Go binary + SQLite (`modernc.org/sqlite`, no CGO).** Zero-dependency operations,
  one-file backup, cross-compiles anywhere. At this scale a database server would be pure
  liability.
- **Polling transport.** NAT/firewall friendly, survives flaky home internet, trivially
  debuggable with curl. 20–30s command lag is acceptable for parental control.
- **chi + middleware layering.** The two-tier auth grouping in `routes.go` is clean and easy
  to extend.
- **Multi-file package layout** (`routes/handlers/db/models/middleware/helpers`) — each file
  has one job; the post-refactor server is easy to navigate.
- **Single Flutter codebase** for the management UI.

### Structural weaknesses (ordered by how much they constrain the future)

1. **Identity is the root flaw.** All agents share one `TOKEN` and *choose their own* integer
   identity. Consequences: any agent (or anyone holding the leaked shared token) can
   impersonate any computer, there is no per-machine revocation, and the registry grows
   unbounded from spoofed identities (audit SEC-09/SEC-12). Every future feature — schedules,
   activity logs, per-computer policy — produces garbage data until the server can trust who
   is talking. **This is the architectural cornerstone to fix first.**
2. **The server is a state store with no memory.** Nothing records what happened: kills,
   mode flips, blocks, admin actions. No audit trail, no activity feed possible, no way to
   answer "why was this process killed yesterday".
3. **Policy is global-only.** One mode and one application list for the whole fleet. A rule
   meant for a kid's machine applies to every machine. The schema has no place to hang
   per-computer or time-based policy.
4. **Hand-rolled cache layer.** Four in-memory caches (`appsCache`, `enabledCache`,
   `modeCache`, `clientCache`) are each manually invalidated at every write site. Correct
   today, but every new table multiplies invalidation paths. At this request rate SQLite
   alone (with WAL, per hardening Task 7) would serve reads in microseconds — the caches are
   an optimization the system does not need and a bug surface it does.
5. **Stringly-typed command channel.** The `client` table is generic name/bool pairs with
   magic names (`power`) and special-cased agent commands (`force_poweroff`,
   `force_shutdown`). Opaque to read, unsafe to extend, no parameters, no acknowledgement.
6. **The phone holds the keys to everything.** The mobile app authenticates with the full
   `ADMIN_TOKEN`. Acceptable for a single admin, but worth noting: phone compromise equals
   total fleet control, and there is no scoped/read-only tier.
7. **Naming drift.** Guardian vs ProcSentinel across binaries, paths, and docs (audit
   IMP-04) — operational friction more than architecture, but worth scheduling.

## Direction (decided 2026-06-12): evolve the REST core

Three options were considered:

| Option | Verdict |
|---|---|
| **A. Evolve the REST core** — keep chi + SQLite + polling; fix identity, data model, observability | **Chosen.** Every step ships working software; nothing about the transport is actually broken at this scale. |
| B. Real-time first — SSE/WebSocket as primary transport | Deferred to M5. Adds reconnect logic and agent rewrite before any user-visible win; polling lag is tolerable. |
| C. Broker re-platform (MQTT/NATS, gRPC) | Rejected. An extra always-on process to operate and a three-component rewrite, for one family. |

### Target architecture

- **Identity layer:** per-agent tokens issued through one-time-code enrollment; the server
  derives identity from the token, never from request parameters. Designed in
  `docs/superpowers/specs/2026-06-12-agent-enrollment-design.md`.
- **Registry:** `computers` becomes the source of truth — server-assigned identity, friendly
  name, agent-reported hostname/OS/version, last-seen.
- **Event log (M4):** append-only `events` table (`ts, computer, type, detail`); agents batch
  kill reports into their next sync; the server logs admin actions; old events pruned by age.
  Powers an activity feed and notifications.
- **Policy model (M3/M6):** introduce a `policies` entity (mode + application set + schedule)
  with a computer→policy assignment; today's global settings become the default policy row,
  so the change is backward compatible.
- **Command channel (with M4):** replace `client` name/bool entries with explicit commands
  (`shutdown`, later `lock`, `message`) carrying an issued-at timestamp and acknowledged via
  the event log — closing today's "power-off ignored / replayed" ambiguities.
- **Cache simplification (opportunistic):** as handlers are touched, drop the in-memory
  caches in favor of direct WAL-mode SQLite reads; keep only the token-hash→identity lookup
  cache, which sits on the hot auth path.
- **Real-time layer (M5, optional):** a thin `GET /client/stream` SSE endpoint that pushes
  "sync now" pokes; agents keep polling as fallback. Presence becomes connection-based
  instead of timestamp arithmetic. Only build it if 20–30s lag starts to hurt in practice.
- **Mobile:** one shared polling repository that screens subscribe to (audit UX-01), built
  after the API surface stabilizes (post-M2) so it is written once.

## Roadmap

| Milestone | Content | Status / precondition |
|---|---|---|
| **M1 Security hardening** | 28 tasks: kill fallback tokens, default-deny, TLS, rate/body limits, non-root, agent kill-safety, mobile UX fixes | **Planned** — `docs/superpowers/plans/2026-06-12-security-hardening.md`. Prerequisite for everything below. |
| **M2 Agent enrollment & identity** | One-time-code enrollment, per-agent tokens, revocation, named computers with hostname/OS metadata | **Designed** — next to plan. Depends on M1 (test harness, rate limiting). |
| **M3 Schedules** | Time-based policy: school-hours blacklist, bedtime block-all, per-day rules | After M2 (schedules per computer need trusted identity). |
| **M4 Activity log + notifications** | Kill/online/offline/admin events; activity screen; optional push (e.g. ntfy) | After M2 (events from spoofable agents are noise). Includes the command-channel rework. |
| **M5 Real-time (SSE)** | Instant command delivery, connection-based presence | Optional; only if polling lag proves painful. |
| **M6 Per-computer policies** | Different lists/modes per machine, policy groups | Builds on M2 + M3 schema. |
| Parking lot | Web dashboard (Flutter web — CORS already permits it), per-process usage statistics, Guardian↔ProcSentinel naming unification (IMP-04), scoped/read-only admin tier | Unscheduled. |

**Why this order:** M1 closes the holes that make an internet-exposed server dangerous today.
M2 is the keystone — it is the only milestone other milestones *depend on for correctness*,
not just convenience. M3 and M4 are the visible feature payoffs and can swap order by
appetite; M3 is scheduled first because it changes daily life (automatic enforcement) while
M4 changes awareness. M5 stays optional on purpose: it is the only milestone that adds
operational complexity without adding capability.
