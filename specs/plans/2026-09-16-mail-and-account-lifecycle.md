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

## Task 1: The mail package

Pure Go, no server, no database, no network. It is the piece with the most fiddly detail — MIME, encodings, CRLF — and the least infrastructure, so it is worth having alone and green.

**Files:**
- Create: `server/internal/mail/mail.go`, `server/internal/mail/smtp.go`, `server/internal/mail/log.go`, `server/internal/mail/mail_test.go`

**Interfaces:**
- Consumes: nothing from this repo.
- Produces:
  - `type Message struct { To, Subject, Body string }`
  - `type Sender interface { Send(ctx context.Context, m Message) error }`
  - `func Render(from *netmail.Address, m Message, now time.Time) ([]byte, error)`
  - `func ParseSMTPURL(raw string) (SMTPConfig, error)` with `SMTPConfig{Addr, Host, Username, Password string}`
  - `func NewSMTPSender(cfg SMTPConfig, from *netmail.Address) *SMTPSender`
  - `func NewLogSender(w io.Writer, from *netmail.Address) *LogSender`

- [ ] **Step 1: Write the failing tests**

Create `server/internal/mail/mail_test.go`:

```go
package mail

import (
	"bytes"
	"context"
	"encoding/base64"
	"mime"
	netmail "net/mail"
	"strings"
	"testing"
	"time"
)

func testFrom(t *testing.T) *netmail.Address {
	t.Helper()
	a, err := netmail.ParseAddress("Guardian <noreply@guardian.example>")
	if err != nil {
		t.Fatalf("parse from: %v", err)
	}
	return a
}

var testTime = time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)

// A mail server reads headers as CRLF-delimited. A lone \n is the classic
// "works against my local Postfix, silently mangled by the provider" bug.
func TestRenderUsesCRLFAndSeparatesHeadersFromBody(t *testing.T) {
	raw, err := Render(testFrom(t), Message{To: "a@b.example", Subject: "Hi", Body: "Hello"}, testTime)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	text := string(raw)
	if strings.Contains(strings.ReplaceAll(text, "\r\n", ""), "\n") {
		t.Fatal("a bare newline survived into the message")
	}
	if !strings.Contains(text, "\r\n\r\n") {
		t.Fatal("no blank line between headers and body")
	}
	for _, want := range []string{
		"From: \"Guardian\" <noreply@guardian.example>",
		"To: a@b.example",
		"MIME-Version: 1.0",
		`Content-Type: text/plain; charset="utf-8"`,
		"Content-Transfer-Encoding: base64",
		"Date: Wed, 16 Sep 2026 12:00:00 +0000",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing header %q in:\n%s", want, text)
		}
	}
}

// The templates are Russian. An unencoded Cyrillic subject is either mangled
// or rejected outright, depending on the provider.
func TestRenderEncodesANonASCIISubject(t *testing.T) {
	subject := "Подтвердите адрес"
	raw, err := Render(testFrom(t), Message{To: "a@b.example", Subject: subject, Body: "x"}, testTime)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	line := headerOf(t, raw, "Subject")
	if strings.Contains(line, "Подтвердите") {
		t.Fatalf("the subject went out as raw UTF-8: %q", line)
	}
	got, err := new(mime.WordDecoder).DecodeHeader(line)
	if err != nil {
		t.Fatalf("decode subject: %v", err)
	}
	if got != subject {
		t.Fatalf("subject decoded to %q, want %q", got, subject)
	}
}

func TestRenderBodyRoundTripsThroughBase64(t *testing.T) {
	body := "Здравствуйте!\nСсылка: https://guardian.example/#/verify/abc\n"
	raw, err := Render(testFrom(t), Message{To: "a@b.example", Subject: "x", Body: body}, testTime)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	_, encoded, ok := strings.Cut(string(raw), "\r\n\r\n")
	if !ok {
		t.Fatal("no body")
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(encoded, "\r\n", ""))
	if err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if string(decoded) != body {
		t.Fatalf("body round-tripped to %q", decoded)
	}
	// Long base64 on one line is legal but some relays baulk past 998 octets.
	for _, l := range strings.Split(encoded, "\r\n") {
		if len(l) > 76 {
			t.Fatalf("a body line is %d characters, want 76 or fewer", len(l))
		}
	}
}

// Header injection: an address or subject carrying CRLF would let a caller
// append headers of their own — a Bcc, say. Every value here reaches us from
// a user-supplied email address at some point.
func TestRenderRefusesHeaderInjection(t *testing.T) {
	cases := []Message{
		{To: "a@b.example\r\nBcc: evil@example.com", Subject: "x", Body: "y"},
		{To: "a@b.example\nBcc: evil@example.com", Subject: "x", Body: "y"},
		{To: "a@b.example", Subject: "x\r\nBcc: evil@example.com", Body: "y"},
	}
	for _, m := range cases {
		if _, err := Render(testFrom(t), m, testTime); err == nil {
			t.Fatalf("rendered a message with an injected header: %+v", m)
		}
	}
}

func TestParseSMTPURLReadsSendGridsShape(t *testing.T) {
	cfg, err := ParseSMTPURL("smtp://apikey:SG.abc123@smtp.sendgrid.net:587")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Addr != "smtp.sendgrid.net:587" {
		t.Errorf("addr %q", cfg.Addr)
	}
	if cfg.Host != "smtp.sendgrid.net" {
		t.Errorf("host %q", cfg.Host)
	}
	// SendGrid's username is the literal string "apikey". Getting this wrong
	// costs an hour of "authentication failed".
	if cfg.Username != "apikey" || cfg.Password != "SG.abc123" {
		t.Errorf("credentials %q / %q", cfg.Username, cfg.Password)
	}
}

func TestParseSMTPURLRejectsWhatCannotBeDialled(t *testing.T) {
	for _, raw := range []string{
		"",
		"smtp.sendgrid.net:587",                  // no scheme
		"https://smtp.sendgrid.net:587",          // not smtp
		"smtp://apikey:k@smtp.sendgrid.net",      // no port
		"smtp://smtp.sendgrid.net:587",           // no credentials
		"smtp://apikey@smtp.sendgrid.net:587",    // no password
	} {
		if _, err := ParseSMTPURL(raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
}

// The development transport must be incapable of failing: it is what a
// developer runs with, and a mailer that errors there turns every handler's
// error path into the one they exercise.
func TestLogSenderWritesAndNeverFails(t *testing.T) {
	var buf bytes.Buffer
	s := NewLogSender(&buf, testFrom(t))
	if err := s.Send(context.Background(), Message{To: "a@b.example", Subject: "Тема", Body: "Тело"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"a@b.example", "Тема", "Тело"} {
		if !strings.Contains(out, want) {
			t.Errorf("log output missing %q:\n%s", want, out)
		}
	}
}

func headerOf(t *testing.T, raw []byte, name string) string {
	t.Helper()
	msg, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("read message: %v", err)
	}
	return msg.Header.Get(name)
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd server && go test ./internal/mail/...`

