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

## Task 2: Configuration, and refusing to start without it

**Files:**
- Create: `server/mailer.go`, `server/mailtemplates.go`, `server/mailer_test.go`
- Modify: `server/main.go` (the `Server` struct and startup)

**Interfaces:**
- Consumes: `mail.Sender`, `mail.ParseSMTPURL`, `mail.NewSMTPSender`, `mail.NewLogSender` (Task 1).
- Produces:
  - `func mailerFromEnv() (mail.Sender, error)`
  - `Server.mail mail.Sender` and `Server.cabinetOrigin string`
  - `func (s *Server) sendMail(m mail.Message)` — fire-and-forget, after commit
  - `func verifyEmail(cabinetOrigin, name, token string) mail.Message`
  - `func resetEmail(cabinetOrigin, name, token string) mail.Message`
  - `type recordingSender struct` in `mailer_test.go`, used by Tasks 3 and 4

- [ ] **Step 1: Write the failing tests**

Create `server/mailer_test.go`:

```go
package main

import (
	"context"
	"strings"
	"sync"
	"testing"

	"server/internal/mail"
)

// recordingSender stands in for a mail server in every handler test. It is in
// this file rather than each test's own because Tasks 3 and 4 both need it.
type recordingSender struct {
	mu   sync.Mutex
	sent []mail.Message
	err  error
}

func (s *recordingSender) Send(_ context.Context, m mail.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.sent = append(s.sent, m)
	return nil
}

func (s *recordingSender) messages() []mail.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]mail.Message(nil), s.sent...)
}

// lastTo returns the most recent message to an address, waiting for the
// send goroutine to run. Handlers send after committing and off the request's
// goroutine, so a test that reads immediately races them.
func (s *recordingSender) lastTo(t *testing.T, email string) mail.Message {
	t.Helper()
	for i := 0; i < 200; i++ {
		for _, m := range s.messages() {
			if m.To == email {
				return m
			}
		}
		waitABit()
	}
	t.Fatalf("no message to %s; got %+v", email, s.messages())
	return mail.Message{}
}

// A server that boots with mail silently disabled produces customers who
// cannot reset their password and an operator who finds out from a support
// ticket. Same shape as CABINET_ORIGIN, and for the same reason.
func TestMailerRefusesToStartWithNoConfiguration(t *testing.T) {
	t.Setenv("SMTP_URL", "")
	t.Setenv("MAIL_TRANSPORT", "")
	t.Setenv("MAIL_FROM", "Guardian <noreply@guardian.example>")
	if _, err := mailerFromEnv(); err == nil {
		t.Fatal("started with no SMTP_URL and no MAIL_TRANSPORT=log")
	}
}

func TestMailerAcceptsTheExplicitDevelopmentOptOut(t *testing.T) {
	t.Setenv("SMTP_URL", "")
	t.Setenv("MAIL_TRANSPORT", "log")
	t.Setenv("MAIL_FROM", "Guardian <noreply@guardian.example>")
	s, err := mailerFromEnv()
	if err != nil {
		t.Fatalf("log transport refused: %v", err)
	}
	if s == nil {
		t.Fatal("no sender")
	}
}

func TestMailerNeedsAFromAddressItCanParse(t *testing.T) {
	t.Setenv("SMTP_URL", "smtp://apikey:k@smtp.sendgrid.net:587")
	for _, from := range []string{"", "not an address"} {
		t.Setenv("MAIL_FROM", from)
		if _, err := mailerFromEnv(); err == nil {
			t.Errorf("accepted MAIL_FROM %q", from)
		}
	}
}

func TestMailerBuildsAnSMTPSenderFromTheURL(t *testing.T) {
	t.Setenv("MAIL_TRANSPORT", "")
	t.Setenv("SMTP_URL", "smtp://apikey:SG.k@smtp.sendgrid.net:587")
	t.Setenv("MAIL_FROM", "Guardian <noreply@guardian.example>")
	if _, err := mailerFromEnv(); err != nil {
		t.Fatalf("refused a valid configuration: %v", err)
	}
}

// The templates carry the one thing the email exists to deliver.
func TestTemplatesCarryTheLinkAndTheProductName(t *testing.T) {
	v := verifyEmail("https://guardian.example", "Мария", "tok-1")
	if !strings.Contains(v.Body, "https://guardian.example/#/verify/tok-1") {
		t.Fatalf("verification body has no usable link:\n%s", v.Body)
	}
	r := resetEmail("https://guardian.example", "", "tok-2")
	if !strings.Contains(r.Body, "https://guardian.example/#/reset/tok-2") {
		t.Fatalf("reset body has no usable link:\n%s", r.Body)
	}
	for _, m := range []mail.Message{v, r} {
		if m.Subject == "" {
			t.Error("a message has no subject")
		}
		if strings.Contains(m.Body, "%!") {
			t.Errorf("a format verb went unfilled:\n%s", m.Body)
		}
	}
}
```

Add to `server/testsupport_test.go`:

```go
// waitABit is the smallest sleep worth having: handlers send mail on their own
// goroutine after committing, so a test that asserts on it has to yield.
func waitABit() { time.Sleep(5 * time.Millisecond) }
```

(`testsupport_test.go` already imports `time`; if it does not, add it.)

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd server && go test . -run 'TestMailer|TestTemplates'`

Expected: FAIL to compile — `undefined: mailerFromEnv`, `undefined: verifyEmail`, `undefined: resetEmail`.

- [ ] **Step 3: Write the mailer**

Create `server/mailer.go`:

```go
package main

import (
	"context"
	"fmt"
	"log/slog"
	netmail "net/mail"
	"os"
	"strings"
	"time"

	"server/internal/mail"
)

// mailSendTimeout bounds one message's round trip. It is longer than the
// request timeout on purpose: the send happens on its own goroutine, after the
// request's transaction has committed, so it is not holding anything up.
const mailSendTimeout = 20 * time.Second

// mailerFromEnv builds the sender from the environment, and refuses to build
// one at all when nothing is configured.
//
// There is deliberately no silent default. A service that boots happily with
// mail disabled produces customers who cannot reset their password and an
// operator who finds out from a support ticket; making the development path an
// explicit opt-in costs one line in a .env and removes that failure entirely.
// Same shape as CABINET_ORIGIN, for the same reason.
func mailerFromEnv() (mail.Sender, error) {
	rawFrom := strings.TrimSpace(os.Getenv("MAIL_FROM"))
	if rawFrom == "" {
		return nil, fmt.Errorf("MAIL_FROM is required, e.g. \"Guardian <noreply@example.com>\" " +
			"(the address must be verified in SendGrid)")
	}
	from, err := netmail.ParseAddress(rawFrom)
	if err != nil {
		return nil, fmt.Errorf("MAIL_FROM is not an address: %w", err)
	}

	if strings.EqualFold(strings.TrimSpace(os.Getenv("MAIL_TRANSPORT")), "log") {
		slog.Warn("MAIL_TRANSPORT=log: mail is printed, not sent")
		return mail.NewLogSender(os.Stdout, from), nil
	}

	raw := strings.TrimSpace(os.Getenv("SMTP_URL"))
	if raw == "" {
		return nil, fmt.Errorf("SMTP_URL is required: the provider to send through, e.g. " +
			"smtp://apikey:<API-KEY>@smtp.sendgrid.net:587 — or set MAIL_TRANSPORT=log to print mail instead")
	}
	cfg, err := mail.ParseSMTPURL(raw)
	if err != nil {
		return nil, err
	}
	return mail.NewSMTPSender(cfg, from), nil
}

// sendMail hands one message to the sender on its own goroutine, with its own
// deadline, and logs whatever happens.
//
// Two rules live here. It runs AFTER the caller's transaction has committed —
// an SMTP round trip inside a transaction holds a Postgres connection hostage
// for as long as the provider feels like taking. And it never reports failure
// to the caller: the row is already written, and telling somebody their
// registration failed because SendGrid was slow would be a lie.
//
// The request's own context is deliberately not used: it is cancelled the
// moment the response is written, which is before this has dialled anything.
func (s *Server) sendMail(m mail.Message) {
	if s.mail == nil {
		slog.Error("no mailer configured; dropping a message", "to", m.To, "subject", m.Subject)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), mailSendTimeout)
		defer cancel()
		if err := s.mail.Send(ctx, m); err != nil {
			// Deliberately not surfaced to the user. SendGrid also accepts and
			// then silently drops mail to a suppressed address, so "sent" is
			// never a delivery guarantee anyway — see specs/server.md.
			slog.Error("send mail", "to", m.To, "subject", m.Subject, "error", err)
			return
		}
		slog.Info("sent mail", "to", m.To, "subject", m.Subject)
	}()
}
```

Create `server/mailtemplates.go`:

```go
package main

