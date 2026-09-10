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
		"smtp.sendgrid.net:587",               // no scheme
		"https://smtp.sendgrid.net:587",       // not smtp
		"smtp://apikey:k@smtp.sendgrid.net",   // no port
		"smtp://smtp.sendgrid.net:587",        // no credentials
		"smtp://apikey@smtp.sendgrid.net:587", // no password
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
