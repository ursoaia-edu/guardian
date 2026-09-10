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