import (
	"fmt"
	"strings"

	"server/internal/mail"
)

// Every word Guardian sends, in one file.
//
// Russian, plain text, no HTML — per specs/2026-09-09-cabinet-v1-design.md §2.
// The cabinet's own interface is English because it has no i18n yet; when that
// lands, this file is the one place the language lives.
//
// No tracking pixels, no link wrapping: a password reset that is tracked is a
// password reset with a third party's pixel in it.

func greeting(name string) string {
	if strings.TrimSpace(name) == "" {
		return "Здравствуйте!"
	}
	return fmt.Sprintf("Здравствуйте, %s!", strings.TrimSpace(name))
}

func verifyEmail(cabinetOrigin, name, token string) mail.Message {
	link := cabinetOrigin + "/#/verify/" + token
	return mail.Message{
		Subject: "Guardian: подтвердите адрес почты",
		Body: fmt.Sprintf(`%s

Вы зарегистрировались в Guardian. Чтобы подтвердить адрес, откройте ссылку:

%s

Ссылка действительна 48 часов.

Пока адрес не подтверждён, вы можете пользоваться кабинетом, но не сможете
скачать установщик для нового компьютера.

Если вы не регистрировались в Guardian, просто удалите это письмо —
без перехода по ссылке ничего не произойдёт.
`, greeting(name), link),
	}
}

func resetEmail(cabinetOrigin, name, token string) mail.Message {
	link := cabinetOrigin + "/#/reset/" + token
	return mail.Message{
		Subject: "Guardian: восстановление пароля",
		Body: fmt.Sprintf(`%s

Кто-то запросил восстановление пароля для этого адреса. Чтобы задать новый
пароль, откройте ссылку:

%s

Ссылка действительна один час и сработает один раз.

После смены пароля все сеансы будут завершены — на всех устройствах
понадобится войти заново.

Если вы не запрашивали восстановление, ничего делать не нужно: пароль
останется прежним.
`, greeting(name), link),
	}
}
```

- [ ] **Step 4: Wire it into the server**

In `server/main.go`, add to the `Server` struct:

```go
	// mail sends the handful of messages this product needs. Never nil in a
	// running server: startup fails when it cannot be built. A Server built
	// directly by a test may leave it nil, and sendMail says so loudly.
	mail mail.Sender

	// cabinetOrigin is the first CABINET_ORIGIN entry, used to build the links
	// in outgoing mail. A link is only useful if it points at the cabinet the
	// customer actually opens.
	cabinetOrigin string
```

Add `"server/internal/mail"` to the file's imports.

In `NewServer` (`server/main.go`), which is where every other piece of configuration is
read. It currently discards the origins it validates; capture them, because the links in
outgoing mail need one:

```go
	origins, err := cabinetOriginsFromEnv()
	if err != nil {
		return nil, err
	}
	mailer, err := mailerFromEnv()
	if err != nil {
		return nil, err
	}
```

(replacing the existing `if _, err := cabinetOriginsFromEnv(); err != nil { return nil, err }`),
and in the returned struct literal:

```go
	return &Server{
		pool:             pool,
		trustedProxies:   proxies,
		installerArchive: archive,
		cabinet:          cabinet,
		mail:             mailer,
		cabinetOrigin:    strings.TrimRight(origins[0], "/"),
	}, nil
```

Add `"strings"` to the imports if it is not already there. A configuration error returns
from `NewServer` like every other one, so `main()` already reports it and exits.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd server && gofmt -l . | grep -v webui; go vet ./... && go test . -run 'TestMailer|TestTemplates' -v`

Expected: PASS.

- [ ] **Step 6: Run the whole suite**

Run: `cd server && go test ./...`

Expected: green. Existing tests construct `&Server{pool: …}` directly and never call `sendMail`, so a nil mailer bothers nothing yet.

- [ ] **Step 7: Commit**

```bash
git add server/mailer.go server/mailtemplates.go server/mailer_test.go server/main.go
git commit -m "feat(server): configure the mailer, and refuse to start without one"
```

---

## Task 3: Email verification, and the one thing it gates

**Files:**
- Create: `server/db/migrations/00019_email_tokens.sql`, `server/db/queries/email_tokens.sql`, `server/handlers_verify.go`, `server/verify_test.go`
- Modify: `server/handlers_auth.go` (register sends the mail), `server/handlers_agent.go` (`handleCreateBindingToken` gains the gate), `server/routes.go`, `server/authz_test.go`

**Interfaces:**
- Consumes: `newToken`, `hashToken` (`server/auth.go`); `s.sendMail`, `verifyEmail`, `s.cabinetOrigin` (Task 2); `recordingSender` (Task 2).
- Produces: `db.CreateEmailToken`, `db.ConsumeEmailToken`, `db.MarkEmailVerified`, `db.DeleteEmailTokensFor`, `db.GetUserByID`, `db.PurgeExpiredEmailTokens`; routes `POST /api/v1/auth/verify` and `POST /api/v1/account/verify/resend`.

- [ ] **Step 1: Write the failing tests**

Create `server/verify_test.go`:

```go
package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// tokenFromLink pulls the opaque token out of the one link in an email.
func tokenFromLink(t *testing.T, body, prefix string) string {
	t.Helper()
	i := strings.Index(body, prefix)
	if i < 0 {
		t.Fatalf("no %q in:\n%s", prefix, body)
	}
	rest := body[i+len(prefix):]
	if j := strings.IndexAny(rest, " \r\n"); j >= 0 {
		rest = rest[:j]
	}
	if rest == "" {
		t.Fatalf("empty token in:\n%s", body)
	}
	return rest
}

func TestRegistrationSendsAVerificationLinkThatWorks(t *testing.T) {
	sender := &recordingSender{}
	s := &Server{pool: testPool(t), mail: sender, cabinetOrigin: "https://guardian.example"}
	h := s.setupRoutes()

	registerAndLogin(t, s, "parent@example.com")

	msg := sender.lastTo(t, "parent@example.com")
	token := tokenFromLink(t, msg.Body, "https://guardian.example/#/verify/")

	if rr := doJSON(t, h, "POST", "/api/v1/auth/verify", map[string]string{"token": token}, nil); rr.Code != 204 {
		t.Fatalf("verify: %d %s", rr.Code, rr.Body.String())
	}

	var verified bool
	ctx := context.Background()
	if err := s.pool.QueryRow(ctx,
		`SELECT email_verified_at IS NOT NULL FROM users WHERE email = 'parent@example.com'`).Scan(&verified); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !verified {
		t.Fatal("the column that has never been written still has not been written")
	}
}

// Single use. A verification link in a mailbox somebody else later reads must
// not still work.
func TestAVerificationTokenWorksOnlyOnce(t *testing.T) {
	sender := &recordingSender{}
	s := &Server{pool: testPool(t), mail: sender, cabinetOrigin: "https://guardian.example"}
	h := s.setupRoutes()
	registerAndLogin(t, s, "parent@example.com")
	token := tokenFromLink(t, sender.lastTo(t, "parent@example.com").Body, "https://guardian.example/#/verify/")

	doJSON(t, h, "POST", "/api/v1/auth/verify", map[string]string{"token": token}, nil)
	rr := doJSON(t, h, "POST", "/api/v1/auth/verify", map[string]string{"token": token}, nil)
	if rr.Code != 400 {
		t.Fatalf("a spent token was accepted again: %d", rr.Code)
	}
}

func TestAnUnknownVerificationTokenIsRefused(t *testing.T) {
	s := &Server{pool: testPool(t), mail: &recordingSender{}, cabinetOrigin: "https://guardian.example"}
	h := s.setupRoutes()
	for _, token := range []string{"", "not-a-token", strings.Repeat("a", 64)} {
		if rr := doJSON(t, h, "POST", "/api/v1/auth/verify", map[string]string{"token": token}, nil); rr.Code != 400 {
			t.Errorf("token %q answered %d, want 400", token, rr.Code)
		}
	}
}

// The gate. An unverified account can do everything except the two things that
// reach outside it; installers are the one that exists today.
func TestAnUnverifiedAccountCannotMintABindingToken(t *testing.T) {
	sender := &recordingSender{}
	s := &Server{pool: testPool(t), mail: sender, cabinetOrigin: "https://guardian.example"}
	h := s.setupRoutes()
	c := registerAndLogin(t, s, "parent@example.com")

	if rr := doJSON(t, h, "POST", "/api/v1/binding-tokens", nil, c); rr.Code != 403 {
		t.Fatalf("an unverified account minted an installer token: %d %s", rr.Code, rr.Body.String())
	}

	// Everything else still works: blocking the whole cabinet would mean a
	// customer who mistypes their address cannot see what they bought.
	if rr := doJSON(t, h, "GET", "/api/v1/rooms", nil, c); rr.Code != 200 {
		t.Fatalf("an unverified account could not read its rooms: %d", rr.Code)
	}
	if rr := doJSON(t, h, "POST", "/api/v1/rooms", map[string]string{"name": "Kids"}, c); rr.Code != 201 {
		t.Fatalf("an unverified account could not create a room: %d", rr.Code)
	}

	token := tokenFromLink(t, sender.lastTo(t, "parent@example.com").Body, "https://guardian.example/#/verify/")
	doJSON(t, h, "POST", "/api/v1/auth/verify", map[string]string{"token": token}, nil)

	if rr := doJSON(t, h, "POST", "/api/v1/binding-tokens", nil, c); rr.Code != 201 {
		t.Fatalf("a verified account still could not mint: %d %s", rr.Code, rr.Body.String())
	}
}

// A link eaten by a spam filter must not be the end of the story, because
// verification gates handing out installers.
func TestVerificationCanBeResent(t *testing.T) {
	sender := &recordingSender{}
	s := &Server{pool: testPool(t), mail: sender, cabinetOrigin: "https://guardian.example"}
	h := s.setupRoutes()
	c := registerAndLogin(t, s, "parent@example.com")

	before := len(sender.messages())
	if rr := doJSON(t, h, "POST", "/api/v1/account/verify/resend", nil, c); rr.Code != 204 {
		t.Fatalf("resend: %d %s", rr.Code, rr.Body.String())
	}
	for i := 0; i < 200 && len(sender.messages()) == before; i++ {
		waitABit()
	}
	if len(sender.messages()) <= before {
		t.Fatal("resend sent nothing")
	}

	// The newest link works, which also proves the old one was replaced rather
	// than accumulating.
	msgs := sender.messages()
	token := tokenFromLink(t, msgs[len(msgs)-1].Body, "https://guardian.example/#/verify/")
	if rr := doJSON(t, h, "POST", "/api/v1/auth/verify", map[string]string{"token": token}, nil); rr.Code != 204 {
		t.Fatalf("the resent link did not work: %d", rr.Code)
	}
}

// Nobody else's address. The token is bearer proof of one mailbox; a session
// is irrelevant to it.
func TestVerifyingIsNotDoneWithASession(t *testing.T) {
	sender := &recordingSender{}
	s := &Server{pool: testPool(t), mail: sender, cabinetOrigin: "https://guardian.example"}
	h := s.setupRoutes()
	registerAndLogin(t, s, "a@example.com")
	other := registerAndLogin(t, s, "b@example.com")
	token := tokenFromLink(t, sender.lastTo(t, "a@example.com").Body, "https://guardian.example/#/verify/")

	// B's session, A's token: A gets verified, because the token is what
	// proves the mailbox.
	if rr := doJSON(t, h, "POST", "/api/v1/auth/verify", map[string]string{"token": token}, other); rr.Code != 204 {
		t.Fatalf("verify with another session: %d", rr.Code)
	}
	ctx := context.Background()
	var aVerified, bVerified bool
	if err := s.pool.QueryRow(ctx,
		`SELECT
		   (SELECT email_verified_at IS NOT NULL FROM users WHERE email='a@example.com'),
		   (SELECT email_verified_at IS NOT NULL FROM users WHERE email='b@example.com')`).
		Scan(&aVerified, &bVerified); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !aVerified || bVerified {
		t.Fatalf("verified the wrong user: a=%v b=%v", aVerified, bVerified)
	}
}

// Mail is best-effort by design, and registration is not.
func TestRegistrationSucceedsWhenMailFails(t *testing.T) {
	sender := &recordingSender{err: http.ErrServerClosed}
	s := &Server{pool: testPool(t), mail: sender, cabinetOrigin: "https://guardian.example"}
	h := s.setupRoutes()

	rr := doJSON(t, h, "POST", "/api/v1/auth/register",
		map[string]string{"email": "parent@example.com", "password": "correct-horse-battery"}, nil)
	if rr.Code != 201 {
		t.Fatalf("registration failed because mail did: %d %s", rr.Code, rr.Body.String())
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd server && go test . -run 'TestRegistrationSends|TestAVerificationToken|TestAnUnknownVerification|TestAnUnverifiedAccount|TestVerificationCanBeResent|TestVerifyingIsNot|TestRegistrationSucceedsWhenMailFails'`

Expected: FAIL — `unknown field mail in struct literal`, then once that compiles, `404` from the routes that do not exist.

- [ ] **Step 3: Write the migration**

Create `server/db/migrations/00019_email_tokens.sql`:

```sql
-- +goose Up
-- One digest table for both the "confirm your address" and the "reset your
-- password" links. They have the same shape — a single-use bearer token with
-- an expiry, tied to one user and one address — and splitting them would mean
-- two tables, two purges and two sets of the same mistakes.
--
-- No RLS, for the same reason sessions has none: the row carries no
-- account_id and is reached before any account scope exists. A reset link is
-- followed by somebody who is, by definition, not signed in.
CREATE TABLE email_tokens (
    token_hash TEXT PRIMARY KEY,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    purpose    TEXT NOT NULL CHECK (purpose IN ('verify', 'reset')),
    -- The address the link was sent to, which is not necessarily the user's
    -- current one: somebody who changes their address must not have an old
    -- link confirm the new one.
    email      TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Every read is "this user's tokens of this purpose", when superseding them.
CREATE INDEX idx_email_tokens_user ON email_tokens(user_id, purpose);

-- +goose Down
DROP TABLE email_tokens;
```

- [ ] **Step 4: Write the queries**

Create `server/db/queries/email_tokens.sql`:

```sql
-- name: CreateEmailToken :exec
INSERT INTO email_tokens (token_hash, user_id, purpose, email, expires_at)
VALUES ($1, $2, $3, $4, $5);

-- name: DeleteEmailTokensFor :exec
-- Minting supersedes: a fresh link invalidates the previous one, so a mailbox
-- never holds two working links to the same door.
DELETE FROM email_tokens WHERE user_id = $1 AND purpose = $2;

-- name: ConsumeEmailToken :one
-- Marks the token used and returns it, in one statement. Two statements would
-- be a race: two clicks on the same link, milliseconds apart, would both find
-- it unused. The WHERE clause carries every condition, so a spent, expired or
-- unknown token all return no rows and are answered identically.
UPDATE email_tokens SET used_at = now()
WHERE token_hash = $1 AND purpose = $2 AND used_at IS NULL AND expires_at > now()
RETURNING user_id, email;

-- name: MarkEmailVerified :execrows
-- Only when the address still matches the one the link was sent to, and only
-- when it is not already verified.
UPDATE users SET email_verified_at = now()
WHERE id = $1 AND email = $2 AND email_verified_at IS NULL;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: SetPassword :exec
UPDATE users SET password_hash = $2 WHERE id = $1;

-- name: DeleteSessionsForUser :execrows
-- Every session, for a completed reset.
DELETE FROM sessions WHERE user_id = $1;

-- name: DeleteOtherSessionsForUser :execrows
-- Every session except the one asking, for a password change from inside the
-- cabinet: the person doing it should not be signed out by their own action.
DELETE FROM sessions WHERE user_id = $1 AND token_hash <> $2;

-- name: PurgeExpiredEmailTokens :execrows
-- Spent or expired, plus a day of grace so a "my link says it is invalid"
-- question can still be answered by looking.
DELETE FROM email_tokens WHERE expires_at < now() - interval '1 day';
```