Expected: FAIL to compile — `undefined: Render`, `undefined: Message`, `undefined: ParseSMTPURL`, `undefined: NewLogSender`.

- [ ] **Step 3: Write the message and its rendering**

Create `server/internal/mail/mail.go`:

```go
// Package mail sends the two or three messages this product needs: confirm
// your address, reset your password, and later an invitation.
//
// It is deliberately small and deliberately not a vendor SDK. SendGrid is
// spoken to over SMTP, so there are no vendor types in the handler layer and
// the day the provider changes it is a URL in a .env rather than a package to
// rewrite. Their Web API buys per-message tracking and templates, neither of
// which this product wants — a password reset that is tracked is a password
// reset with a third party's pixel in it.
package mail

import (
	"context"
	"encoding/base64"
	"fmt"
	"mime"
	netmail "net/mail"
	"strings"
	"time"
)

// Message is one email, as a caller thinks of it: who, what about, what it
// says. Everything MIME is Render's business.
type Message struct {
	To      string
	Subject string
	Body    string
}

// Sender is the whole interface. Two implementations: SMTP and a log.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// maxBodyLineLen is the base64 wrap width. RFC 5322 allows 998 octets per
// line, but 76 is what every encoder emits and what every relay is happiest
// with.
const maxBodyLineLen = 76

// Render builds the RFC 5322 message. The interesting parts are all failure
// modes somebody else has already been bitten by: CRLF line endings, an
// RFC 2047 subject because the templates are Russian, base64 for the body so
// UTF-8 survives a relay that thinks in 7 bits, and a refusal to render at all
// when a value carries a line break.
func Render(from *netmail.Address, m Message, now time.Time) ([]byte, error) {
	if from == nil {
		return nil, fmt.Errorf("mail: no From address")
	}
	to, err := netmail.ParseAddress(m.To)
	if err != nil {
		return nil, fmt.Errorf("mail: invalid recipient %q: %w", m.To, err)
	}
	// ParseAddress accepts a display name, and a display name is a place to
	// hide a line break. Check the raw values, not the parsed ones.
	for name, v := range map[string]string{"To": m.To, "Subject": m.Subject} {
		if strings.ContainsAny(v, "\r\n") {
			return nil, fmt.Errorf("mail: %s header contains a line break", name)
		}
	}

	var b strings.Builder
	writeHeader(&b, "From", from.String())
	writeHeader(&b, "To", to.Address)
	writeHeader(&b, "Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	writeHeader(&b, "Date", now.Format(time.RFC1123Z))
	writeHeader(&b, "MIME-Version", "1.0")
	writeHeader(&b, "Content-Type", `text/plain; charset="utf-8"`)
	writeHeader(&b, "Content-Transfer-Encoding", "base64")
	b.WriteString("\r\n")

	encoded := base64.StdEncoding.EncodeToString([]byte(m.Body))
	for len(encoded) > maxBodyLineLen {
		b.WriteString(encoded[:maxBodyLineLen])
		b.WriteString("\r\n")
		encoded = encoded[maxBodyLineLen:]
	}
	b.WriteString(encoded)
	b.WriteString("\r\n")

	return []byte(b.String()), nil
}

func writeHeader(b *strings.Builder, name, value string) {
	b.WriteString(name)
	b.WriteString(": ")
	b.WriteString(value)
	b.WriteString("\r\n")
}
```

