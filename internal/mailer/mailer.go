package mailer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/smtp"
	"strings"
	"sync"
	"time"
)

type Message struct {
	To       string
	From     string
	Subject  string
	HTMLBody string
	TextBody string
}

type Mailer interface {
	Send(ctx context.Context, msg Message) error
}

type SMTPConfig struct {
	Host string
	Port string
	From string
	User string
	Pass string
}

type SMTPMailer struct {
	cfg    SMTPConfig
	logger *slog.Logger
}

func NewSMTPMailer(cfg SMTPConfig, logger *slog.Logger) *SMTPMailer {
	if logger == nil {
		logger = slog.Default()
	}
	return &SMTPMailer{cfg: cfg, logger: logger}
}

func (m *SMTPMailer) Send(_ context.Context, msg Message) error {
	if msg.To == "" || msg.Subject == "" {
		return errors.New("mailer: missing To or Subject")
	}
	from := msg.From
	if from == "" {
		from = m.cfg.From
	}
	body := buildMIME(from, msg.To, msg.Subject, msg.HTMLBody, msg.TextBody)
	addr := m.cfg.Host + ":" + m.cfg.Port
	var auth smtp.Auth
	if m.cfg.User != "" {
		auth = smtp.PlainAuth("", m.cfg.User, m.cfg.Pass, m.cfg.Host)
	}
	if err := smtp.SendMail(addr, auth, from, []string{msg.To}, body); err != nil {
		return fmt.Errorf("mailer: smtp send: %w", err)
	}
	m.logger.Info("mail_sent", slog.String("to", msg.To), slog.String("subject", msg.Subject))
	return nil
}

func buildMIME(from, to, subject, html, text string) []byte {
	if text == "" {
		text = stripHTML(html)
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", to)
	fmt.Fprintf(&b, "Subject: %s\r\n", subject)
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z))
	fmt.Fprint(&b, "MIME-Version: 1.0\r\n")
	boundary := "gmcauditor-boundary-001"
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=%s\r\n\r\n", boundary)
	fmt.Fprintf(&b, "--%s\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n", boundary, text)
	fmt.Fprintf(&b, "--%s\r\nContent-Type: text/html; charset=utf-8\r\n\r\n%s\r\n", boundary, html)
	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return b.Bytes()
}

func stripHTML(s string) string {
	var b strings.Builder
	skip := false
	for _, r := range s {
		switch r {
		case '<':
			skip = true
		case '>':
			skip = false
		default:
			if !skip {
				b.WriteRune(r)
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// MemMailer captures messages in memory for tests.
type MemMailer struct {
	mu       sync.Mutex
	Messages []Message
}

func NewMemMailer() *MemMailer { return &MemMailer{} }

func (m *MemMailer) Send(_ context.Context, msg Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Messages = append(m.Messages, msg)
	return nil
}

func (m *MemMailer) Last() (Message, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.Messages) == 0 {
		return Message{}, false
	}
	return m.Messages[len(m.Messages)-1], true
}

// Compose builds a message ready to send.
func Compose(to, from, subject, html string) Message {
	return Message{To: to, From: from, Subject: subject, HTMLBody: html}
}