- [ ] **Step 5: Regenerate the sqlc code**

Run: `cd server && go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.27.0 generate`

Expected: a new `internal/db/email_tokens.sql.go` and an `EmailToken` model. CI runs `sqlc diff` and fails when the committed output does not match, so this step is not optional.

- [ ] **Step 6: Write the handlers**

Create `server/handlers_verify.go`:

```go
package main

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"server/internal/db"
)

// verifyTokenTTL is how long a confirmation link lives. Longer than a reset
// link because it is far less dangerous — it proves a mailbox, it does not
// open an account — and because a registration email is often read the next
// day.
const verifyTokenTTL = 48 * time.Hour

// sendVerification mints a link and mails it, superseding any previous one.
// It is called after the caller's transaction has committed.
func (s *Server) sendVerification(ctx context.Context, userID uuid.UUID, email, name string) {
	plain, hash := newToken()
	q := db.New(s.pool)
	if err := q.DeleteEmailTokensFor(ctx, db.DeleteEmailTokensForParams{
		UserID: userID, Purpose: "verify",
	}); err != nil {
		slog.Error("supersede verification tokens", "error", err)
		return
	}
	if err := q.CreateEmailToken(ctx, db.CreateEmailTokenParams{
		TokenHash: hash, UserID: userID, Purpose: "verify", Email: email,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(verifyTokenTTL), Valid: true},
	}); err != nil {
		slog.Error("create verification token", "error", err)
		return
	}
	s.sendMail(verifyEmail(s.cabinetOrigin, name, plain))
}

type tokenRequest struct {
	Token string `json:"token"`
}

// handleVerifyEmail consumes a confirmation link. Unauthenticated on purpose:
// the token is what proves the mailbox, and the link is often opened in a
// browser that has never signed in.
func (s *Server) handleVerifyEmail(w http.ResponseWriter, r *http.Request) {
	var req tokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Token == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "This confirmation link is not valid"})
		return
	}

	ctx := r.Context()
	row, err := db.New(s.pool).ConsumeEmailToken(ctx, db.ConsumeEmailTokenParams{
		TokenHash: hashToken(req.Token), Purpose: "verify",
	})
	if err != nil {
		// Unknown, spent and expired are one answer. Distinguishing them tells
		// somebody holding a stolen link which kind of wrong it is.
		if !errors.Is(err, pgx.ErrNoRows) {
			slog.Error("consume verification token", "error", err)
		}
		writeJSON(w, http.StatusBadRequest,
			ErrorResponse{Error: "This confirmation link is not valid or has already been used"})
		return
	}

	if _, err := db.New(s.pool).MarkEmailVerified(ctx, db.MarkEmailVerifiedParams{
		ID: row.UserID, Email: row.Email,
	}); err != nil {
		slog.Error("mark email verified", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Internal error"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleResendVerification sends the link again to the signed-in user's own
// address. Rate-limited at the route, and a no-op for an address that is
// already confirmed.
func (s *Server) handleResendVerification(w http.ResponseWriter, r *http.Request) {
	t, ok := mustTenant(w, r)
	if !ok {
		return
	}
	user, err := db.New(s.pool).GetUserByID(r.Context(), t.UserID)
	if err != nil {
		slog.Error("read user for resend", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Internal error"})
		return
	}
	if user.EmailVerifiedAt.Valid {
		// Already done. Answering 204 keeps the cabinet's code path simple and
		// tells a re-clicker nothing they did not know.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.sendVerification(r.Context(), user.ID, user.Email, user.Name)
	w.WriteHeader(http.StatusNoContent)
}
```

Add the imports this file needs: `context`, `encoding/json`, `github.com/google/uuid`, `github.com/jackc/pgx/v5/pgtype`.

- [ ] **Step 7: Send on registration, and gate the installer**

In `server/handlers_auth.go`, at the end of `handleRegister` — **after** `tx.Commit(ctx)` succeeds and before writing the response:

```go
	// After the commit, never inside it: an SMTP round trip inside a
	// transaction holds a Postgres connection for as long as the provider
	// takes, and a registration that already succeeded must not be undone by
	// a mail failure.
	s.sendVerification(context.WithoutCancel(ctx), user.ID, user.Email, user.Name)
```

Add `"context"` to that file's imports if it is not already there.

In `server/handlers_agent.go`, at the top of `handleCreateBindingToken`, immediately after `mustTenant`:

```go
	// An unverified address may not hand out installers. This and inviting
	// somebody are the two actions that reach outside the account — one adds
	// machines, the other adds people — and gating exactly these two is what
	// stops a typo'd or someone else's address from becoming a working fleet.
	// Everything else in the cabinet stays open: blocking it all would mean a
	// customer who mistypes their address cannot see what they bought.
	user, err := db.New(s.pool).GetUserByID(r.Context(), t.UserID)
	if err != nil {
		slog.Error("read user for the installer gate", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Internal error"})
		return
	}
	if !user.EmailVerifiedAt.Valid {
		writeJSON(w, http.StatusForbidden, ErrorResponse{
			Error: "Confirm your email address before installing the agent on a computer"})
		return
	}
```

The same gate belongs on `GET /api/v1/installer` (`server/installer.go`), which mints a binding token of its own — add the identical block after its `mustTenant`.

- [ ] **Step 8: Register and classify the routes**

In `server/routes.go`, in the unauthenticated auth group next to register and login:

```go
			r.With(perIP(loginRateLimit)).Post("/auth/verify", s.handleVerifyEmail)
```

and in the session-authenticated guest-reachable group:

```go
			r.With(perIP(registerRateLimit)).Post("/account/verify/resend", s.handleResendVerification)
```

In `server/authz_test.go`, add to `guestReachableRoutes`:

```go
	"POST /api/v1/account/verify/resend":                 true,
```

**Why the resend lives under `/account/` and not next to `/auth/verify`.**
`TestEveryAPIRouteIsClassified` skips every route under `/api/v1/auth/` — "no
session yet, so no role to check", which is true of the routes that are there
now. A session-authenticated route under that prefix would be invisible to the
one test whose whole job is to make sure nobody forgets to classify a route.
`POST /api/v1/auth/verify` itself stays unauthenticated and therefore needs no
entry; the resend is a signed-in action and belongs with the other thing a
signed-in person does to their own identity, `POST /api/v1/account/password`.

- [ ] **Step 9: Run the tests to verify they pass**

Run:
```sh
cd server && go run . migrate
go test . -run 'TestRegistrationSends|TestAVerificationToken|TestAnUnknownVerification|TestAnUnverifiedAccount|TestVerificationCanBeResent|TestVerifyingIsNot|TestRegistrationSucceedsWhenMailFails' -v
```

Expected: PASS, all of them.

- [ ] **Step 10: Run the whole suite**

Run: `cd server && gofmt -l . | grep -v webui; go vet ./... && go test ./...`

Expected: green. **Watch for enrollment tests that mint a binding token as a freshly registered user** — they now need a verified address. Fix them by verifying in the test helper rather than by loosening the gate: in `server/enroll_test.go`'s `mintBindingToken`, mark the user verified first with a direct `UPDATE users SET email_verified_at = now()` on the pool, and say in a comment why.

- [ ] **Step 11: Update the docs**

In `specs/api.md`: document `POST /api/v1/auth/verify` and `POST /api/v1/account/verify/resend`, and add to `POST /api/v1/binding-tokens` and `GET /api/v1/installer` that both answer `403` until the caller's address is confirmed. In `specs/server.md`: an `### email_tokens` section next to `### sessions`, saying it carries no RLS and why. In `CLAUDE.md`: the two routes in the endpoint list, and `email_tokens` in the table list.

- [ ] **Step 12: Commit**

```bash
git add server/db/migrations/00019_email_tokens.sql server/db/queries/email_tokens.sql \
        server/internal/db server/handlers_verify.go server/verify_test.go \
        server/handlers_auth.go server/handlers_agent.go server/installer.go \
        server/routes.go server/authz_test.go server/enroll_test.go \
        specs/api.md specs/server.md CLAUDE.md
git commit -m "feat(server): confirm email addresses, and gate installers on it"
```

---

## Task 4: Password reset and change

**Files:**
- Create: `server/handlers_password.go`, `server/password_test.go`
- Modify: `server/routes.go`, `server/authz_test.go`

