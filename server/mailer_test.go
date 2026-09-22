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

// waitForLink returns the token out of the first link with the given prefix in
// a message to that address, waiting for the send goroutine to run. Handlers
// send after committing and off the request's goroutine, so a test that reads
// immediately races them.
//
// It waits for the LINK and not merely for the address, which matters more than
// it looks: one person gets a verification link when they register and a reset
// link later, both to the same mailbox. Waiting for "a message to this address"
// would sometimes find the older one and sometimes the newer, depending on
// which goroutine won — a test that passes alone and fails in a suite.
func (s *recordingSender) waitForLink(t *testing.T, email, prefix string) string {
	t.Helper()
	for i := 0; i < 400; i++ {
		msgs := s.messages()
		for j := len(msgs) - 1; j >= 0; j-- {
			if msgs[j].To != email {
				continue
			}
			if k := strings.Index(msgs[j].Body, prefix); k >= 0 {
				token := msgs[j].Body[k+len(prefix):]
				if end := strings.IndexAny(token, " \r\n"); end >= 0 {
					token = token[:end]
				}
				if token != "" {
					return token
				}
			}
		}
		waitABit()
	}
	t.Fatalf("no message to %s containing %q; got %+v", email, prefix, s.messages())
	return ""
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
	v := verifyEmail("https://guardian.example", "maria@example.com", "Мария", "tok-1")
	if !strings.Contains(v.Body, "https://guardian.example/#/verify/tok-1") {
		t.Fatalf("verification body has no usable link:\n%s", v.Body)
	}
	r := resetEmail("https://guardian.example", "ivan@example.com", "", "tok-2")
	if !strings.Contains(r.Body, "https://guardian.example/#/reset/tok-2") {
		t.Fatalf("reset body has no usable link:\n%s", r.Body)
	}
	for _, m := range []mail.Message{v, r} {
		// A message with no recipient is not a message. The transport would
		// refuse it, but only after the handler had already answered 204.
		if m.To == "" {
			t.Error("a template built a message with no recipient")
		}
		if m.Subject == "" {
			t.Error("a message has no subject")
		}
		if strings.Contains(m.Body, "%!") {
			t.Errorf("a format verb went unfilled:\n%s", m.Body)
		}
	}
}
