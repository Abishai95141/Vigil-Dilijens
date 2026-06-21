package notify

import (
	"context"
	"fmt"
	"net/smtp"
	"strings"
)

// SMTPNotifier sends mail through an SMTP relay using PlainAuth over STARTTLS — the
// Gmail default on :587 (smtp.SendMail negotiates STARTTLS when the server advertises
// it). Credentials come from the caller (env): they are never hard-coded and never
// logged. whatsapp-web.js etc. needed a sidecar; this needs only a Gmail App Password.
type SMTPNotifier struct {
	Host string // "host:port", e.g. "smtp.gmail.com:587"
	User string // the authenticated sender (also the From)
	Pass string // app password — NEVER logged
}

// DefaultSMTPHost is Gmail's submission endpoint.
const DefaultSMTPHost = "smtp.gmail.com:587"

// NewSMTPNotifier builds a notifier; an empty host defaults to Gmail.
func NewSMTPNotifier(host, user, pass string) *SMTPNotifier {
	if strings.TrimSpace(host) == "" {
		host = DefaultSMTPHost
	}
	return &SMTPNotifier{Host: host, User: user, Pass: pass}
}

// Send dials the relay, upgrades to STARTTLS, authenticates, and sends. net/smtp has
// no context plumbing; the caller bounds this by running it off-digest in a goroutine.
func (n *SMTPNotifier) Send(_ context.Context, m Message) error {
	if len(m.To) == 0 {
		return fmt.Errorf("notify: send: no recipients")
	}
	if n.User == "" || n.Pass == "" {
		return fmt.Errorf("notify: send: missing SMTP credentials")
	}
	hostname := n.Host
	if i := strings.LastIndex(n.Host, ":"); i >= 0 {
		hostname = n.Host[:i] // PlainAuth checks the bare hostname against the cert
	}
	auth := smtp.PlainAuth("", n.User, n.Pass, hostname)
	return smtp.SendMail(n.Host, auth, n.User, m.To, rfc822(n.User, m))
}

// rfc822 renders the wire message. No Date header: the relay stamps one, which keeps
// this package free of time.Now (the injected-clock discipline). Header values are
// sanitised against CRLF injection (a stray newline in a subject could smuggle headers).
func rfc822(from string, m Message) []byte {
	var b strings.Builder
	b.WriteString("From: Vigil <" + sanitizeHeader(from) + ">\r\n")
	b.WriteString("To: " + sanitizeHeader(strings.Join(m.To, ", ")) + "\r\n")
	b.WriteString("Subject: " + sanitizeHeader(m.Subject) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(strings.ReplaceAll(m.Body, "\n", "\r\n"))
	return []byte(b.String())
}

// sanitizeHeader strips CR/LF so a value can never inject extra headers.
func sanitizeHeader(s string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
}