**Interfaces:**
- Consumes: everything Task 3 produced, plus `hashPassword`, `verifyPassword` (`server/auth.go`) and `resetEmail` (Task 2).
- Produces: routes `POST /api/v1/auth/password/forgot`, `POST /api/v1/auth/password/reset`, `POST /api/v1/account/password`.

- [ ] **Step 1: Write the failing tests**

Create `server/password_test.go`:

```go
package main

import (
	"context"
	"testing"
)

const newPassword = "a-much-better-password"

func TestForgotSendsALinkThatResetsThePassword(t *testing.T) {
	sender := &recordingSender{}
	s := &Server{pool: testPool(t), mail: sender, cabinetOrigin: "https://guardian.example"}
	h := s.setupRoutes()
	registerAndLogin(t, s, "parent@example.com")

	if rr := doJSON(t, h, "POST", "/api/v1/auth/password/forgot",
		map[string]string{"email": "parent@example.com"}, nil); rr.Code != 204 {
		t.Fatalf("forgot: %d %s", rr.Code, rr.Body.String())
	}
	token := tokenFromLink(t, sender.lastTo(t, "parent@example.com").Body, "https://guardian.example/#/reset/")

	if rr := doJSON(t, h, "POST", "/api/v1/auth/password/reset",
		map[string]string{"token": token, "password": newPassword}, nil); rr.Code != 204 {
		t.Fatalf("reset: %d %s", rr.Code, rr.Body.String())
	}

	// The new one works and the old one does not.
	if rr := doJSON(t, h, "POST", "/api/v1/auth/login",
		map[string]string{"email": "parent@example.com", "password": newPassword}, nil); rr.Code != 200 {
		t.Fatalf("login with the new password: %d", rr.Code)
	}
	if rr := doJSON(t, h, "POST", "/api/v1/auth/login",
		map[string]string{"email": "parent@example.com", "password": "correct-horse-battery"}, nil); rr.Code == 200 {
		t.Fatal("the old password still works")
	}
}

// Account enumeration: the registration endpoint already treats it as a bug to
// avoid, and a reset endpoint that leaks it undoes that.
func TestForgotAnswersTheSameForAnUnknownAddress(t *testing.T) {
	sender := &recordingSender{}
	s := &Server{pool: testPool(t), mail: sender, cabinetOrigin: "https://guardian.example"}
	h := s.setupRoutes()
	registerAndLogin(t, s, "known@example.com")

	known := doJSON(t, h, "POST", "/api/v1/auth/password/forgot", map[string]string{"email": "known@example.com"}, nil)
	unknown := doJSON(t, h, "POST", "/api/v1/auth/password/forgot", map[string]string{"email": "nobody@example.com"}, nil)

	if known.Code != 204 || unknown.Code != 204 {
		t.Fatalf("statuses differ: known %d, unknown %d", known.Code, unknown.Code)
	}
	if known.Body.String() != unknown.Body.String() {
		t.Fatalf("bodies differ: %q vs %q", known.Body.String(), unknown.Body.String())
	}
	// And nothing was sent to the address that does not exist.
	for _, m := range sender.messages() {
		if m.To == "nobody@example.com" {
			t.Fatal("mailed an address with no account")
		}
	}
}

// A reset is the one moment somebody might be taking an account back from
// whoever has been in it.
func TestACompletedResetEndsEverySession(t *testing.T) {
	sender := &recordingSender{}
	s := &Server{pool: testPool(t), mail: sender, cabinetOrigin: "https://guardian.example"}
	h := s.setupRoutes()
	first := registerAndLogin(t, s, "parent@example.com")

	doJSON(t, h, "POST", "/api/v1/auth/password/forgot", map[string]string{"email": "parent@example.com"}, nil)
	token := tokenFromLink(t, sender.lastTo(t, "parent@example.com").Body, "https://guardian.example/#/reset/")
	doJSON(t, h, "POST", "/api/v1/auth/password/reset",
		map[string]string{"token": token, "password": newPassword}, nil)

	if rr := doJSON(t, h, "GET", "/api/v1/me", nil, first); rr.Code != 401 {
		t.Fatalf("a session survived the reset: %d", rr.Code)
	}
}

func TestAResetTokenWorksOnlyOnceAndExpires(t *testing.T) {
	sender := &recordingSender{}
	s := &Server{pool: testPool(t), mail: sender, cabinetOrigin: "https://guardian.example"}
	h := s.setupRoutes()
	registerAndLogin(t, s, "parent@example.com")
	doJSON(t, h, "POST", "/api/v1/auth/password/forgot", map[string]string{"email": "parent@example.com"}, nil)
	token := tokenFromLink(t, sender.lastTo(t, "parent@example.com").Body, "https://guardian.example/#/reset/")

	doJSON(t, h, "POST", "/api/v1/auth/password/reset", map[string]string{"token": token, "password": newPassword}, nil)
	rr := doJSON(t, h, "POST", "/api/v1/auth/password/reset",
		map[string]string{"token": token, "password": "another-password-entirely"}, nil)
	if rr.Code != 400 {
		t.Fatalf("a spent reset token was accepted: %d", rr.Code)
	}

	// And an expired one is refused. Backdating is done on the pool, which the
	// handler path never does.
	doJSON(t, h, "POST", "/api/v1/auth/password/forgot", map[string]string{"email": "parent@example.com"}, nil)
	fresh := tokenFromLink(t, sender.lastTo(t, "parent@example.com").Body, "https://guardian.example/#/reset/")
	if _, err := s.pool.Exec(context.Background(),
		`UPDATE email_tokens SET expires_at = now() - interval '1 minute' WHERE purpose = 'reset'`); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	if rr := doJSON(t, h, "POST", "/api/v1/auth/password/reset",
		map[string]string{"token": fresh, "password": newPassword}, nil); rr.Code != 400 {
		t.Fatalf("an expired reset token was accepted: %d", rr.Code)
	}
}

func TestChangingThePasswordKeepsTheCallersOwnSession(t *testing.T) {
	s := &Server{pool: testPool(t), mail: &recordingSender{}, cabinetOrigin: "https://guardian.example"}
	h := s.setupRoutes()
	mine := registerAndLogin(t, s, "parent@example.com")
	// A second sign-in for the same person: a phone, say.
	other := loginAs(t, s, "parent@example.com", "correct-horse-battery")

	if rr := doJSON(t, h, "POST", "/api/v1/account/password",
		map[string]string{"current": "correct-horse-battery", "new": newPassword}, mine); rr.Code != 204 {
		t.Fatalf("change: %d %s", rr.Code, rr.Body.String())
	}

	if rr := doJSON(t, h, "GET", "/api/v1/me", nil, mine); rr.Code != 200 {
		t.Fatalf("the caller was signed out by their own password change: %d", rr.Code)
	}
	if rr := doJSON(t, h, "GET", "/api/v1/me", nil, other); rr.Code != 401 {
		t.Fatalf("the other device kept its session: %d", rr.Code)
	}
}

func TestChangingThePasswordNeedsTheCurrentOne(t *testing.T) {
	s := &Server{pool: testPool(t), mail: &recordingSender{}, cabinetOrigin: "https://guardian.example"}
	h := s.setupRoutes()
	c := registerAndLogin(t, s, "parent@example.com")

	if rr := doJSON(t, h, "POST", "/api/v1/account/password",
		map[string]string{"current": "not-the-password", "new": newPassword}, c); rr.Code != 403 {
		t.Fatalf("changed the password without the current one: %d", rr.Code)
	}
	if rr := doJSON(t, h, "POST", "/api/v1/account/password",
		map[string]string{"current": "correct-horse-battery", "new": "short"}, c); rr.Code != 400 {
		t.Fatalf("accepted a password below the minimum: %d", rr.Code)
	}
}
```

Add to `server/session_test.go`, next to `registerAndLogin`:

```go
// loginAs signs an existing user in again, for tests about a second device.
func loginAs(t *testing.T, s *Server, email, password string) *http.Cookie {
	t.Helper()
	rr := doJSON(t, s.setupRoutes(), "POST", "/api/v1/auth/login",
		map[string]string{"email": email, "password": password}, nil)
	if rr.Code != 200 {
		t.Fatalf("login as %s: %d %s", email, rr.Code, rr.Body.String())
	}
	for _, c := range rr.Result().Cookies() {
		if c.Name == sessionCookieName {
			return c
		}
	}
	t.Fatalf("login as %s set no session cookie", email)
	return nil
}
```

