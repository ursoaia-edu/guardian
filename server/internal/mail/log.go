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