- [ ] **Step 4: Write the two transports**

Create `server/internal/mail/smtp.go`:

```go
package mail

import (
	"context"
	"fmt"
	netmail "net/mail"
	"net/smtp"
	"net/url"
	"time"
)

// SMTPConfig is a dialable server plus the credentials for it.
type SMTPConfig struct {
	Addr     string // host:port, ready for net/smtp
	Host     string // host alone, for the TLS handshake
	Username string
	Password string
}

// ParseSMTPURL reads SMTP_URL. For SendGrid that is
//
//	smtp://apikey:<API-KEY>@smtp.sendgrid.net:587
//
// where the username is the LITERAL STRING "apikey" — not the account's email
// address, not a name. Port 587 with STARTTLS rather than implicit TLS on 465:
// it is the port SendGrid documents first and the one least likely to be
// blocked outbound by a VPS provider.
//
// Everything missing is an error rather than a default, because every default
// here produces a server that starts and cannot send.
func ParseSMTPURL(raw string) (SMTPConfig, error) {
	if raw == "" {
		return SMTPConfig{}, fmt.Errorf("SMTP_URL is empty")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return SMTPConfig{}, fmt.Errorf("SMTP_URL is not a URL: %w", err)
	}
	if u.Scheme != "smtp" {
		return SMTPConfig{}, fmt.Errorf("SMTP_URL scheme is %q, want smtp", u.Scheme)
	}
	if u.Hostname() == "" || u.Port() == "" {
		return SMTPConfig{}, fmt.Errorf("SMTP_URL needs host and port, e.g. smtp://apikey:KEY@smtp.sendgrid.net:587")
	}
	if u.User == nil || u.User.Username() == "" {
		return SMTPConfig{}, fmt.Errorf("SMTP_URL needs a username (for SendGrid it is the literal string \"apikey\")")
	}
	password, ok := u.User.Password()
	if !ok || password == "" {
		return SMTPConfig{}, fmt.Errorf("SMTP_URL needs a password (the SendGrid API key; percent-encode any @ / or : in it)")
	}
	return SMTPConfig{
		Addr: u.Host, Host: u.Hostname(),
		Username: u.User.Username(), Password: password,
	}, nil
}

// SMTPSender talks to a real mail server. smtp.SendMail negotiates STARTTLS
// itself when the server advertises it, which SendGrid does on 587.
type SMTPSender struct {
	cfg  SMTPConfig
	from *netmail.Address
	now  func() time.Time
}

func NewSMTPSender(cfg SMTPConfig, from *netmail.Address) *SMTPSender {
	return &SMTPSender{cfg: cfg, from: from, now: time.Now}
}

func (s *SMTPSender) Send(ctx context.Context, m Message) error {
	raw, err := Render(s.from, m, s.now())
	if err != nil {
		return err
	}
	to, err := netmail.ParseAddress(m.To)
	if err != nil {
		return err
	}
	// net/smtp has no context support. The caller gives this goroutine a
	// deadline of its own (see server/mailer.go), so the check here is for the
	// case where the deadline passed while the message sat in a queue.
	if err := ctx.Err(); err != nil {
		return err
	}
	auth := smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)
	return smtp.SendMail(s.cfg.Addr, auth, s.from.Address, []string{to.Address}, raw)
}
```

Create `server/internal/mail/log.go`:

```go
package mail

import (
	"context"
	"fmt"
	"io"
	netmail "net/mail"
	"sync"
)

// LogSender prints the message instead of sending it. It is what
// MAIL_TRANSPORT=log selects, so a developer can read the verification link
// out of the server's own output and follow it.
//
// It cannot fail. A development transport that errors turns every handler's
// mail-failure branch into the one a developer exercises, which is exactly
// backwards.
type LogSender struct {
	mu   sync.Mutex
	w    io.Writer
	from *netmail.Address
}

func NewLogSender(w io.Writer, from *netmail.Address) *LogSender {
	return &LogSender{w: w, from: from}
}

func (s *LogSender) Send(_ context.Context, m Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Fprintf(s.w, "\n--- mail (not sent: MAIL_TRANSPORT=log) ---\nFrom: %s\nTo: %s\nSubject: %s\n\n%s\n--- end ---\n",
		s.from, m.To, m.Subject, m.Body)
	return nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd server && gofmt -l ./internal/mail && go vet ./internal/mail/... && go test ./internal/mail/... -v`

Expected: PASS, all of them.

- [ ] **Step 6: Commit**

```bash
git add server/internal/mail
git commit -m "feat(server): add the mail package, SMTP and a log transport"
```

---