(`sessionCookieName` is the constant `handleLogin` sets, `handlers_auth.go:144`.)

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd server && go test . -run 'TestForgot|TestACompletedReset|TestAResetToken|TestChangingThePassword'`

Expected: FAIL with `404` — none of the three routes exist.

- [ ] **Step 3: Write the handlers**

Create `server/handlers_password.go`:

```go
package main

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"server/internal/db"
)

// resetTokenTTL is short on purpose: a reset link is a bearer credential for
// an entire account, and an hour is long enough to walk to a laptop.
const resetTokenTTL = time.Hour

type forgotRequest struct {
	Email string `json:"email"`
}

type resetRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

type changePasswordRequest struct {
	Current string `json:"current"`
	New     string `json:"new"`
}

// handleForgotPassword mails a reset link, and answers 204 either way.
//
// The 204-always is not politeness: this endpoint would otherwise be an
// account-enumeration oracle, undoing the same care the registration endpoint
// takes. The dummy hash on the miss path is what keeps the timing from leaking
// what the status code does not — argon2id is the expensive part of the real
// path, so skipping it would make a miss measurably faster.
func (s *Server) handleForgotPassword(w http.ResponseWriter, r *http.Request) {
	var req forgotRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Invalid JSON"})
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))

	ctx := r.Context()
	user, err := db.New(s.pool).GetUserByEmail(ctx, email)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			slog.Error("look up user for reset", "error", err)
		}
		// Spend the same work the found path spends.
		_, _ = hashPassword("dummy-work-so-the-timing-matches")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	plain, hash := newToken()
	q := db.New(s.pool)
	if err := q.DeleteEmailTokensFor(ctx, db.DeleteEmailTokensForParams{
		UserID: user.ID, Purpose: "reset",
	}); err != nil {
		slog.Error("supersede reset tokens", "error", err)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := q.CreateEmailToken(ctx, db.CreateEmailTokenParams{
		TokenHash: hash, UserID: user.ID, Purpose: "reset", Email: user.Email,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(resetTokenTTL), Valid: true},
	}); err != nil {
		slog.Error("create reset token", "error", err)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.sendMail(resetEmail(s.cabinetOrigin, user.Name, plain))
	w.WriteHeader(http.StatusNoContent)
}

