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
