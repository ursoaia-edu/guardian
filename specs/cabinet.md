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
| `ui.js` | the shared components: `Card`, `Button`, `Field`, `Toggle`, `StateBadge`, `Banner`, `Confirm`, `Tally`, `PageHeader`, `Live`, `BrandMark`, `ErrorBoundary` |
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

### When a screen throws

`ErrorBoundary` (`ui.js`) is mounted twice, and the two catch different things.

The inner one wraps `Screen` inside the shell, so a screen that fails to draw leaves the
rail, the navigation and the page header standing, and the person can walk away from it.
Its `resetKey` is the route, so navigating elsewhere clears the error — a boundary that
stays broken after the person leaves the screen is a dead tab.

The outer one wraps the whole app at the root, with `standalone`, for a throw in the shell
itself — where there is no rail left to draw a fallback inside. Both say the same thing
first: the fault is in the cabinet, not on the customer's machines, because the agents go
on enforcing the policy they already hold whatever this page does.

React boundaries never catch what happens in an event handler or a fetch, and those do not
need one: `useAction` keeps a failed mutation's error next to the button that failed, and
`usePoll` hands a failed load to `ErrorBanner`.

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

```sh
cd server/webui-tests && node --test
```

No install, no `package.json`, no dependency: the three pure modules are imported
straight out of `server/webui/` and driven with Node's own test runner.
`.github/workflows/cabinet.yml` runs it on every push that touches the cabinet.

**The tests live in `server/webui-tests/`, not beside the code.** `server/webui/` is
embedded whole (`go:embed all:webui`) and every file in it is served, so a test file in
there would ship inside the binary and be downloadable from the cabinet. The workflow
fails the build if one appears.

What is covered:

- `format.js` — the online threshold and its boundary, what a machine is called, that
  locked outranks offline, that protection-off outranks the mode, every step of
  `relativeTime` including a clock that runs ahead, and that an unlabelled event falls
  through to its raw type instead of vanishing.
- `router.js` — every route parses and every route survives a round trip through its own
  `href`, ids with spaces and slashes included; an unknown tab lands on the room's default
  tab rather than a dead end; anything unrecognised is `notfound` rather than a guess.
- `api.js` — the account header appears only once an account is chosen, every request
  carries `same-origin` credentials, a `204` and an empty body are both `null`, the
  server's own error message survives to the screen, a non-JSON reply falls back to its
  status, a dropped connection is status `0` rather than a server error, and an abort
  stays an abort.

What is **not** covered: the screens and `ui.js`, because rendering them needs a DOM, and
a DOM needs a `package.json` and a dependency — a bigger decision than this suite. The
`ErrorBoundary` is verified by hand for the same reason (break an endpoint's shape in the
harness below and the fallback appears with the rail intact).

`server/webui_test.go` covers the Go side: the index is served at the root, deep links
fall back to it, the API prefixes are refused, and the vendored script tags are present.

For looking at the thing, a harness that serves `server/webui/` with fixture API
responses (no Postgres, no agent) is enough to open every screen; the cabinet talks to
nothing but its own origin.
