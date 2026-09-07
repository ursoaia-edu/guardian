# Mail and Account Lifecycle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Guardian can send email, so a person can confirm their address and recover a forgotten password instead of losing their entire fleet to a typo.

**Architecture:** One `server/internal/mail` package with a `Sender` interface and two implementations — SMTP (SendGrid, port 587, STARTTLS) and a logging one for development. Message rendering is pure and tested on its own; the transport is a thin shell around `net/smtp`. A new `email_tokens` digest table backs both verification and reset, with no RLS for the same reason `sessions` has none. Every send happens **after** its transaction commits, in a goroutine with its own deadline, and can never fail the request.

**Tech Stack:** Go 1.25, `net/smtp`, `net/mail`, `mime`, `go-chi/chi` v5, `jackc/pgx/v5`, `sqlc` (pgx/v5), `pressly/goose/v3`, PostgreSQL 16 with row-level security.

**Spec:** `specs/2026-09-09-cabinet-v1-design.md` — §2 *Registration, finished*, minus **Invitations**. This plan implements step 2 of that document's **Implementation order**; invitations are step 3 and build on what lands here.

## Global Constraints

- **Mail never fails a request.** A send that fails is logged and nothing else. The row was already committed; telling somebody their registration failed because SendGrid was slow would be a lie.
- **Mail is sent after the transaction commits**, never inside it. An SMTP round trip to a third party inside a database transaction holds a Postgres connection hostage for as long as the provider feels like taking, and the request timeout would abort work that was already written.
- **`forgot` answers `204` whether or not the address exists**, and spends the same argon2id work either way. The registration endpoint already treats account enumeration as a bug to avoid; a reset endpoint that leaks it undoes that.
- **Tokens are digests.** `newToken()`/`hashToken()` (`server/auth.go`) already do this: the plaintext exists in the email and nowhere else, exactly as session and agent tokens work.
- **A completed reset deletes every session of that user.** A password change from inside the cabinet deletes every session except the calling one. Both are the point of having server-side sessions at all.
- **Every new route under `/api/v1` gets classified in `server/authz_test.go`**, which walks the router and fails the build on an unclassified route.
- **`email_tokens` carries no RLS**, like `sessions`: the row has no `account_id` and is reached before any account scope exists. It therefore gets **no** row in `server/isolation_test.go` — that table is for account-scoped endpoints.
- **Server tests need a live Postgres.** `docker compose -f server/docker-compose.dev.yml up -d`, then `TEST_DATABASE_URL` and `TEST_APP_DATABASE_URL`.
- **Commit message format:** this repo's history uses Conventional Commits (`feat(server): …`). No Claude attribution, no session trailer.
- **Docs are updated in the same commit as the code:** `specs/api.md`, `specs/server.md`, `specs/cabinet.md` and `CLAUDE.md` where the change touches what they describe.

### Decisions this plan settles, and why

1. **Templates are Russian, per the spec** — while the cabinet's own UI is English, because it has no i18n yet (`specs/cabinet.md`). This is a real inconsistency and it is the spec's call, not this plan's: §2 says "plain text, in Russian". All copy lives in one file (`server/mailtemplates.go`) so it moves in one edit when i18n lands.
2. **Links are `{CABINET_ORIGIN}/#/verify/<token>`**, with the `#/`. The spec writes `{CABINET_ORIGIN}/invite/<token>`, but the cabinet routes on the hash (`specs/cabinet.md`) and a path-only link would land on the overview with the token dropped. The hash form works today with no cabinet change.
3. **TTLs:** a verification link lives **48 hours**, a reset link **1 hour**. The spec fixes 7 days for invitations only. A reset link is the more dangerous of the two — it is a bearer credential for an account — and an hour is long enough to walk to a laptop.
4. **A `resend` for verification exists** (`POST /api/v1/account/verify/resend`, session, rate-limited). Without it a customer whose mail was eaten by a spam filter can never verify, and verification gates handing out installers.

---

## File Structure

**Created:**

| File | Responsibility |
|---|---|
| `server/internal/mail/mail.go` | `Sender`, `Message`, `Render`, the header-injection guard |
| `server/internal/mail/smtp.go` | `ParseSMTPURL`, `SMTPSender` |
| `server/internal/mail/log.go` | `LogSender` — prints the message, never fails |
| `server/internal/mail/mail_test.go` | rendering, encoding, injection, URL parsing, the log sender |
| `server/mailer.go` | `mailerFromEnv`, `Server.sendMail` (after-commit, goroutine, deadline) |
| `server/mailtemplates.go` | every subject and body, in one file |
| `server/mailer_test.go` | config refusal, the recording sender used by handler tests |
| `server/db/migrations/00019_email_tokens.sql` | the table and its index |
| `server/db/queries/email_tokens.sql` | mint, consume, purge, and the user reads this needs |
| `server/handlers_password.go` | forgot, reset, change |
| `server/handlers_verify.go` | verify, resend |
| `server/verify_test.go` | the verification flow and the binding-token gate |
| `server/password_test.go` | forgot, reset, change, and session invalidation |
| `server/webui/screens/verify.js` | the cabinet's landing for a verification link |
| `server/webui/screens/password.js` | forgot, reset, and the change form in Settings |

**Modified:** `server/main.go` (config, `Server.mail`), `server/handlers_auth.go` (register sends), `server/handlers_agent.go` (the binding-token gate), `server/routes.go`, `server/authz_test.go`, `server/maintenance.go`, `server/webui/api.js`, `server/webui/router.js`, `server/webui/app.js`, `server/webui/screens/settings.js`, `server/webui/screens/signin.js`, `dist/server/server.env`, `specs/api.md`, `specs/server.md`, `specs/cabinet.md`, `CLAUDE.md`.

---

