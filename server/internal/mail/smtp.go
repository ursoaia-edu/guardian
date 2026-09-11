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
