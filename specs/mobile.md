# Mobile App Architecture Specification

## Overview

Guardian for Android (`mobile/`, Flutter) is a cabinet in your pocket: it
signs in as a person, acts in one account at a time, and manages that
account's rooms, rules and computers through the same `/api/v1` the web
cabinet uses.

It replaced a client that held a shared `ADMIN_TOKEN` typed into a settings
field — one secret for the whole deployment, no identity behind a change, and
no way to revoke one phone without revoking every phone and every other
client with it.

## File Structure

| File | Purpose |
|---|---|
| `lib/main.dart` | app shell; `_Root` shows the login screen or the tabs depending on whether a session exists |
| `lib/services/settings_service.dart` | the whole API client: stored state, session auth, every endpoint, `ApiException` |
| `lib/screens/login_screen.dart` | sign in, or register an account |
| `lib/screens/home_screen.dart` | **Rules** — one room's applications: add, switch off, delete |
| `lib/screens/system_screen.dart` | **Room** — protection, mode, power, rename, delete, create |
| `lib/screens/computers_screen.dart` | **Computers** — the account's pool: room assignment, lock, rename, unenrol, and minting an installer token |
| `lib/screens/settings_screen.dart` | server address, account switcher, sign out, version |
| `lib/widgets/room_selector.dart` | the room dropdown shared by the two room-scoped screens |
| `test/` | `flutter test` — API contract tests against a mock HTTP client, plus a login-screen widget test |

## Authentication

`POST /api/v1/auth/login` with `"client": "mobile"`. That field is what asks
for the session token in the response body: a browser deliberately receives it
only as an `HttpOnly` cookie it cannot read, and this app has no cookie jar.

The token is stored in `shared_preferences` and sent as
`Authorization: Bearer <token>`. Signing out calls `/api/v1/auth/logout`,
which deletes the session server-side, and clears local state either way — a
sign-out that failed because the session was already gone is still a
sign-out.

`SettingsService` is a `ChangeNotifier` and the single source of truth for
"is there a session". `_Root` in `main.dart` listens to it, so signing in or
out anywhere swaps the screen without anybody navigating.

## Accounts

A user owns their own account and may also be an admin or a room guest of
somebody else's, so requests carry `X-Guardian-Account` naming which one they
act in. It selects among memberships the session has already proved — the
server rejects anything else with a 404 — see `specs/api.md`.

Signing in lands in the first account `/api/v1/me` returns, which is ordered
strongest-role-first: a user's own account when they have one, the account
shared with them when they do not. Settings offers a switcher when there is
more than one. Switching accounts clears the selected room, because rooms
belong to an account.

## Rooms

Policy is per-room, so the two room-scoped screens are always about the room
named in the app bar, kept in `SettingsService.roomId`. When the stored room
no longer exists the app falls back to the first one rather than showing
nothing (`pickRoom`).

## Error Handling

Every call throws `ApiException` carrying the server's own message and status.
The previous client caught everything and returned `false` or an empty list,
which made an outage, an expired session and "there is nothing here"
indistinguishable — the screen said the account was empty either way. Now a
failure is shown as what it was, and `isUnauthorized`/`isForbidden` let a
caller tell "sign in again" from "you are not an admin here".

## Testing

`flutter test` needs no Android SDK and no server:

- API contract tests drive `SettingsService` against `MockClient`
  (`package:http/testing.dart`), asserting the things a screen cannot see —
  that login asks for `"client": "mobile"`, that requests carry the Bearer
  token and the account header, that a server error message survives to the
  UI, that a transport failure is an exception rather than an empty list, and
  that switching accounts clears the room.
- A widget test renders the login screen.

`SettingsService.clientFactory` and `resetForTest`/`signInForTest` are
`@visibleForTesting` seams that exist for these.

## Build

`mobile/build.sh` → `flutter build apk --release` → `../dist/guardian.apk`.
Requires the Android SDK; `flutter analyze` and `flutter test` do not.
