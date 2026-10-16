package notification

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// Mail is a plain-text message.
type Mail struct {
	To      string
	Subject string
	Text    string
	// UnsubscribeURL, when set, adds RFC 8058 one-click List-Unsubscribe headers.
	UnsubscribeURL string
}

// Mailer sends one message.
type Mailer interface {
	Send(ctx context.Context, m Mail) error
}

// ValidEmail normalises and checks a bare address: no display name, one @,
// bounded length, no control characters.
func ValidEmail(raw string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" || len(s) > 254 || strings.ContainsAny(s, "\r\n\x00<>\" ,;") {
		return "", errors.New("invalid email address")
	}
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s || a.Name != "" {
		return "", errors.New("invalid email address")
	}
	at := strings.LastIndex(s, "@")
	if at < 1 || !strings.Contains(s[at+1:], ".") {
		return "", errors.New("invalid email address")
	}
	return s, nil
}

// MaskEmail hides most of the local part: "alice@example.com" -> "a***@example.com".
func MaskEmail(addr string) string {
	at := strings.LastIndex(addr, "@")
	if at < 1 {
		return ""
	}
	return addr[:1] + "***" + addr[at:]
}

// SMTP sends through a configured SMTP server.
type SMTP struct {
	Host, Username, Password string
	Port                     int
	From                     string
	// Security is "starttls", "tls" or "none".
	Security string
	Timeout  time.Duration
}

func (s *SMTP) port() int {
	if s.Port != 0 {
		return s.Port
	}
	if s.Security == "tls" {
		return 465
	}
	if s.Security == "none" {
		return 25
	}
	return 587
}

// Send delivers m. Every header value is checked so recipient input can't inject
// extra headers or recipients.
func (s *SMTP) Send(ctx context.Context, m Mail) error {
	to, err := ValidEmail(m.To)
	if err != nil {
		return err
	}
	from, err := mail.ParseAddress(s.From)
	if err != nil {
		return err
	}
	if strings.ContainsAny(m.Subject, "\r\n") || strings.ContainsAny(m.UnsubscribeURL, "\r\n<> ") {
		return errors.New("invalid header value")
	}
	msg, err := buildMessage(from, to, m, time.Now().UTC())
	if err != nil {
		return err
	}
	timeout := s.Timeout
	if timeout == 0 {
		timeout = 20 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	addr := net.JoinHostPort(s.Host, strconv.Itoa(s.port()))
	dialer := &net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("smtp connect: %w", err)
	}
	deadline, _ := ctx.Deadline()
	_ = conn.SetDeadline(deadline)
	tlsConfig := &tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12}
	if s.Security == "tls" {
		conn = tls.Client(conn, tlsConfig)
	}
	client, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("smtp greeting: %w", err)
	}
	defer client.Close()
	if s.Security == "starttls" || s.Security == "" {
		if err = client.StartTLS(tlsConfig); err != nil {
			return fmt.Errorf("smtp starttls: %w", err)
		}
	}
	if s.Username != "" {
		if err = client.Auth(smtp.PlainAuth("", s.Username, s.Password, s.Host)); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err = client.Mail(from.Address); err != nil {
		return err
	}
	if err = client.Rcpt(to); err != nil {
		return err
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err = w.Write(msg); err != nil {
		return err
	}
	if err = w.Close(); err != nil {
		return err
	}
	return client.Quit()
}

func buildMessage(from *mail.Address, to string, m Mail, now time.Time) ([]byte, error) {
	id := make([]byte, 12)
	if _, err := rand.Read(id); err != nil {
		return nil, err
	}
	domain := "localhost"
	if at := strings.LastIndex(from.Address, "@"); at >= 0 {
		domain = from.Address[at+1:]
	}
	var b bytes.Buffer
	h := func(k, v string) { fmt.Fprintf(&b, "%s: %s\r\n", k, v) }
	h("From", from.String())
	h("To", to)
	h("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	h("Date", now.Format(time.RFC1123Z))
	h("Message-ID", "<"+hex.EncodeToString(id)+"@"+domain+">")
	h("MIME-Version", "1.0")
	h("Content-Type", `text/plain; charset="utf-8"`)
	h("Content-Transfer-Encoding", "quoted-printable")
	h("Auto-Submitted", "auto-generated")
	if m.UnsubscribeURL != "" {
		h("List-Unsubscribe", "<"+m.UnsubscribeURL+">")
		h("List-Unsubscribe-Post", "List-Unsubscribe=One-Click")
	}
	b.WriteString("\r\n")
	qp := quotedprintable.NewWriter(&b)
	text := strings.ReplaceAll(strings.ReplaceAll(m.Text, "\r\n", "\n"), "\n", "\r\n")
	if _, err := qp.Write([]byte(text)); err != nil {
		return nil, err
	}
	if err := qp.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