// handleResetPassword completes a reset and signs every device out.
func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	var req resetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Token == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "This link is not valid"})
		return
	}
	if len(req.Password) < minPasswordLen {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Password must be at least 8 characters"})
		return
	}
	if len(req.Password) > maxPasswordLen {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Password is too long"})
		return
	}

	hash, err := hashPassword(req.Password)
	if err != nil {
		slog.Error("hash password", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Internal error"})
		return
	}

	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Internal error"})
		return
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)

	row, err := q.ConsumeEmailToken(ctx, db.ConsumeEmailTokenParams{
		TokenHash: hashToken(req.Token), Purpose: "reset",
	})
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			slog.Error("consume reset token", "error", err)
		}
		writeJSON(w, http.StatusBadRequest,
			ErrorResponse{Error: "This link is not valid, has already been used, or has expired"})
		return
	}
	if err := q.SetPassword(ctx, db.SetPasswordParams{ID: row.UserID, PasswordHash: hash}); err != nil {
		slog.Error("set password", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Internal error"})
		return
	}
	// Every session, including the one that may belong to whoever the account
	// is being taken back from. This is the point of server-side sessions.
	if _, err := q.DeleteSessionsForUser(ctx, row.UserID); err != nil {
		slog.Error("delete sessions after reset", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Internal error"})
		return
	}
	if err := tx.Commit(ctx); err != nil {
		slog.Error("commit reset", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Internal error"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleChangePassword changes it from inside the cabinet, keeping the caller
// signed in and signing every other device out.
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	t, ok := mustTenant(w, r)
	if !ok {
		return
	}
	var req changePasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Invalid JSON"})
		return
	}
	if len(req.New) < minPasswordLen {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Password must be at least 8 characters"})
		return
	}
	if len(req.New) > maxPasswordLen {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Password is too long"})
		return
	}

	ctx := r.Context()
	user, err := db.New(s.pool).GetUserByID(ctx, t.UserID)
	if err != nil {
		slog.Error("read user for password change", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Internal error"})
		return
	}
	if !verifyPassword(user.PasswordHash, req.Current) {
		// 403 rather than 401: the session is fine, the claim about the
		// current password is not, and a 401 would send the cabinet to the
		// sign-in screen for a mistyped field.
		writeJSON(w, http.StatusForbidden, ErrorResponse{Error: "That is not your current password"})
		return
	}

	hash, err := hashPassword(req.New)
	if err != nil {
		slog.Error("hash password", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Internal error"})
		return
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Internal error"})
		return
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)
	if err := q.SetPassword(ctx, db.SetPasswordParams{ID: user.ID, PasswordHash: hash}); err != nil {
		slog.Error("set password", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Internal error"})
		return
	}
	// Every device except this one. Signing the person out of the browser they
	// are standing in front of, as a consequence of their own deliberate act,
	// is a bug that reads as one.
	if _, err := q.DeleteOtherSessionsForUser(ctx, db.DeleteOtherSessionsForUserParams{
		UserID: user.ID, TokenHash: t.SessionTokenHash,
	}); err != nil {
		slog.Error("delete other sessions", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Internal error"})
		return
	}
	if err := tx.Commit(ctx); err != nil {
		slog.Error("commit password change", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Internal error"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

**`Tenant` needs the calling session's digest** for `DeleteOtherSessionsForUser`.

In `server/tenant.go`, add a field to `Tenant`:

```go
	// SessionTokenHash is the digest of the session that authenticated this
	// request, empty for agent requests. It exists so a password change can
	// delete every OTHER session — signing somebody out of the browser they
	// are standing in front of, as a consequence of their own deliberate act,
	// is a bug that reads as one.
	SessionTokenHash string
```

In `server/middleware.go`, `SessionAuth` currently computes the digest inline as
`q.GetSession(ctx, hashToken(plain))`. Give it a name and carry it through:

```go
		digest := hashToken(plain)
		session, err := q.GetSession(ctx, digest)
```

and set `SessionTokenHash: digest` wherever that function builds the `Tenant` it puts in
the context.

- [ ] **Step 4: Register and classify the routes**

In `server/routes.go`, with the unauthenticated auth routes:

```go
			r.With(perIP(loginRateLimit)).Post("/auth/password/forgot", s.handleForgotPassword)
			r.With(perIP(loginRateLimit)).Post("/auth/password/reset", s.handleResetPassword)
```

and in the session-authenticated guest-reachable group:

```go
			r.Post("/account/password", s.handleChangePassword)
```

In `server/authz_test.go`, add to `guestReachableRoutes`:

```go
	"POST /api/v1/account/password":                      true,
```

A guest changes their own password like anybody else — the route touches the caller's own user row and no account-wide state, which is why it is not manager-only despite the `/account/` prefix.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd server && go test . -run 'TestForgot|TestACompletedReset|TestAResetToken|TestChangingThePassword' -v`

Expected: PASS, all six.

- [ ] **Step 6: Run the whole suite**

Run: `cd server && gofmt -l . | grep -v webui; go vet ./... && go test ./...`

Expected: green.

- [ ] **Step 7: Update the docs**

In `specs/api.md`, the three endpoints with their exact request bodies, the always-`204` rule for `forgot` and the session consequences of each. In `specs/server.md`, a paragraph under the session section on what a reset and a change do to `sessions`. In `CLAUDE.md`, the three routes.

- [ ] **Step 8: Commit**

```bash
git add server/handlers_password.go server/password_test.go server/routes.go \
        server/authz_test.go server/tenant.go server/middleware.go server/session_test.go \
        specs/api.md specs/server.md CLAUDE.md
git commit -m "feat(server): reset a forgotten password, and change a known one"
```

---

## Task 5: Purge, and the deployment template

**Files:**
- Modify: `server/maintenance.go`, `server/maintenance_test.go`, `dist/server/server.env`, `specs/server.md`, `CLAUDE.md`

**Interfaces:**
- Consumes: `db.PurgeExpiredEmailTokens` (Task 3), `s.purgeOnce` (existing).
- Produces: nothing other tasks consume.

- [ ] **Step 1: Write the failing test**

Append to `server/maintenance_test.go`:

```go
// Spent and expired link rows are unusable — ConsumeEmailToken filters on both
// — so this is housekeeping, not security: without it the table only grows.
func TestPurgeRemovesExpiredEmailTokens(t *testing.T) {
	s := &Server{pool: testPool(t)}
	ctx := context.Background()
	c := registerAndLogin(t, s, "parent@example.com")
	_ = c

	var userID uuid.UUID
	if err := s.pool.QueryRow(ctx, `SELECT id FROM users WHERE email = 'parent@example.com'`).Scan(&userID); err != nil {
		t.Fatalf("read user: %v", err)
	}
	for _, age := range []string{"30 days", "1 minute"} {
		if _, err := s.pool.Exec(ctx, `
			INSERT INTO email_tokens (token_hash, user_id, purpose, email, expires_at)
			VALUES ($1, $2, 'reset', 'parent@example.com', now() - $3::interval)`,
			"hash-"+age, userID, age); err != nil {
			t.Fatalf("seed %s: %v", age, err)
		}
	}

	s.purgeOnce(ctx)

	var left int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM email_tokens`).Scan(&left); err != nil {
		t.Fatalf("count: %v", err)
	}
	// The day of grace keeps the one that expired a minute ago.
	if left != 1 {
		t.Fatalf("%d rows left, want 1", left)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd server && go test . -run TestPurgeRemovesExpiredEmailTokens`

Expected: FAIL — 2 rows left.

- [ ] **Step 3: Wire the purge in**

In `server/maintenance.go`, at the end of `purgeOnce`:

```go
	// email_tokens has no RLS (it has no account_id), so this needs no
	// SECURITY DEFINER function — the application role can delete directly.
	if n, err := db.New(s.pool).PurgeExpiredEmailTokens(ctx); err != nil {
		slog.Error("purge expired email tokens", "error", err)
	} else if n > 0 {
		slog.Info("purged expired email tokens", "count", n)
	}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd server && go test . -run TestPurgeRemoves -v`

Expected: PASS, together with the two purge tests that already exist.

- [ ] **Step 5: Write the deployment template**

In `dist/server/server.env`, add:

```sh
# --- Mail (SendGrid over SMTP) -------------------------------------------
# The username is the LITERAL STRING "apikey" — not your email, not a name.
# Getting this wrong costs an hour of "authentication failed".
# Percent-encode any @ : or / inside the key itself.
# The API key is scoped to "Mail Send" and nothing else.
SMTP_URL=smtp://apikey:SG.replace-me@smtp.sendgrid.net:587

# The From address, which MUST be verified in SendGrid or nothing is accepted.
MAIL_FROM=Guardian <noreply@example.com>

# Development only: print mail instead of sending it. Leave unset in
# production. The server refuses to start when neither this nor SMTP_URL is
# set, on purpose — a server that boots with mail silently disabled produces
# customers who cannot reset their password.
# MAIL_TRANSPORT=log

# Two prerequisites that are not code and decide whether mail arrives at all:
#
#  1. Domain authentication — the CNAME records SendGrid issues for SPF and
#     DKIM. Without them mail from a fresh domain lands in spam, and a reset
#     link in a spam folder is indistinguishable from a broken product. Single
#     Sender Verification is enough to SEND and not enough to be DELIVERED.
#  2. A plan that covers the volume. The free tier is 100 messages a day.
```

- [ ] **Step 6: Update the docs**

In `specs/server.md`, a **Mail** section: the package, the two transports, the startup refusal, the after-commit rule, and the accepted v1 limit — SendGrid suppresses bounced and complained-about addresses, returns success over SMTP, and silently drops the message, which this server cannot detect; the operator diagnoses it in SendGrid's Activity Feed, and closing it properly means the Event Webhook and a delivery-status column. Add `email_tokens` to the maintenance paragraph. In `CLAUDE.md`, add the three `.env` keys to the deployment paragraph and `server/internal/mail` to the file list.

- [ ] **Step 7: Run the whole suite and commit**

Run: `cd server && gofmt -l . | grep -v webui; go vet ./... && go test ./...`

```bash
git add server/maintenance.go server/maintenance_test.go dist/server/server.env specs/server.md CLAUDE.md
git commit -m "feat(server): purge spent email tokens, and document the mail setup"
```

---

## Task 6: The cabinet screens

Without these the emailed links land on the overview and nothing happens, which makes every task above invisible.

**Files:**
- Create: `server/webui/screens/verify.js`, `server/webui/screens/password.js`
- Modify: `server/webui/api.js`, `server/webui/router.js`, `server/webui/app.js`, `server/webui/screens/signin.js`, `server/webui/screens/settings.js`
- Test: `server/webui-tests/router.test.mjs`, `server/webui-tests/api.test.mjs`

**Interfaces:**
- Consumes: the five routes from Tasks 3 and 4.
- Produces: routes `verify` (`#/verify/<token>`), `reset` (`#/reset/<token>`), `forgot` (`#/forgot`); `api.verifyEmail`, `api.resendVerification`, `api.forgotPassword`, `api.resetPassword`, `api.changePassword`.

- [ ] **Step 1: Write the failing router tests**

Append to `server/webui-tests/router.test.mjs`:

```js
test('the links in an email are routes', () => {
  assert.deepEqual(parseRoute('#/verify/abc123'), { name: 'verify', params: { token: 'abc123' } })
  assert.deepEqual(parseRoute('#/reset/abc123'), { name: 'reset', params: { token: 'abc123' } })
  assert.deepEqual(parseRoute('#/forgot'), { name: 'forgot', params: {} })
})

test('a token with url-unsafe characters survives the round trip', () => {
  // The token is hex today, but a route that breaks on one is a trap set for
  // whoever changes the minting.
  for (const token of ['a b', 'a/b', 'a+b']) {
    const route = parseRoute(href('verify', { token }))
    assert.equal(route.name, 'verify')
    assert.equal(route.params.token, token)
  }
})
```

Append to `server/webui-tests/api.test.mjs`:

```js
test('the account-lifecycle calls post what the server expects', async () => {
  const { calls, fetchImpl } = fakeFetch(() => new Response(null, { status: 204 }))
  const api = createApi({ fetch: fetchImpl })

  await api.verifyEmail('tok')
  await api.forgotPassword('a@b.example')
  await api.resetPassword('tok', 'a-new-password')
  await api.changePassword('old', 'a-new-password')
  await api.resendVerification()

  assert.equal(calls[0].url, '/api/v1/auth/verify')
  assert.deepEqual(JSON.parse(calls[0].init.body), { token: 'tok' })
  assert.equal(calls[1].url, '/api/v1/auth/password/forgot')
  assert.deepEqual(JSON.parse(calls[1].init.body), { email: 'a@b.example' })
  assert.equal(calls[2].url, '/api/v1/auth/password/reset')
  assert.deepEqual(JSON.parse(calls[2].init.body), { token: 'tok', password: 'a-new-password' })
  assert.equal(calls[3].url, '/api/v1/account/password')
  assert.deepEqual(JSON.parse(calls[3].init.body), { current: 'old', new: 'a-new-password' })
  assert.equal(calls[4].url, '/api/v1/account/verify/resend')
})
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd server/webui-tests && node --test`

Expected: FAIL — `notfound` for the three routes, `api.verifyEmail is not a function`.

- [ ] **Step 3: Add the routes and the API calls**

In `server/webui/router.js`, add to `ROUTES`:

```js
  { name: 'forgot', pattern: ['forgot'] },
  { name: 'verify', pattern: ['verify', ':token'] },
  { name: 'reset', pattern: ['reset', ':token'] },
```

and to `href`:

```js
    case 'verify':
      return `#/verify/${encodeURIComponent(params.token)}`
    case 'reset':
      return `#/reset/${encodeURIComponent(params.token)}`
```

In `server/webui/api.js`, alongside the other auth calls:

```js
    verifyEmail: (token) => request('POST', '/api/v1/auth/verify', { body: { token } }),
    resendVerification: () => request('POST', '/api/v1/account/verify/resend'),
    forgotPassword: (email) => request('POST', '/api/v1/auth/password/forgot', { body: { email } }),
    resetPassword: (token, password) =>
      request('POST', '/api/v1/auth/password/reset', { body: { token, password } }),
    changePassword: (current, next) =>
      request('POST', '/api/v1/account/password', { body: { current, new: next } }),
```

- [ ] **Step 4: Write the screens**

Create `server/webui/screens/verify.js`:

```js
const { useState, useEffect } = window.React

import { html, Card, Button, Banner, Loading, BrandMark } from '../ui.js'
import { href, navigate } from '../router.js'

// The landing for a confirmation link. It runs before anybody signs in,
// because the link is usually opened in whatever browser the mail client
// hands it to.
export function VerifyScreen({ api, token, signedIn }) {
  const [state, setState] = useState('working') // working | done | failed
  const [error, setError] = useState(null)

  useEffect(() => {
    let alive = true
    api
      .verifyEmail(token)
      .then(() => alive && setState('done'))
      .catch((err) => {
        if (!alive) return
        setError(err)
        setState('failed')
      })
    return () => {
      alive = false
    }
  }, [api, token])

  return html`
    <div class="signin">
      <div class="signin-card">
        <div class="brand"><${BrandMark} /><span>Guardian</span></div>
        ${state === 'working' && html`<${Card}><${Loading} what="Confirming your address" /><//>`}
        ${state === 'done' &&
        html`<${Card} title="Address confirmed">
          <p>You can install the agent on a computer now.</p>
          <${Button} kind="primary" onClick=${() => navigate(signedIn ? 'install' : 'overview')}>
            ${signedIn ? 'Install the agent' : 'Sign in'}
          <//>
        <//>`}
        ${state === 'failed' &&
        html`<${Card} title="This link did not work">
          <${Banner} kind="warn">
            ${(error && error.message) || 'The link is not valid or has already been used.'}
          <//>
          <p class="hint">
            A confirmation link works once and lasts 48 hours. Sign in and ask for a new one from
            Settings.
          </p>
          <a href=${href('overview')}>Go to the cabinet</a>
        <//>`}
      </div>
    </div>
  `
}
```

Create `server/webui/screens/password.js`:

```js
const { useState } = window.React

import { html, Card, Button, Field, TextInput, Banner, ErrorBanner, BrandMark } from '../ui.js'
import { href, navigate } from '../router.js'

// "I forgot it". Signed out, so it draws its own page rather than living in
// the shell.
export function ForgotScreen({ api }) {
  const [email, setEmail] = useState('')
  const [busy, setBusy] = useState(false)
  const [sent, setSent] = useState(false)
  const [error, setError] = useState(null)

  async function submit(event) {
    event.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await api.forgotPassword(email.trim())
      setSent(true)
    } catch (err) {
      setError(err)
    } finally {
      setBusy(false)
    }
  }

  return html`
    <div class="signin">
      <div class="signin-card">
        <div class="brand"><${BrandMark} /><span>Guardian</span></div>
        <h1>Reset your password</h1>
        ${sent
          ? html`<${Card} title="Check your email">
              <p>
                If that address has an account, a link is on its way. It works once and lasts an
                hour.
              </p>
              <p class="hint">
                Nothing arrived? Look in spam, then try again — the answer here is the same whether
                or not the address is registered, on purpose.
              </p>
              <a href=${href('overview')}>Back to sign in</a>
            <//>`
          : html`
              <p class="signin-lede">We will email you a link to set a new one.</p>
              <${ErrorBanner} error=${error} onDismiss=${() => setError(null)} />
              <form onSubmit=${submit}>
                <${Field} label="Email">
                  <${TextInput}
                    type="email"
                    value=${email}
                    onChange=${setEmail}
                    autoComplete="email"
                    required=${true}
                  />
                <//>
                <${Button} type="submit" kind="primary" busy=${busy}>Email me a link<//>
              </form>
              <p class="signin-switch"><a href=${href('overview')}>Back to sign in</a></p>
            `}
      </div>
    </div>
  `
}

// The landing for a reset link.
export function ResetScreen({ api, token }) {
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [done, setDone] = useState(false)
  const [error, setError] = useState(null)

  async function submit(event) {
    event.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await api.resetPassword(token, password)
      setDone(true)
    } catch (err) {
      setError(err)
    } finally {
      setBusy(false)
    }
  }

  return html`
    <div class="signin">
      <div class="signin-card">
        <div class="brand"><${BrandMark} /><span>Guardian</span></div>
        <h1>Set a new password</h1>
        ${done
          ? html`<${Card} title="Password changed">
              <${Banner} kind="info">
                Every device has been signed out. Sign in again with the new password.
              <//>
              <${Button} kind="primary" onClick=${() => navigate('overview')}>Sign in<//>
            <//>`
          : html`
              <${ErrorBanner} error=${error} onDismiss=${() => setError(null)} />
              <form onSubmit=${submit}>
                <${Field} label="New password" hint="At least 8 characters.">
                  <${TextInput}
                    type="password"
                    value=${password}
                    onChange=${setPassword}
                    autoComplete="new-password"
                    required=${true}
                  />
                <//>
                <${Button} type="submit" kind="primary" busy=${busy}>Set the password<//>
              </form>
              <p class="signin-switch">
                Link expired? <a href=${href('forgot')}>Ask for a new one</a>
              </p>
            `}
      </div>
    </div>
  `
}

// The change form, for somebody already signed in. Lives in Settings.
export function ChangePasswordCard({ api }) {
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [busy, setBusy] = useState(false)
  const [done, setDone] = useState(false)
  const [error, setError] = useState(null)

  async function submit(event) {
    event.preventDefault()
    setBusy(true)
    setError(null)
    setDone(false)
    try {
      await api.changePassword(current, next)
      setCurrent('')
      setNext('')
      setDone(true)
    } catch (err) {
      setError(err)
    } finally {
      setBusy(false)
    }
  }

  return html`
    <${Card} title="Password">
      <${ErrorBanner} error=${error} onDismiss=${() => setError(null)} />
      ${done && html`<${Banner} kind="info">Changed. Every other device has been signed out.<//>`}
      <form class="inline-form" onSubmit=${submit}>
        <${Field} label="Current password">
          <${TextInput} type="password" value=${current} onChange=${setCurrent} autoComplete="current-password" />
        <//>
        <${Field} label="New password" hint="At least 8 characters.">
          <${TextInput} type="password" value=${next} onChange=${setNext} autoComplete="new-password" />
        <//>
        <${Button} type="submit" kind="primary" busy=${busy}>Change it<//>
      </form>
    <//>
  `
}
```

- [ ] **Step 5: Mount them**

In `server/webui/app.js`:

- import `VerifyScreen` from `./screens/verify.js` and `ForgotScreen`, `ResetScreen` from `./screens/password.js`;
- **before** the `status === 'signedout'` branch, handle the three routes that must work without a session:

```js
  // These three are reached from a link in an email, by somebody who is very
  // often not signed in. They are checked before the sign-in gate, not after.
  if (route.name === 'verify') {
    return html`<${VerifyScreen} api=${api} token=${route.params.token} signedIn=${status === 'ready'} />`
  }
  if (route.name === 'reset') {
    return html`<${ResetScreen} api=${api} token=${route.params.token} />`
  }
  if (status !== 'ready' && route.name === 'forgot') {
    return html`<${ForgotScreen} api=${api} />`
  }
```

In `server/webui/screens/signin.js`, under the form:

```js
        <p class="signin-switch"><a href=${href('forgot')}>Forgot your password?</a></p>
```

(and import `href` from `../router.js`).

In `server/webui/screens/settings.js`, import `ChangePasswordCard` from `./password.js`, render `<${ChangePasswordCard} api=${api} />` under the "You" card, and delete the sentence that says changing a password is not built yet.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `cd server/webui-tests && node --test`

Expected: PASS, everything including the new cases.

- [ ] **Step 7: Look at it**

With the fixture harness serving `server/webui/`, open `#/forgot`, `#/reset/anything` and `#/verify/anything` and confirm each draws its own page rather than the shell, and that Settings shows the change form.

- [ ] **Step 8: Update the docs and commit**

In `specs/cabinet.md`, add the three screens to the screen list and a line on why they are handled before the sign-in gate. In `CLAUDE.md`, update the cabinet's screen count and mention the three.

```bash
git add server/webui server/webui-tests specs/cabinet.md CLAUDE.md
git commit -m "feat(cabinet): confirm an address and recover a password"
```

---

