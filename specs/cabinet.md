# Guardian Cabinet — the web UI

**Lives in:** `server/webui/`, served by `server/webui.go`
**Related:** `specs/api.md` (every endpoint it calls), `specs/server.md` (auth, tenancy),
`specs/2026-09-09-cabinet-v1-design.md` (the features it does not have yet)

The cabinet is the browser UI an account's owner, administrators and room guests use.
It is served by the same Go binary as the API, from the same origin, and it ships as
source: there is no build step, no bundler in CI, and no build output to regenerate.

---

## How it is built and served

### No build step, on purpose

React 18, ReactDOM and [htm](https://github.com/developit/htm) are files in
`server/webui/vendor/`. `index.html` loads them as plain `<script>` tags, and every
other file is a native ES module. `htm` binds JSX-like template literals to
`React.createElement` in the browser, so a template like

```js
html`<${Card} title="Rooms">${children}<//>`
```

needs no transform. What a developer edits is what ships; `go build` alone produces a
working cabinet.

The whole directory is embedded with `go:embed all:webui` (`server/webui.go`). Every
file's bytes, content type and a strong ETag over the bytes are computed once at
startup, so a request costs a map lookup and a write.

### Routing and the API boundary

`cabinetHandler` hangs on chi's `NotFound`. It refuses `/api/`, `/agent/` and `/health`
itself — a mistyped API path gets the API's JSON 404, not a page — and serves
`index.html` for every other unmatched path, so a deep link or a refresh on `/settings`
lands on the cabinet.

Inside the page, routing is on the **hash** (`#/rooms/<id>/rules`), which never reaches
the server at all. No proxy in front of Guardian has to be taught about the cabinet's
routes.

### Headers

```
Content-Security-Policy: default-src 'self'; base-uri 'none'; form-action 'self';
                         frame-ancestors 'none'; object-src 'none'
X-Content-Type-Options: nosniff
Referrer-Policy: same-origin
Cache-Control: no-cache          (revalidated by ETag; a 304 carries no body)
```

There is no `unsafe-inline` and no `unsafe-eval`, which is what vendoring bought: the
page loads nothing from any third party, including its fonts. Being same-origin with
the API is also deliberate — the session cookie is `HttpOnly` and `SameSite=Lax`, so a
cabinet on another origin would need CORS credentials and a token within JavaScript's
reach. Here it needs neither.

### Files

| File | Responsibility |
|---|---|
| `index.html` | the mount point, the vendored script tags, the font preloads |
| `app.js` | session, account scope, hash routing, and the shell (rail + page header) |
| `api.js` | the whole HTTP surface; `fetch` is injectable so it is testable without a browser |
| `hooks.js` | `usePoll` (refresh, backoff, hidden-tab pause), `useAction`, `useFreshness` |
| `ui.js` | the shared components: `Card`, `Button`, `Field`, `Toggle`, `StateBadge`, `Banner`, `Confirm`, `Tally`, `PageHeader`, `Live`, `BrandMark` |
| `format.js` | the presentation rules worth testing: `isOnline`, `computerName`, `computerState`, `roomPolicy`, `roomMode`, `relativeTime`, `lastSeenMillis`, event labels |
| `router.js` | `parseRoute`, `href`, `navigate`, `ROUTES`, `ROOM_TABS` — pure, no DOM |
| `app.css` | the entire stylesheet, tokens included |
| `favicon.svg` | the brand mark as a path, sharp at 16px |
| `screens/*.js` | one file per screen |
| `vendor/` | React, ReactDOM, htm, and the three subsetted fonts (`vendor/fonts/LICENSE.txt`) |

### Screens

`signin` (doubles as registration) · `welcome` (the first-computer wizard) · `overview` ·
`rooms` · `room` with four tabs (computers, rules, power, members) · `computers` (the
pool) · `computer` (one machine's passport) · `install` · `activity` · `settings`.

### Freshness

`usePoll` refreshes the active screen every 7 seconds, pauses entirely in a hidden tab,
backs off exponentially to a one-minute ceiling on consecutive failures, and re-throws a
401 to the caller, whose job is to send the person back to the sign-in screen.

Every successful load publishes to a module-level store in `hooks.js`; `useFreshness`
reads it and `PageHeader` renders it as the `live` indicator. A screen's own polling is
therefore what the indicator reports — it never claims freshness nobody fetched.

---

## The design system

The cabinet looks like `docs/index.html`, the marketing site, because somebody who
clicks "Sign in" there must not feel they landed in a different product. Dark only:
`<meta name="color-scheme" content="dark">`, no light theme and no toggle.

### Colour

Tokens are defined once on `:root` in `app.css` and taken from the landing page.

| Token | Value | Means |
|---|---|---|
| `--void` | `#07090A` | the page |
| `--panel` | `#0E1412` | a panel's surface |
| `--raised` | `#121A17` | hover, a selected row |
| `--sunken` | `#090D0B` | table headers, inputs, machine cells |
| `--line` / `--line-soft` | `#1E2A24` / `#16201B` | borders, separators |
| `--guardian` / `--signal` | `#4ADE80` / `#6EFF9B` | enforcing, online, primary action / its hover |
| `--amber` | `#F5C451` | locked, needs attention |
| `--danger` | `#FF5C6C` | destructive, failed |
| `--ink` / `--muted` / `--dim` | `#E6F0EA` / `#8FA39A` / `#5E6F67` | the three levels of text |

**Colour carries state and nothing else.** Everything that is not a state is greyscale,
so a coloured pixel always means something. A room's name is a name, not a state, and
takes the accent only on hover.

### Type

Three faces, one rule: **type carries who is speaking.**

- `--display` **Space Grotesk** — headings only.
- `--ui` **Inter** — the words a person reads: labels, prose, buttons.
- `--data` **JetBrains Mono** — what a machine said: hostnames, machine GUIDs, agent
  versions, sync times, installer tokens, executable names, event payloads. The `.mono`
  class carries it, and nothing else wears it.

The faces are vendored as Latin and Latin-Extended `woff2` subsets in `vendor/fonts/`
(SIL OFL — see the `LICENSE.txt` there for what they are and how to refresh them). The
CSP forbids fetching them from Google, and an isolated school network could not reach it
anyway. `index.html` preloads the two faces the first paint needs.

### Layout

A fixed **rail** of 216px carries the five screens and, under them, the rooms with a live
protection dot each — so "is that room still protected" is answerable from anywhere. The
account and sign-out sit at its foot. Below 860px the rail becomes a horizontal
scrolling bar above the content.

Each screen opens with a `PageHeader`: the title, an optional line of context, the
actions belonging to the whole screen, and the `live` indicator. The shell writes that
header for the screens it can name (`PAGE_TITLES` in `app.js`); the room, the computer
and the wizard write their own, because they are named after something the server knows.

### Density

**A list of machines is a table.** The computer pool and the room's computer list have
aligned, sortable columns, because the screen exists to be scanned — forty rows, looking
for the one that is wrong. A sentence of facts joined by middle dots can only be read one
row at a time, and that is what these replaced.

A handful of people, rooms or accounts is a `.rows` list instead: one line each, actions
on the right, rules between them rather than a card around each.

### The rack

The overview's hero is the fleet as the customer owns it: one panel per room, one cell
per machine, the cell's left edge coloured by state. Cells sort by how much they need
attention — locked, then offline, then online — so the eye lands on the wrong colour
first. It reads the same with six machines or sixty. Machines in no room get their own
panel at the end.

### Motion

The `live` dot beats once when a refresh actually landed. That is the only thing in the
cabinet that moves by itself; everything else moves in answer to a click.
`prefers-reduced-motion: reduce` removes the beat and flattens every transition.

### Writing

Errors show the server's own `{"error": "..."}` wherever there is one — paraphrasing it
in the client loses the detail the server bothered to give. Empty states name what is
missing and put the action that fixes it within reach. Buttons say what happens
("Save the name", not "Submit").

---

## Testing

There is no automated test for the cabinet's JavaScript yet, and CI does not build it —
there is nothing to build. `server/webui_test.go` covers the Go side: the index is served
at the root, deep links fall back to it, the API prefixes are refused, and the vendored
script tags are present.

`api.js`, `router.js` and `format.js` are written to be testable under `node --test`
without a browser or a server, which is the obvious next step.

For looking at the thing, a harness that serves `server/webui/` with fixture API
responses (no Postgres, no agent) is enough to open every screen; the cabinet talks to
nothing but its own origin.
